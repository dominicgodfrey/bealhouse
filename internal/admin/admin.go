// Package admin is authentication for the owner's console (decision #15).
//
// # Passkeys, not passwords
//
// The console is opened from two phones belonging to the two people who run the
// inn. It shows every guest's contact details and can move money, and it is
// reachable from the public internet — so the thing guarding it should not be a
// string two people share, retype, and eventually put in a message to each
// other.
//
// So there is no password anywhere in this package. Sign-in is WebAuthn: the
// phone holds a private key it will only use after its own biometric check, and
// this server holds the matching public key. What follows from that is most of
// the reason it was chosen:
//
//   - Nothing stored here is a credential. An attacker who reads every table
//     this package writes cannot sign in with any of it.
//   - It cannot be phished. The signature is bound to the origin by the
//     browser, so a convincing copy of the login page on another domain gets an
//     assertion the real server will reject.
//   - There is nothing to rotate, share, or leave in a password manager both
//     owners can read.
//
// # What the pieces are for
//
// Four things, and each exists because the alternative has a specific hole in
// it:
//
//   - **Sessions are rows, hashed.** The requirement is that a phone stays
//     signed in, and "indefinitely" is only a safe thing to offer next to a
//     list somebody can strike a line through. A signed stateless token cannot
//     be revoked when a phone is lost. The token is stored as its SHA-256, so
//     reading the table does not hand anyone a live session.
//   - **Enrolment tokens are single use.** Enrolling a passkey creates a
//     permanent way in, so the thing authorising it must be spendable exactly
//     once — which an HMAC link is not. Claiming is one UPDATE ... RETURNING,
//     so two phones racing for the same token produce one row and one winner.
//   - **Ceremonies are rows, deleted on use.** A challenge that can be answered
//     twice is a signature that can be replayed.
//   - **Every refusal is the same refusal.** A token that expired, one that was
//     already spent, one that never existed and one that was forged all come
//     back as ErrDenied. Distinguishing them tells whoever is trying which
//     guesses were close.
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// ErrDenied is the only refusal this package produces.
//
// Deliberately one error and not five. An endpoint that answers "expired" to
// one token and "unknown" to another is an oracle: it tells somebody feeding it
// guesses which of them were real, which is exactly the information the guessing
// is for. The server log gets the detail; the caller gets a closed door.
var ErrDenied = errors.New("admin: denied")

// ErrNotConfigured means there is no origin to verify a signature against, so
// nothing can sign in.
//
// A WebAuthn assertion is bound to an origin. Without knowing which origin this
// deployment answers to there is no way to tell a real one from a copy hosted
// somewhere else, so the console refuses rather than guessing — the same shape
// as a missing BOOKING_LINK_SECRET.
var ErrNotConfigured = errors.New("admin: no site origin configured; the console cannot accept sign-ins")

const (
	// SessionLifetime is how long a session survives without being used, and it
	// rolls forward every time it is.
	//
	// "Stay signed in" in practice: a phone opened even once a year never signs
	// in again, and one that stopped being used — lost, sold, stolen and wiped
	// — stops working on its own without anybody having to remember to revoke
	// it. Indefinite with no floor at all would mean a session found on an old
	// handset years later still worked.
	SessionLifetime = 365 * 24 * time.Hour

	// touchInterval is how stale last_seen_at is allowed to get before the
	// session row is written to. Without it the console writes a row per
	// request; with it, the rolling expiry is still accurate to the hour, which
	// is far finer than a year needs.
	touchInterval = time.Hour

	// EnrollmentLifetime is how long an enrolment token is good for.
	//
	// Short, because it is meant to be minted and used in the same few minutes
	// with the phone in hand. It is also single use, so this is the second lock
	// rather than the only one.
	EnrollmentLifetime = 15 * time.Minute

	// ceremonyLifetime bounds a half-finished WebAuthn exchange. Long enough
	// for somebody to find their glasses, short enough that an abandoned
	// challenge is not sitting there tomorrow.
	ceremonyLifetime = 5 * time.Minute

	// tokenBytes is the size of a session token and an enrolment token.
	//
	// 32 bytes of crypto/rand is 256 bits of entropy, which is not guessable by
	// anything, and is why the stored SHA-256 needs no salt and no work factor:
	// there is no dictionary to run against a value nobody chose.
	tokenBytes = 32

	// userAgentLimit truncates what gets stored for the device list. It is a
	// display string and never a decision, so it needs no more room than a
	// person will read.
	userAgentLimit = 200
)

// Beginner is anything that can start a transaction: a *pgxpool.Pool in the
// server, a pgx.Tx in tests.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RP is the relying party: which origin this console answers to.
type RP struct {
	// ID is the domain a credential is scoped to — "bealhouse.com", no scheme
	// and no port. A key registered against one ID cannot be used against
	// another, which is what makes a copy of the login page on a different
	// domain useless.
	ID string

	// DisplayName is what the phone's passkey prompt shows.
	DisplayName string

	// Origins are the full origins allowed to complete a ceremony.
	Origins []string
}

