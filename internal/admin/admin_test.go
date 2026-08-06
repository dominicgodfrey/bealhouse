package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// These tests are adversarial on purpose. The console guards every guest's
// contact details and the ability to move money, so what is worth asserting is
// not that a correct sign-in works — it is that each of the specific ways this
// could be got around does not.
//
// The auth tables are this package's alone: nothing else in the suite reads or
// writes users, user_passkeys, user_sessions, user_enrollments or
// webauthn_ceremonies. So the committing tests here clean up after themselves
// rather than taking testdb.Exclusive, which exists to stop packages deleting
// each other's rows out from under a race.

func setup(t *testing.T) (context.Context, *Console, *db.Queries, *pgxpool.Pool) {
	t.Helper()

	pool := testdb.Connect(t)
	q := db.New(pool)

	rp, err := NewRP("https://admin.test", false)
	if err != nil {
		t.Fatalf("building the relying party: %v", err)
	}
	console, err := New(rp, q, pool)
	if err != nil {
		t.Fatalf("building the console: %v", err)
	}
	return context.Background(), console, q, pool
}

// makeUser creates an account and removes it, and everything hanging off it,
// when the test ends.
func makeUser(t *testing.T, ctx context.Context, q *db.Queries, pool *pgxpool.Pool) db.User {
	t.Helper()

	handle := make([]byte, 64)
	if _, err := rand.Read(handle); err != nil {
		t.Fatalf("generating a handle: %v", err)
	}

	u, err := q.CreateUser(ctx, db.CreateUserParams{Handle: handle, Name: "Test Inn"})
	if err != nil {
		t.Fatalf("creating the account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	return u
}

// makePasskey enrols a credential without a ceremony. The WebAuthn exchange
// itself is the library's to get right and is exercised by its own tests; what
// is under test here is everything this package hangs around it.
func makePasskey(t *testing.T, ctx context.Context, q *db.Queries, userID int64, label string) []byte {
	t.Helper()

	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatalf("generating a credential id: %v", err)
	}

	encoded, err := json.Marshal(webauthn.Credential{ID: id})
	if err != nil {
		t.Fatalf("encoding the credential: %v", err)
	}
	if err := q.CreatePasskey(ctx, db.CreatePasskeyParams{
		ID: id, UserID: userID, Label: label, Credential: encoded,
	}); err != nil {
		t.Fatalf("creating the passkey: %v", err)
	}
	return id
}

// signIn opens a session the way a completed ceremony would.
func signIn(t *testing.T, ctx context.Context, q *db.Queries, userID int64, passkeyID []byte) Session {
	t.Helper()

	s, err := openSession(ctx, q, userID, passkeyID, "test-agent")
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	return s
}

// The token in the cookie must not be the token in the table. Anyone who can
// read user_sessions — a leaked dump, a backup on a laptop — would otherwise be
// holding live admin sessions.
func TestSessionTokensAreNeverStoredInTheClear(t *testing.T) {
	ctx, _, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)
	pk := makePasskey(t, ctx, q, u.ID, "Phone")

	s := signIn(t, ctx, q, u.ID, pk)

	var stored []byte
	err := pool.QueryRow(ctx,
		"SELECT token_hash FROM user_sessions WHERE user_id = $1", u.ID).Scan(&stored)
	if err != nil {
		t.Fatalf("reading the session row: %v", err)
	}

	if bytes.Contains(stored, []byte(s.Token)) {
		t.Fatal("the session token is in the table verbatim")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s.Token)
	if err != nil {
		t.Fatalf("the token is not the shape it claims: %v", err)
	}
	if bytes.Contains(stored, raw) {
		t.Fatal("the raw token bytes are in the table")
	}

	want := sha256.Sum256(raw)
	if !bytes.Equal(stored, want[:]) {
		t.Fatal("the stored value is not the token's SHA-256")
	}
	if len(raw) < 32 {
		t.Errorf("the token is %d bytes; that is not enough to be unguessable", len(raw))
	}
}

