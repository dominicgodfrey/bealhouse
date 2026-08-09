package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// Enrollment is a minted invitation for one phone.
type Enrollment struct {
	Token     string
	Label     string
	ExpiresAt time.Time
}

// MintEnrollment creates a single-use invitation to enrol a phone.
//
// Two callers, and both of them already prove something: `bealhouse enroll`,
// which needs shell access to the server, and the console itself, which needs
// an existing signed-in phone. There is no third way in — a console with no
// passkeys and nobody able to reach the box is one that stays shut, which is
// the correct failure.
//
// The label is chosen here rather than typed on the phone, because the person
// minting it is the one who knows which handset they are about to hand it to.
func (c *Console) MintEnrollment(ctx context.Context, label string) (Enrollment, error) {
	if label == "" {
		label = "New phone"
	}

	token, hash, err := newToken()
	if err != nil {
		return Enrollment{}, err
	}

	// Attach to the shared account when there is one. NULL when there is not,
	// which is the very first enrolment: the account is created at the end of
	// the ceremony, so an invitation nobody accepts leaves nothing behind.
	var userID *int64
	if u, err := c.q.GetFirstUser(ctx); err == nil {
		userID = &u.ID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Enrollment{}, fmt.Errorf("admin: looking for the owner account: %w", err)
	}

	if err := c.q.CreateEnrollment(ctx, db.CreateEnrollmentParams{
		TokenHash:       hash,
		Label:           label,
		UserID:          userID,
		LifetimeSeconds: EnrollmentLifetime.Seconds(),
	}); err != nil {
		return Enrollment{}, fmt.Errorf("admin: creating the enrolment: %w", err)
	}

	return Enrollment{
		Token:     token,
		Label:     label,
		ExpiresAt: time.Now().Add(EnrollmentLifetime),
	}, nil
}

// EnrollURL is where a phone goes to accept an invitation.
//
// The token is in the fragment, after the `#`, and that is deliberate: a
// fragment is never sent to the server, never written to an access log, and
// never forwarded in a Referer header. A query string is all three, and this
// value is a one-shot key to the admin console.
//
// With no site URL there is no absolute address to build, so the bare path is
// returned and whoever is holding it knows where their own inn is.
func EnrollURL(siteURL, token string) string {
	return strings.TrimRight(siteURL, "/") + "/admin/enroll#" + token
}

// BeginEnrollment spends an invitation and issues a registration challenge.
//
// **The token is claimed here, at the start.** Claiming it at the end would
// leave a window in which two phones both begin against the same invitation and
// both finish, and a single-use token that can be used twice is not one. The
// claim is a single UPDATE ... RETURNING, so the database picks the winner.
//
// A failure after the claim releases it again — an invitation should not be
// burned by a WebAuthn error or a mis-tapped Face ID.
func (c *Console) BeginEnrollment(ctx context.Context, token string) (*protocol.CredentialCreation, []byte, error) {
	// Opportunistic tidying of expired ceremonies, invitations and long-dead
	// sessions. A handful of rows a year is not worth a scheduled job.
	if err := c.q.SweepExpiredAuth(ctx); err != nil {
		slog.Error("could not sweep expired admin auth rows", "err", err)
	}

	if token == "" {
		return nil, nil, ErrDenied
	}

	claimed, err := c.q.ClaimEnrollment(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		// Expired, already spent, forged, or never real. One answer.
		return nil, nil, ErrDenied
	}
	if err != nil {
		return nil, nil, fmt.Errorf("admin: claiming the enrolment: %w", err)
	}

	release := func() {
		if err := c.q.ReleaseEnrollment(context.WithoutCancel(ctx), claimed.TokenHash); err != nil {
			slog.Error("could not release a failed enrolment", "err", err)
		}
	}

	// The account this phone will join, or a fresh handle for one that does not
	// exist yet. Nothing is written for a new account here: an abandoned
	// ceremony must not leave an empty user behind, so creation waits for a
	// credential that actually validated.
	acct, err := c.enrollingAccount(ctx, claimed)
	if err != nil {
		release()
		return nil, nil, err
	}

	// Exclusions stop the same phone enrolling twice, which would leave the
	// owner a device list with two rows they cannot tell apart and one of them
	// revoking nothing.
	creation, session, err := c.wa.BeginRegistration(acct,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(acct.WebAuthnCredentials()).CredentialDescriptors()),
	)
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("admin: starting the registration: %w", err)
	}

	id, err := c.storeCeremony(ctx, "register", session, claimed.TokenHash)
	if err != nil {
		release()
		return nil, nil, err
	}
	return creation, id, nil
}

