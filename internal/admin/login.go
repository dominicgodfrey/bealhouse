package admin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// BeginLogin issues a sign-in challenge.
//
// Discoverable, which is why the login page has no username field: the phone
// already holds the account handle beside the key and offers both. There is
// nothing to type, and nothing for a caller to enumerate — this endpoint says
// the same thing whether or not anybody has ever enrolled.
func (c *Console) BeginLogin(ctx context.Context) (*protocol.CredentialAssertion, []byte, error) {
	if err := c.q.SweepExpiredAuth(ctx); err != nil {
		slog.Error("could not sweep expired admin auth rows", "err", err)
	}

	assertion, session, err := c.wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, nil, fmt.Errorf("admin: starting the sign-in: %w", err)
	}

	id, err := c.storeCeremony(ctx, "login", session, nil)
	if err != nil {
		return nil, nil, err
	}
	return assertion, id, nil
}

// FinishLogin validates the phone's signature and opens a session.
//
// The credential is written back in the same transaction that opens the
// session, and that is not bookkeeping: the stored blob carries the
// authenticator's signature counter, and a counter that goes backwards is how a
// cloned key is spotted. Skipping the write would mean comparing every future
// sign-in against the count from the day the phone enrolled.
//
// Every way this can fail — no ceremony, a bad signature, a credential that is
// not on file, an account that has gone — answers ErrDenied. The reason goes to
// the log, where the owner can find it and a caller cannot.
func (c *Console) FinishLogin(ctx context.Context, ceremonyID []byte, r *http.Request) (Session, Identity, error) {
	cer, err := c.consumeCeremony(ctx, ceremonyID, "login")
	if err != nil {
		return Session{}, Identity{}, err
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(cer.Session, &session); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: decoding the sign-in challenge: %w", err)
	}

	// The handle the phone hands back is the only thing that names an account
	// here. It is matched against the users table rather than trusted, and a
	// handle nobody has is refused like any other bad assertion.
	var acct *account
	lookup := func(rawID, handle []byte) (webauthn.User, error) {
		u, err := c.q.GetUserByHandle(ctx, handle)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDenied
		}
		if err != nil {
			return nil, fmt.Errorf("admin: loading the account: %w", err)
		}

		acct, err = loadAccount(ctx, c.q, u)
		if err != nil {
			return nil, err
		}
		return acct, nil
	}

	_, credential, err := c.wa.FinishPasskeyLogin(lookup, session, r)
	if err != nil {
		slog.Warn("admin sign-in refused", "err", err)
		return Session{}, Identity{}, ErrDenied
	}
	if acct == nil {
		return Session{}, Identity{}, ErrDenied
	}

	// The library sets this when the authenticator's counter did not advance the
	// way a genuine one would. It is not proof of a clone — some authenticators
	// do not count at all — but it is the only signal there is, and an admin
	// console is where it is worth saying out loud.
	if credential.Authenticator.CloneWarning {
		slog.Error("an admin passkey signed in with a suspect signature counter; it may have been cloned",
			"passkey", fmt.Sprintf("%x", credential.ID))
	}

	encoded, err := json.Marshal(credential)
	if err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: encoding the passkey: %w", err)
	}

	tx, err := c.beginner.Begin(ctx)
	if err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: beginning the sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	if err := q.UpdatePasskeyAfterUse(ctx, db.UpdatePasskeyAfterUseParams{
		ID:         credential.ID,
		Credential: encoded,
	}); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: recording the sign-in: %w", err)
	}

	out, err := openSession(ctx, q, acct.id, credential.ID, r.UserAgent())
	if err != nil {
		return Session{}, Identity{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Session{}, Identity{}, fmt.Errorf("admin: committing the sign-in: %w", err)
	}

	return out, Identity{UserID: acct.id, Name: acct.name, PasskeyID: credential.ID}, nil
}

// storeCeremony records a challenge so the finish step can find it.
//
// The returned id travels to the browser in a short-lived cookie. It names the
// row and nothing else: knowing it does not help anyone answer the challenge
// inside.
func (c *Console) storeCeremony(ctx context.Context, purpose string, session *webauthn.SessionData, enrollment []byte) ([]byte, error) {
	encoded, err := json.Marshal(session)
	if err != nil {
		return nil, fmt.Errorf("admin: encoding the challenge: %w", err)
	}

	id, err := randomID()
	if err != nil {
		return nil, err
	}

	if err := c.q.CreateCeremony(ctx, db.CreateCeremonyParams{
		ID:              id,
		Purpose:         purpose,
		Session:         encoded,
		Enrollment:      enrollment,
		LifetimeSeconds: ceremonyLifetime.Seconds(),
	}); err != nil {
		return nil, fmt.Errorf("admin: storing the challenge: %w", err)
	}
	return id, nil
}

// consumeCeremony takes a challenge, once.
//
// The DELETE ... RETURNING is what makes it once: a challenge still on file
// after it has been answered is a signature that can be replayed, and two
// concurrent finishes would both succeed if the read and the delete were
// separate statements.
//
// The purpose is checked here as well as constrained in the schema, so a login
// response cannot be submitted against a registration challenge or the reverse.
func (c *Console) consumeCeremony(ctx context.Context, id []byte, purpose string) (db.WebauthnCeremony, error) {
	if len(id) == 0 {
		return db.WebauthnCeremony{}, ErrDenied
	}

	cer, err := c.q.ConsumeCeremony(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WebauthnCeremony{}, ErrDenied
	}
	if err != nil {
		return db.WebauthnCeremony{}, fmt.Errorf("admin: loading the challenge: %w", err)
	}
	if cer.Purpose != purpose {
		return db.WebauthnCeremony{}, ErrDenied
	}
	return cer, nil
}

// randRead is crypto/rand, named so the account-handle path reads the same as
// the token path.
func randRead(b []byte) (int, error) { return rand.Read(b) }