// Every way a session can be no good has to look the same from outside.
func TestEveryBadSessionIsTheSameRefusal(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)
	pk := makePasskey(t, ctx, q, u.ID, "Phone")

	revoked := signIn(t, ctx, q, u.ID, pk)
	if _, err := q.RevokeSession(ctx, hashToken(revoked.Token)); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	expired := signIn(t, ctx, q, u.ID, pk)
	if _, err := pool.Exec(ctx,
		"UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE token_hash = $1",
		hashToken(expired.Token)); err != nil {
		t.Fatalf("expiring: %v", err)
	}

	invented, _, err := newToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	for _, c := range []struct {
		name  string
		token string
	}{
		{"revoked", revoked.Token},
		{"expired", expired.Token},
		{"never existed", invented},
		{"empty", ""},
		{"not base64", "!!!!not a token!!!!"},
		{"the stored hash itself", base64.RawURLEncoding.EncodeToString(hashToken(revoked.Token))},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := console.Authenticate(ctx, c.token); !errors.Is(err, ErrDenied) {
				t.Errorf("error %v, want ErrDenied — a caller must not be able to tell these apart", err)
			}
		})
	}
}

// A live session resolves to its own account and nobody else's.
func TestALiveSessionResolvesToItsOwnAccount(t *testing.T) {
	ctx, console, q, pool := setup(t)

	mine := makeUser(t, ctx, q, pool)
	theirs := makeUser(t, ctx, q, pool)
	pk := makePasskey(t, ctx, q, mine.ID, "Phone")

	s := signIn(t, ctx, q, mine.ID, pk)

	id, err := console.Authenticate(ctx, s.Token)
	if err != nil {
		t.Fatalf("authenticating: %v", err)
	}
	if id.UserID != mine.ID {
		t.Errorf("resolved to account %d, want %d", id.UserID, mine.ID)
	}
	if id.UserID == theirs.ID {
		t.Error("a session resolved to somebody else's account")
	}
}

// The whole security property of an invitation: it enrols one phone.
//
// Concurrent, because the interesting failure is not calling it twice in a row
// — it is two requests arriving together and both passing a check that was
// separate from the claim.
func TestAnInvitationCanOnlyBeSpentOnce(t *testing.T) {
	ctx, _, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)

	token, hash, err := newToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}
	if err := q.CreateEnrollment(ctx, db.CreateEnrollmentParams{
		TokenHash: hash, Label: "Phone", UserID: &u.ID, LifetimeSeconds: 900,
	}); err != nil {
		t.Fatalf("creating the invitation: %v", err)
	}

	const racers = 24
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won int

	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.New(pool).ClaimEnrollment(context.Background(), hashToken(token))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, pgx.ErrNoRows):
			default:
				t.Errorf("claiming: %v", err)
			}
		}()
	}
	wg.Wait()

	if won != 1 {
		t.Fatalf("%d of %d racers claimed the same invitation; it must be exactly 1", won, racers)
	}
}

// A challenge that can be answered twice is a signature that can be replayed.
func TestAChallengeCanOnlyBeAnsweredOnce(t *testing.T) {
	ctx, _, q, pool := setup(t)

	id, err := randomID()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := q.CreateCeremony(ctx, db.CreateCeremonyParams{
		ID: id, Purpose: "login", Session: []byte(`{"challenge":"x"}`), LifetimeSeconds: 300,
	}); err != nil {
		t.Fatalf("storing the challenge: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM webauthn_ceremonies WHERE id = $1", id)
	})

	const racers = 24
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won int

	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.New(pool).ConsumeCeremony(context.Background(), id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, pgx.ErrNoRows):
			default:
				t.Errorf("consuming: %v", err)
			}
		}()
	}
	wg.Wait()

	if won != 1 {
		t.Fatalf("%d of %d racers consumed the same challenge; it must be exactly 1", won, racers)
	}
}

// A sign-in response must not be submittable against a registration challenge,
// or the reverse: those two ceremonies prove different things.
func TestAChallengeCannotBeUsedForTheOtherCeremony(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)

	token, hash, err := newToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}
	if err := q.CreateEnrollment(ctx, db.CreateEnrollmentParams{
		TokenHash: hash, Label: "Phone", UserID: &u.ID, LifetimeSeconds: 900,
	}); err != nil {
		t.Fatalf("creating the invitation: %v", err)
	}

	_, ceremony, err := console.BeginEnrollment(ctx, token)
	if err != nil {
		t.Fatalf("starting the registration: %v", err)
	}

	// A registration challenge, offered to the sign-in path.
	if _, err := console.consumeCeremony(ctx, ceremony, "login"); !errors.Is(err, ErrDenied) {
		t.Errorf("error %v, want ErrDenied", err)
	}
}