// NewRP works out the relying party from the site's public origin.
//
// In dev, with no SITE_URL, it answers to localhost — both the Go binary's port
// and Vite's, because `npm run dev` serves the console from 5173 and proxies
// the API to 8080, so the browser's origin is the one it must accept. Browsers
// exempt localhost from the HTTPS requirement, which is the only reason this
// works without certificates.
//
// Outside dev an empty SITE_URL returns no RP at all, and the console refuses
// every sign-in. Guessing an origin would mean accepting assertions minted for
// somewhere else.
func NewRP(siteURL string, isDev bool) (*RP, error) {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")

	if siteURL == "" {
		if !isDev {
			return nil, ErrNotConfigured
		}
		return &RP{
			ID:          "localhost",
			DisplayName: "The Beal House",
			Origins:     []string{"http://localhost:8080", "http://localhost:5173"},
		}, nil
	}

	u, err := url.Parse(siteURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("admin: SITE_URL %q is not an origin a passkey can be scoped to", siteURL)
	}

	rp := &RP{ID: u.Hostname(), DisplayName: "The Beal House", Origins: []string{siteURL}}

	// A developer who has set SITE_URL to a localhost address still runs the
	// SPA on Vite's port. Only ever widened for localhost, and only in dev — so
	// a production origin list is exactly the site's own origin and nothing
	// else, which is what TestTheConsoleRefusesToRunWithNoOrigin pins down.
	if isDev && rp.ID == "localhost" {
		rp.Origins = dedupe(append(rp.Origins, "http://localhost:5173", "http://localhost:8080"))
	}
	return rp, nil
}

// dedupe keeps the first of each origin, so a SITE_URL that already names one
// of the dev ports does not produce a list with it in twice.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Console is admin authentication.
type Console struct {
	wa       *webauthn.WebAuthn
	q        *db.Queries
	beginner Beginner
}

// New builds the console's authentication.
//
// The authenticator policy is three settings and each one is load-bearing:
//
//   - **Resident key required.** The credential is stored on the phone with the
//     account it belongs to, so signing in is one tap with nothing typed first.
//     There is no username on the login page because there is nothing to type.
//   - **User verification required.** The phone must check who is holding it —
//     Face ID, a fingerprint, the device passcode — not merely that somebody
//     touched it. Without this a passkey on an unlocked phone is a key with no
//     lock, which is most of the point of choosing phones as the authenticator.
//   - **Attestation: none.** Attestation says which make of authenticator this
//     is, and verifying it needs the FIDO metadata service. Asking for
//     something nobody checks is theatre; two phones the owners hold are not a
//     population that needs vetting.
//
// Authenticator attachment is deliberately left unset. Requiring "platform"
// would restrict enrolment to the phone's built-in authenticator, which is what
// the owners will use anyway — leaving it open lets a hardware key be enrolled
// as a spare at no cost to any of the above.
func New(rp *RP, q *db.Queries, beginner Beginner) (*Console, error) {
	if rp == nil {
		return nil, ErrNotConfigured
	}

	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  rp.ID,
		RPDisplayName:         rp.DisplayName,
		RPOrigins:             rp.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("admin: configuring webauthn: %w", err)
	}
	return &Console{wa: wa, q: q, beginner: beginner}, nil
}

// Identity is who is on the other end of a request.
type Identity struct {
	UserID int64
	Name   string

	// PasskeyID is the phone this session was opened from. Empty if that
	// passkey has since been revoked, which leaves the session recognisable and
	// already dead.
	PasskeyID []byte
}

// account adapts a row to what the WebAuthn library expects of a user.
type account struct {
	id     int64
	handle []byte
	name   string
	creds  []webauthn.Credential
}

func (a *account) WebAuthnID() []byte                         { return a.handle }
func (a *account) WebAuthnName() string                       { return a.name }
func (a *account) WebAuthnDisplayName() string                { return a.name }
func (a *account) WebAuthnCredentials() []webauthn.Credential { return a.creds }

// loadAccount reads a user and every passkey enrolled against it.
//
// The credentials come back as the library wrote them. A row that will not
// decode is an error rather than a skip: silently signing somebody in against a
// subset of their keys is how a revoked-looking credential turns out to still
// work.
func loadAccount(ctx context.Context, q *db.Queries, u db.User) (*account, error) {
	rows, err := q.ListPasskeys(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("admin: loading passkeys: %w", err)
	}

	a := &account{id: u.ID, handle: u.Handle, name: u.Name, creds: make([]webauthn.Credential, 0, len(rows))}
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(row.Credential, &c); err != nil {
			return nil, fmt.Errorf("admin: decoding passkey %x: %w", row.ID, err)
		}
		a.creds = append(a.creds, c)
	}
	return a, nil
}

// newToken mints a token and the hash to store beside it.
func newToken() (token string, hash []byte, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("admin: generating a token: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// hashToken turns a token from a cookie or a URL back into its stored form.
//
// Malformed input hashes to something that matches nothing rather than
// returning an error, so a garbage token takes the same path as a wrong one.
func hashToken(token string) []byte {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		sum := sha256.Sum256([]byte(token))
		return sum[:]
	}
	sum := sha256.Sum256(raw)
	return sum[:]
}

// randomID is a ceremony id: opaque, unguessable, and never a secret on its own
// — the ceremony it names is still bound to the challenge inside it.
func randomID() ([]byte, error) {
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("admin: generating an id: %w", err)
	}
	return id, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
