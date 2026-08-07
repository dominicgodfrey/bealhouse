package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// Session is a signed-in phone: the token the cookie carries, and when it dies
// if nothing touches it again.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// openSession issues a session for an account, inside the caller's transaction.
//
// Always called from the transaction that just validated a passkey, so the
// session and the credential update commit together. The token is returned once
// and never stored — only its hash reaches the database.
func openSession(ctx context.Context, q *db.Queries, userID int64, passkeyID []byte, userAgent string) (Session, error) {
	token, hash, err := newToken()
	if err != nil {
		return Session{}, err
	}

	if err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash:       hash,
		UserID:          userID,
		PasskeyID:       passkeyID,
		LifetimeSeconds: SessionLifetime.Seconds(),
		UserAgent:       truncate(userAgent, userAgentLimit),
	}); err != nil {
		return Session{}, fmt.Errorf("admin: opening a session: %w", err)
	}

	return Session{Token: token, ExpiresAt: time.Now().Add(SessionLifetime)}, nil
}

// Authenticate resolves a session token to whoever is holding it.
//
// Revoked, expired, and never-existed are one answer, decided in SQL rather
// than by conditions checked out here — see GetLiveSession. ErrDenied for all
// of them, so a caller feeding this tokens learns nothing from which ones come
// back differently.
//
// A live session has its expiry pushed forward, which is what makes "stays
// signed in" true for a phone in regular use. At most once an hour, so an open
// console is not writing a row per request.
func (c *Console) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrDenied
	}

	hash := hashToken(token)

	row, err := c.q.GetLiveSession(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrDenied
	}
	if err != nil {
		return Identity{}, fmt.Errorf("admin: loading the session: %w", err)
	}

	if time.Since(row.LastSeenAt) > touchInterval {
		// Best effort. A failure to roll the expiry forward is not a reason to
		// refuse a request from a session that is demonstrably still valid; the
		// worst case is that it expires on its original schedule.
		if err := c.q.TouchSession(ctx, db.TouchSessionParams{
			TokenHash:       hash,
			LifetimeSeconds: SessionLifetime.Seconds(),
		}); err != nil {
			slog.Error("could not extend an admin session", "err", err)
		}
	}

	return Identity{UserID: row.UserID, Name: row.Name, PasskeyID: row.PasskeyID}, nil
}

// SignOut revokes one session.
//
// Idempotent, and silent about whether there was anything to revoke: signing
// out of a session that has already gone is a success from where the caller is
// standing, and answering otherwise would say whether a token was ever real.
func (c *Console) SignOut(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if _, err := c.q.RevokeSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("admin: signing out: %w", err)
	}
	return nil
}

// credentialID is how a passkey's id crosses the wire.
//
// base64url and unpadded, which is what WebAuthn itself uses for a credential
// id and what DELETE /api/admin/passkeys/{id} decodes. A []byte field would be
// marshalled by encoding/json as *standard* base64 — padded, and containing +
// and / — which is neither a thing that survives a URL path segment nor a thing
// that route accepts. The list would then be handing out ids that can revoke
// nothing.
func credentialID(id []byte) string {
	if len(id) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id)
}

// Device is one signed-in phone, as the console lists them.
type Device struct {
	// PasskeyID identifies the phone. Empty when the passkey behind the session
	// has been revoked but the session row is still inside its expiry.
	PasskeyID string `json:"passkeyId,omitempty"`

	Label      string    `json:"label"`
	UserAgent  string    `json:"userAgent"`
	SignedInAt time.Time `json:"signedInAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`

	// Current marks the device asking. Without it the owner is looking at two
	// identical-looking rows deciding which one to sign out, and the answer
	// matters.
	Current bool `json:"current"`
}

// Devices lists the phones currently signed in.
func (c *Console) Devices(ctx context.Context, userID int64, currentToken string) ([]Device, error) {
	rows, err := c.q.ListLiveSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("admin: listing signed-in devices: %w", err)
	}

	current := hashToken(currentToken)

	out := make([]Device, 0, len(rows))
	for _, row := range rows {
		label := "Revoked passkey"
		if row.Label != nil {
			label = *row.Label
		}
		out = append(out, Device{
			PasskeyID:  credentialID(row.PasskeyID),
			Label:      label,
			UserAgent:  row.UserAgent,
			SignedInAt: row.CreatedAt,
			LastSeenAt: row.LastSeenAt,
			Current:    string(row.TokenHash) == string(current),
		})
	}
	return out, nil
}

// Passkey is one enrolled phone, as the console lists them.
type Passkey struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// Passkeys lists the phones that can sign in.
func (c *Console) Passkeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := c.q.ListPasskeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("admin: listing passkeys: %w", err)
	}

	out := make([]Passkey, 0, len(rows))
	for _, row := range rows {
		p := Passkey{ID: credentialID(row.ID), Label: row.Label, CreatedAt: row.CreatedAt}
		if row.LastUsedAt.Valid {
			used := row.LastUsedAt.Time
			p.LastUsedAt = &used
		}
		out = append(out, p)
	}
	return out, nil
}

// ErrLastPasskey refuses to remove the only way in.
//
// Deleting it would leave a console nobody can open, recoverable only by
// someone with shell access to the server minting a fresh enrolment. That is a
// recovery path, not a thing to walk into by tapping "remove" on the wrong row.
var ErrLastPasskey = errors.New("admin: that is the only passkey; enrol another phone before removing this one")

// RevokePasskey removes a phone and signs out everything opened from it.
//
// **Both, in one transaction.** Removing the key while its session kept working
// would be a revocation that revokes nothing — the lost phone stays inside the
// console until the session ages out a year later.
func (c *Console) RevokePasskey(ctx context.Context, userID int64, passkeyID []byte) error {
	tx, err := c.beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("admin: beginning the revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	remaining, err := q.CountPasskeys(ctx, userID)
	if err != nil {
		return fmt.Errorf("admin: counting passkeys: %w", err)
	}
	if remaining <= 1 {
		return ErrLastPasskey
	}

	// **Sessions first, then the key.** user_sessions.passkey_id is ON DELETE
	// SET NULL — which is what keeps a revoked phone's session readable in the
	// device list instead of disappearing — so deleting first would blank the
	// column and leave the revocation matching nothing. The lost handset would
	// then stay signed in for the rest of its year.
	if _, err := q.RevokeSessionsForPasskey(ctx, db.RevokeSessionsForPasskeyParams{
		PasskeyID: passkeyID,
		UserID:    userID,
	}); err != nil {
		return fmt.Errorf("admin: signing out the revoked passkey: %w", err)
	}

	removed, err := q.DeletePasskey(ctx, db.DeletePasskeyParams{ID: passkeyID, UserID: userID})
	if err != nil {
		return fmt.Errorf("admin: removing the passkey: %w", err)
	}
	if removed == 0 {
		// Either it never existed or it belongs to another account. Both are
		// the same answer, for the reason every other refusal here is — and the
		// rollback takes the revocations above with it, so naming somebody
		// else's key signs nobody out.
		return ErrDenied
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("admin: committing the revocation: %w", err)
	}
	return nil
}

// SignOutEverywhere revokes every session for an account.
//
// The button for a phone that has gone missing with the console open on it. It
// leaves the passkeys alone: the phone is locked, and the owner still has to be
// able to sign back in from the other one.
func (c *Console) SignOutEverywhere(ctx context.Context, userID int64) error {
	if _, err := c.q.RevokeAllSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("admin: signing out every device: %w", err)
	}
	return nil
}