func TestAnExpiredInvitationIsRefused(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)

	token, hash, err := newToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}
	if err := q.CreateEnrollment(ctx, db.CreateEnrollmentParams{
		TokenHash: hash, Label: "Phone", UserID: &u.ID, LifetimeSeconds: -1,
	}); err != nil {
		t.Fatalf("creating the invitation: %v", err)
	}

	if _, _, err := console.BeginEnrollment(ctx, token); !errors.Is(err, ErrDenied) {
		t.Errorf("error %v, want ErrDenied", err)
	}
}

func TestAnInventedInvitationIsRefused(t *testing.T) {
	ctx, console, _, _ := setup(t)

	invented, _, err := newToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}
	for _, token := range []string{invented, "", "................"} {
		if _, _, err := console.BeginEnrollment(ctx, token); !errors.Is(err, ErrDenied) {
			t.Errorf("token %q: error %v, want ErrDenied", token, err)
		}
	}
}

// Revoking a phone has to take its live session with it, or the lost handset
// keeps working for as long as the session lasts — which is a year.
func TestRevokingAPasskeySignsThatPhoneOut(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)

	lost := makePasskey(t, ctx, q, u.ID, "Lost phone")
	kept := makePasskey(t, ctx, q, u.ID, "Other phone")

	lostSession := signIn(t, ctx, q, u.ID, lost)
	keptSession := signIn(t, ctx, q, u.ID, kept)

	if err := console.RevokePasskey(ctx, u.ID, lost); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	if _, err := console.Authenticate(ctx, lostSession.Token); !errors.Is(err, ErrDenied) {
		t.Error("the revoked phone's session still works")
	}
	if _, err := console.Authenticate(ctx, keptSession.Token); err != nil {
		t.Errorf("revoking one phone signed the other out too: %v", err)
	}
}

// One owner's console must not be able to remove the other account's phone by
// naming its id.
func TestAPasskeyCannotBeRevokedFromAnotherAccount(t *testing.T) {
	ctx, console, q, pool := setup(t)

	mine := makeUser(t, ctx, q, pool)
	theirs := makeUser(t, ctx, q, pool)

	makePasskey(t, ctx, q, mine.ID, "Mine A")
	makePasskey(t, ctx, q, mine.ID, "Mine B")
	victim := makePasskey(t, ctx, q, theirs.ID, "Theirs")
	makePasskey(t, ctx, q, theirs.ID, "Theirs spare")

	if err := console.RevokePasskey(ctx, mine.ID, victim); !errors.Is(err, ErrDenied) {
		t.Fatalf("error %v, want ErrDenied", err)
	}

	if _, err := q.GetPasskey(ctx, victim); err != nil {
		t.Errorf("the other account's passkey was removed anyway: %v", err)
	}
}

// Removing the only key leaves a console nobody can open, recoverable only from
// a shell on the server. That is a recovery path, not a tap away.
func TestTheLastPasskeyCannotBeRemoved(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)
	only := makePasskey(t, ctx, q, u.ID, "Only phone")

	if err := console.RevokePasskey(ctx, u.ID, only); !errors.Is(err, ErrLastPasskey) {
		t.Fatalf("error %v, want ErrLastPasskey", err)
	}
	if _, err := q.GetPasskey(ctx, only); err != nil {
		t.Errorf("the only passkey was removed anyway: %v", err)
	}
}