// enrollingAccount is the account the invitation joins.
//
// An invitation carrying a user id joins that account and must see its existing
// credentials, so the exclusion list is real. One carrying none is the first
// enrolment: a brand new handle, held only in the challenge until the ceremony
// succeeds.
func (c *Console) enrollingAccount(ctx context.Context, e db.UserEnrollment) (*account, error) {
	if e.UserID == nil {
		handle := make([]byte, 64)
		if _, err := randRead(handle); err != nil {
			return nil, err
		}
		return &account{handle: handle, name: "The Beal House"}, nil
	}

	u, err := c.q.GetUser(ctx, *e.UserID)
	if err != nil {
		return nil, fmt.Errorf("admin: loading the account being enrolled into: %w", err)
	}
	return loadAccount(ctx, c.q, u)
}

// FinishEnrollment validates the phone's new credential and signs it in.
//
// Signing in here rather than sending the owner to the login page: they have
// just completed a user-verified ceremony against a single-use invitation,
// which is strictly more than a sign-in proves. Making them do it twice would
// be friction bought with nothing.
//
// Everything that follows is one transaction — the account if it is new, the
// passkey, and the session. A partial enrolment is a credential the phone
// believes in that the server has no record of, which looks to the owner like a
// passkey that silently does not work.
func (c *Console) FinishEnrollment(ctx context.Context, ceremonyID []byte, r *http.Request) (Session, Identity, error) {
	cer, err := c.consumeCeremony(ctx, ceremonyID, "register")
	if err != nil {
		return Session{}, Identity{}, err
	}

	enrollment, err := c.q.GetEnrollment(ctx, cer.Enrollment)
	if err != nil {
		return Session{}, Identity{}, ErrDenied
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(cer.Session, &session); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: decoding the registration challenge: %w", err)
	}

	acct, err := c.enrollingAccountFrom(ctx, enrollment, session.UserID)
	if err != nil {
		return Session{}, Identity{}, err
	}

	credential, err := c.wa.FinishRegistration(acct, session, r)
	if err != nil {
		// The invitation was not spent on anything, so hand it back. The
		// ceremony is gone either way, which is what stops this being a retry
		// loop against one challenge.
		if err := c.q.ReleaseEnrollment(context.WithoutCancel(ctx), enrollment.TokenHash); err != nil {
			slog.Error("could not release a failed enrolment", "err", err)
		}
		slog.Warn("admin passkey registration failed", "err", err)
		return Session{}, Identity{}, ErrDenied
	}

	encoded, err := json.Marshal(credential)
	if err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: encoding the new passkey: %w", err)
	}

	tx, err := c.beginner.Begin(ctx)
	if err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: beginning the enrolment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	userID := acct.id
	if userID == 0 {
		created, err := q.CreateUser(ctx, db.CreateUserParams{Handle: acct.handle, Name: acct.name})
		if err != nil {
			return Session{}, Identity{}, fmt.Errorf("admin: creating the owner account: %w", err)
		}
		userID = created.ID
	}

	if err := q.CreatePasskey(ctx, db.CreatePasskeyParams{
		ID:         credential.ID,
		UserID:     userID,
		Label:      enrollment.Label,
		Credential: encoded,
	}); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: saving the passkey: %w", err)
	}

	out, err := openSession(ctx, q, userID, credential.ID, r.UserAgent())
	if err != nil {
		return Session{}, Identity{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: committing the enrolment: %w", err)
	}

	slog.Info("a phone was enrolled in the admin console", "label", enrollment.Label)
	return out, Identity{UserID: userID, Name: acct.name, PasskeyID: credential.ID}, nil
}

// enrollingAccountFrom rebuilds the account the challenge was issued against.
//
// The handle comes from the stored challenge rather than from anything the
// browser sent, so a finish step cannot be pointed at a different account than
// the one the ceremony started for.
func (c *Console) enrollingAccountFrom(ctx context.Context, e db.UserEnrollment, handle []byte) (*account, error) {
	if e.UserID != nil {
		u, err := c.q.GetUser(ctx, *e.UserID)
		if err != nil {
			return nil, fmt.Errorf("admin: loading the account being enrolled into: %w", err)
		}
		return loadAccount(ctx, c.q, u)
	}

	// First enrolment. The account may have been created by a ceremony that
	// finished in between, in which case join it rather than making a second.
	if u, err := c.q.GetUserByHandle(ctx, handle); err == nil {
		return loadAccount(ctx, c.q, u)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("admin: looking for the owner account: %w", err)
	}

	return &account{handle: handle, name: "The Beal House"}, nil
}
