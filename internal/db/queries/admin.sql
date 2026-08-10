-- name: CreateUser :one
INSERT INTO users (handle, name)
VALUES (sqlc.arg(handle), sqlc.arg(name))
RETURNING id, handle, name, created_at;

-- name: GetUser :one
SELECT id, handle, name, created_at FROM users WHERE id = sqlc.arg(id);

-- Look a user up by the handle their authenticator hands back at sign-in.
--
-- This is the only lookup the discoverable-credential flow can do: the phone
-- offers a key and the handle stored beside it, and the server has to turn that
-- into an account without ever having been told a username.
-- name: GetUserByHandle :one
SELECT id, handle, name, created_at FROM users WHERE handle = sqlc.arg(handle);

-- The shared owner account, if one exists yet.
--
-- Ordered and limited rather than assuming a single row: decision #15 is one
-- account today and the table is deliberately capable of more, so "the oldest"
-- is a defined answer instead of an error waiting for the second row.
-- name: GetFirstUser :one
SELECT id, handle, name, created_at FROM users ORDER BY id LIMIT 1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreatePasskey :exec
INSERT INTO user_passkeys (id, user_id, label, credential)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(label), sqlc.arg(credential));

-- Every passkey enrolled against one account.
--
-- Loaded in full on each sign-in because that is what the library validates
-- against, and shown to the owner as the list of phones they can revoke.
-- name: ListPasskeys :many
SELECT id, user_id, label, credential, created_at, last_used_at
FROM user_passkeys
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at;

-- name: GetPasskey :one
SELECT id, user_id, label, credential, created_at, last_used_at
FROM user_passkeys
WHERE id = sqlc.arg(id);

-- Write the credential back after a successful sign-in.
--
-- Not bookkeeping. The stored blob carries the authenticator's signature
-- counter, and the library can only spot a cloned key by comparing the count it
-- was just given against the one it last stored — which only works if the last
-- one was saved.
-- name: UpdatePasskeyAfterUse :exec
UPDATE user_passkeys
SET credential   = sqlc.arg(credential),
    last_used_at = now()
WHERE id = sqlc.arg(id);

-- Remove a phone.
--
-- Sessions opened from it are not deleted with it — they are revoked
-- separately, in the same transaction, so the row survives to say a phone was
-- signed in and is not any more.
-- name: DeletePasskey :execrows
DELETE FROM user_passkeys WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CountPasskeys :one
SELECT count(*) FROM user_passkeys WHERE user_id = sqlc.arg(user_id);

-- name: CreateSession :exec
INSERT INTO user_sessions (token_hash, user_id, passkey_id, expires_at, user_agent)
VALUES (
  sqlc.arg(token_hash),
  sqlc.arg(user_id),
  sqlc.arg(passkey_id),
  now() + make_interval(secs => sqlc.arg(lifetime_seconds)::double precision),
  sqlc.arg(user_agent)
);

-- Resolve a cookie to the account behind it.
--
-- Every condition that can refuse is in the WHERE clause rather than checked in
-- Go afterwards: a revoked or expired session must be indistinguishable from
-- one that never existed, and the surest way to keep those three answers
-- identical is for all of them to be "no row".
--
-- The expiry comparison uses the database's clock for the same reason job
-- scheduling does — two machines disagreeing about the time must not be able to
-- extend a session.
-- name: GetLiveSession :one
SELECT
  s.token_hash,
  s.user_id,
  s.passkey_id,
  s.created_at,
  s.last_seen_at,
  s.expires_at,
  s.user_agent,
  u.handle,
  u.name
FROM user_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash)
  AND s.revoked_at IS NULL
  AND s.expires_at > now();

-- Note that a session was used, and push its expiry out.
--
-- Rolling rather than fixed, which is what "stays signed in" means in practice:
-- a phone in daily use never has to sign in again, and one that stopped being
-- used stops working on its own. Called at most once an hour by the caller, so
-- an idle console is not writing a row per request.
-- name: TouchSession :exec
UPDATE user_sessions
SET last_seen_at = now(),
    expires_at   = now() + make_interval(secs => sqlc.arg(lifetime_seconds)::double precision)
WHERE token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL
  AND expires_at > now();

-- name: RevokeSession :execrows
UPDATE user_sessions
SET revoked_at = now()
WHERE token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL;

-- Sign out every session opened from one phone.
--
-- **Run before the passkey is deleted, in the same transaction.** The sessions
-- reference it with ON DELETE SET NULL — which is what keeps a revoked phone's
-- session readable in the device list rather than vanishing — so deleting first
-- clears passkey_id and leaves this matching nothing. The lost handset then
-- keeps working for the rest of the session's year, which is the exact opposite
-- of what pressing "remove" meant.
--
-- Scoped by user as well as by key, so revoking runs against the caller's own
-- account whatever id they name.
-- name: RevokeSessionsForPasskey :execrows
UPDATE user_sessions
SET revoked_at = now()
WHERE passkey_id = sqlc.arg(passkey_id)
  AND user_id = sqlc.arg(user_id)
  AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :execrows
UPDATE user_sessions
SET revoked_at = now()
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- The signed-in phones, for the console's device list.
-- name: ListLiveSessions :many
SELECT
  s.token_hash,
  s.passkey_id,
  s.created_at,
  s.last_seen_at,
  s.expires_at,
  s.user_agent,
  p.label