// "Signed in for a year" has to mean a year from last use, not from first.
func TestUsingASessionPushesItsExpiryForward(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)
	pk := makePasskey(t, ctx, q, u.ID, "Phone")

	s := signIn(t, ctx, q, u.ID, pk)
	hash := hashToken(s.Token)

	// Age it past the touch interval and pull the expiry in, as a session used
	// eleven months ago would look.
	if _, err := pool.Exec(ctx, `
		UPDATE user_sessions
		SET last_seen_at = now() - interval '2 hours',
		    expires_at   = now() + interval '1 hour'
		WHERE token_hash = $1`, hash); err != nil {
		t.Fatalf("ageing the session: %v", err)
	}

	if _, err := console.Authenticate(ctx, s.Token); err != nil {
		t.Fatalf("authenticating: %v", err)
	}

	var expires time.Time
	if err := pool.QueryRow(ctx,
		"SELECT expires_at FROM user_sessions WHERE token_hash = $1", hash).Scan(&expires); err != nil {
		t.Fatalf("reading the expiry: %v", err)
	}
	if time.Until(expires) < SessionLifetime-24*time.Hour {
		t.Errorf("expiry is %v away; using a session should have rolled it forward a year", time.Until(expires))
	}
}

// An expired session is not renewed by being used. Rolling forward is for live
// sessions; a dead one coming back would make the year meaningless.
func TestAnExpiredSessionIsNotRevivedByUsingIt(t *testing.T) {
	ctx, console, q, pool := setup(t)
	u := makeUser(t, ctx, q, pool)
	pk := makePasskey(t, ctx, q, u.ID, "Phone")

	s := signIn(t, ctx, q, u.ID, pk)
	hash := hashToken(s.Token)

	if _, err := pool.Exec(ctx,
		"UPDATE user_sessions SET expires_at = now() - interval '1 day' WHERE token_hash = $1",
		hash); err != nil {
		t.Fatalf("expiring: %v", err)
	}

	if _, err := console.Authenticate(ctx, s.Token); !errors.Is(err, ErrDenied) {
		t.Fatalf("error %v, want ErrDenied", err)
	}

	var expires time.Time
	if err := pool.QueryRow(ctx,
		"SELECT expires_at FROM user_sessions WHERE token_hash = $1", hash).Scan(&expires); err != nil {
		t.Fatalf("reading the expiry: %v", err)
	}
	if expires.After(time.Now()) {
		t.Error("an expired session was pushed back into the future")
	}
}

// The schema, not the handler, is what makes a registration impossible without
// something authorising it.
func TestTheDatabaseRefusesARegistrationWithNoInvitation(t *testing.T) {
	ctx, _, q, _ := setup(t)

	id, err := randomID()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	err = q.CreateCeremony(ctx, db.CreateCeremonyParams{
		ID: id, Purpose: "register", Session: []byte(`{}`), LifetimeSeconds: 300,
	})
	if err == nil {
		_, _ = q.ConsumeCeremony(ctx, id)
		t.Fatal("a registration challenge was stored with no invitation behind it")
	}
}

// The relying party is what binds a passkey to this origin. Without one there
// is nothing to verify against, and guessing would accept assertions minted
// elsewhere.
func TestTheConsoleRefusesToRunWithNoOrigin(t *testing.T) {
	if _, err := NewRP("", false); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("error %v, want ErrNotConfigured outside dev", err)
	}

	rp, err := NewRP("", true)
	if err != nil {
		t.Fatalf("dev should fall back to localhost: %v", err)
	}
	if rp.ID != "localhost" {
		t.Errorf("dev relying party id %q, want localhost", rp.ID)
	}

	live, err := NewRP("https://bealhouse.com", false)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	if live.ID != "bealhouse.com" {
		t.Errorf("relying party id %q, want the site's host with no scheme", live.ID)
	}
	for _, origin := range live.Origins {
		if origin != "https://bealhouse.com" {
			t.Errorf("origin %q accepted in production; only the site's own origin should be", origin)
		}
	}
}

// The invitation must not be in the part of the URL that gets logged.
func TestTheInvitationTravelsInTheFragment(t *testing.T) {
	url := EnrollURL("https://bealhouse.com", "SECRET-TOKEN")

	if !bytes.Contains([]byte(url), []byte("#SECRET-TOKEN")) {
		t.Errorf("url %q does not carry the token in the fragment", url)
	}
	if bytes.Contains([]byte(url), []byte("?")) {
		t.Errorf("url %q puts the token in a query string, where every proxy logs it", url)
	}
}