FROM user_sessions s
LEFT JOIN user_passkeys p ON p.id = s.passkey_id
WHERE s.user_id = sqlc.arg(user_id)
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
ORDER BY s.last_seen_at DESC;

-- name: CreateEnrollment :exec
INSERT INTO user_enrollments (token_hash, label, user_id, expires_at)
VALUES (
  sqlc.arg(token_hash),
  sqlc.arg(label),
  sqlc.narg(user_id),
  now() + make_interval(secs => sqlc.arg(lifetime_seconds)::double precision)
);

-- Claim an enrollment token, or find that there is nothing to claim.
--
-- **The check and the claim are one statement.** Reading the row and marking it
-- used separately leaves a window in which two phones both pass the read and
-- both enrol — and a single-use token that can be used twice is the entire
-- security property gone. The UPDATE ... RETURNING makes the database decide,
-- and exactly one caller gets a row.
--
-- Used, expired and never-existed all come back the same way: no row.
-- name: ClaimEnrollment :one
UPDATE user_enrollments
SET used_at = now()
WHERE token_hash = sqlc.arg(token_hash)
  AND used_at IS NULL
  AND expires_at > now()
RETURNING token_hash, label, user_id, created_at, expires_at, used_at;

-- Look at an enrollment without spending it.
--
-- The registration ceremony spans two requests and the token is claimed at the
-- start, so the finish step needs to read what it is completing. Reachable only
-- from a ceremony row, which is itself only reachable from the cookie handed
-- out at the start.
-- name: GetEnrollment :one
SELECT token_hash, label, user_id, created_at, expires_at, used_at
FROM user_enrollments
WHERE token_hash = sqlc.arg(token_hash);

-- Hand an unspent enrollment back, so a ceremony that failed is not a token
-- burned. Only ever called on the failure path of the same request that claimed
-- it.
-- name: ReleaseEnrollment :exec
UPDATE user_enrollments SET used_at = NULL WHERE token_hash = sqlc.arg(token_hash);

-- name: CreateCeremony :exec
INSERT INTO webauthn_ceremonies (id, purpose, session, enrollment, expires_at)
VALUES (
  sqlc.arg(id),
  sqlc.arg(purpose),
  sqlc.arg(session),
  sqlc.narg(enrollment),
  now() + make_interval(secs => sqlc.arg(lifetime_seconds)::double precision)
);

-- Take a ceremony, once.
--
-- DELETE ... RETURNING rather than SELECT-then-DELETE, for the reason
-- ClaimEnrollment is one statement: a challenge that can be answered twice is a
-- signature that can be replayed, and the only way two concurrent finishes
-- cannot both succeed is for the database to be the one deciding.
-- name: ConsumeCeremony :one
DELETE FROM webauthn_ceremonies
WHERE id = sqlc.arg(id) AND expires_at > now()
RETURNING id, purpose, session, enrollment, created_at, expires_at;

-- Drop what has gone stale.
--
-- Called opportunistically when a ceremony starts rather than from a scheduled
-- job. These rows are seconds-lived and arrive a handful of times a year; a
-- background job to tidy up after them would be more machinery than the thing
-- it maintains.
-- name: SweepExpiredAuth :exec
WITH c AS (DELETE FROM webauthn_ceremonies WHERE expires_at < now()),
     e AS (DELETE FROM user_enrollments WHERE expires_at < now() AND used_at IS NULL)
DELETE FROM user_sessions WHERE expires_at < now() - interval '30 days';

-- ---------------------------------------------------------------------------
-- Push subscriptions
-- ---------------------------------------------------------------------------

-- Save where a browser wants its notifications.
--
-- An upsert on the endpoint, because that is the browser's own identity for
-- this subscription and it hands back the same string when it re-subscribes.
-- The keys are updated too: a browser may rotate them without the endpoint
-- changing, and a stale p256dh encrypts a message nothing can open.
-- name: UpsertPushSubscription :exec
INSERT INTO push_subscriptions (endpoint, user_id, p256dh, auth, label)
VALUES (sqlc.arg(endpoint), sqlc.arg(user_id), sqlc.arg(p256dh), sqlc.arg(auth), sqlc.arg(label))
ON CONFLICT (endpoint) DO UPDATE SET
  user_id = EXCLUDED.user_id,
  p256dh  = EXCLUDED.p256dh,
  auth    = EXCLUDED.auth,
  label   = EXCLUDED.label;

-- Everyone who should hear about a booking.
--
-- Every subscription, not the ones belonging to some particular phone: the
-- console is one shared owner account and a notification about a booking is
-- for whoever is holding a handset.
-- name: ListPushSubscriptions :many
SELECT endpoint, user_id, p256dh, auth, label, created_at, last_sent_at
FROM push_subscriptions
ORDER BY created_at;

-- name: CountPushSubscriptions :one
SELECT count(*) FROM push_subscriptions;

-- name: DeletePushSubscription :execrows
DELETE FROM push_subscriptions WHERE endpoint = sqlc.arg(endpoint);

-- name: TouchPushSubscription :exec
UPDATE push_subscriptions SET last_sent_at = now() WHERE endpoint = sqlc.arg(endpoint);
