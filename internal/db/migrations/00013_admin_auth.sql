-- +goose Up

-- Who can open the admin console (decision #15).
--
-- **One shared account, and a real table behind it.** The inn is run by two
-- people who both act as "the owner", and there is no meaningful permission
-- boundary to draw between them — so the console has one identity rather than a
-- role system nobody would ever configure. The table is here anyway because the
-- day a cleaner or a bookkeeper needs their own login, adding a row is the whole
-- change.
--
-- **There is no password column, and that is the point.** Sign-in is WebAuthn:
-- each phone holds a private key it will only use after its own biometric
-- check, and the server holds the matching public key. Nothing here is a secret
-- that can be shared, phished, reused on another site, or leaked in a dump —
-- the strongest thing this table gives an attacker who reads it is the name of
-- an inn they already knew about.
CREATE TABLE users (
  id bigserial PRIMARY KEY,

  -- The WebAuthn user handle: an opaque random identifier the authenticator
  -- stores alongside the key and hands back at sign-in.
  --
  -- Random rather than the row id, because it is written into every phone that
  -- ever enrols and the spec is explicit that it must carry no personal meaning.
  -- 64 bytes is the maximum the spec allows and what it recommends using.
  handle bytea NOT NULL UNIQUE,

  -- Display only. It is what the phone's passkey prompt shows.
  name text NOT NULL,

  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT user_handle_is_long_enough CHECK (octet_length(handle) BETWEEN 16 AND 64),
  CONSTRAINT user_name_present          CHECK (btrim(name, E' \t\r\n') <> '')
);

COMMENT ON TABLE users IS
  'Admin console accounts (decision #15). One shared owner account today; the table exists so a second is a row rather than a rewrite. No passwords anywhere — sign-in is WebAuthn.';

-- One row per enrolled phone.
--
-- The whole `webauthn.Credential` is kept as JSON rather than shredded into
-- columns. The library defines that struct, versions it, and needs every field
-- back to validate a sign-in — including ones this schema would have to learn
-- about on each upgrade. The two things the application looks *up* by are
-- columns; everything the protocol needs stays in one blob that round-trips
-- exactly as the library wrote it.
--
-- The signature counter lives in there too, and is written back on every
-- successful sign-in: an authenticator that replays an old count is a cloned
-- one, and the library can only notice if the number it is handed is the number
-- it last stored.
CREATE TABLE user_passkeys (
  -- The credential id the authenticator chose. Opaque, unique per key, and the
  -- value the browser sends at sign-in, so it is the natural primary key.
  id bytea PRIMARY KEY,

  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  -- "Sol's iPhone". What the owner reads when deciding which one to revoke,
  -- which is the only moment this list is ever looked at.
  label text NOT NULL,

  credential jsonb NOT NULL,

  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,

  CONSTRAINT passkey_id_present    CHECK (octet_length(id) > 0),
  CONSTRAINT passkey_label_present CHECK (btrim(label, E' \t\r\n') <> '')
);

CREATE INDEX user_passkeys_user_id_idx ON user_passkeys (user_id);

-- A signed-in phone.
--
-- **The token is stored hashed, never in the clear.** The cookie holds 32
-- random bytes; this holds their SHA-256. Anyone who reads this table therefore
-- learns that two sessions exist and nothing that lets them use either — the
-- same reason a password would be hashed, applied to the thing that is actually
-- a live credential once you are past the front door.
--
-- SHA-256 and not a slow hash on purpose. This value is 32 bytes of
-- cryptographic randomness rather than something a person chose, so there is no
-- dictionary to run against it and no work factor worth paying on every single
-- request.
--
-- Rows rather than a signed stateless token, because the requirement is that a
-- phone stays signed in indefinitely, and "indefinitely" is only safe next to a
-- list somebody can strike a line through. A JWT cannot be revoked; this can.
CREATE TABLE user_sessions (
  token_hash bytea PRIMARY KEY,

  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  -- Which phone this session was opened from. Nullable because revoking the
  -- passkey should not take the audit trail with it; ON DELETE SET NULL keeps
  -- the row readable while cutting the link.
  passkey_id bytea REFERENCES user_passkeys (id) ON DELETE SET NULL,

  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,

  -- Recorded so the "signed-in devices" list can say which row is which phone.
  -- Truncated by the application; it is a display string and never a decision.
  user_agent text NOT NULL DEFAULT '',

  CONSTRAINT session_token_hash_is_sha256 CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX user_sessions_user_id_idx ON user_sessions (user_id);

-- What lets a new phone enrol.
--
-- Enrolling a passkey creates a permanent way in, so the thing that authorises
-- it has to be **single use** — which is why this is a table and not another
-- HMAC like the guest's manage link. A signed link stays valid until it
-- expires, and a link that is still good after the phone has used it is one
-- forwarded message away from being someone else's admin console.
--
-- The token is hashed here for the same reason a session's is: reading this
-- table must not be enough to enrol.
--
-- Minted by `bealhouse enroll` on the server, which needs shell access to the
-- box, or from an already-signed-in console for the second phone.
CREATE TABLE user_enrollments (
  token_hash bytea PRIMARY KEY,

  -- The label the resulting passkey is created with, chosen when the token is
  -- minted rather than typed on the phone: the person at the keyboard knows
  -- which handset they are about to hand it to.
  label text NOT NULL,

  -- Which account the new phone joins. NULL means the shared owner account,
  -- created on first use — so the very first enrollment needs nothing to exist
  -- beforehand.
  user_id bigint REFERENCES users (id) ON DELETE CASCADE,

  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,

  CONSTRAINT enrollment_token_hash_is_sha256 CHECK (octet_length(token_hash) = 32),
  CONSTRAINT enrollment_label_present        CHECK (btrim(label, E' \t\r\n') <> '')
);

-- A WebAuthn ceremony in progress.
--
-- Both halves of a sign-in are two HTTP requests: the server issues a challenge,
-- the phone signs it. The challenge has to be remembered in between, and it has
-- to be **usable exactly once** — a replayed assertion against a challenge that
-- is still on file is a valid signature the second time as well. Deleting the
-- row when it is consumed is what makes that impossible, and is the reason this
-- is a table rather than a signed cookie carrying its own challenge.
--
-- The id is random and travels in a short-lived cookie, so the browser can find
-- its own ceremony without being able to name anybody else's.
CREATE TABLE webauthn_ceremonies (
  id bytea PRIMARY KEY,

  purpose text NOT NULL CHECK (purpose IN ('register', 'login')),

  -- webauthn.SessionData: the challenge and what was asked of the authenticator.
  session jsonb NOT NULL,

  -- For a registration, the enrollment being spent. Carried here rather than
  -- re-sent by the browser at the finish step, so the token cannot be swapped
  -- for a different one between the two halves.
  enrollment bytea REFERENCES user_enrollments (token_hash) ON DELETE CASCADE,

  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,

  CONSTRAINT ceremony_id_is_long_enough CHECK (octet_length(id) = 32),

  -- A login ceremony has no enrollment and a registration always does. Stated as
  -- a constraint because the alternative — a registration that reached the
  -- finish step with nothing authorising it — is precisely the bug that would
  -- let anyone enrol a passkey.
  CONSTRAINT ceremony_registration_is_authorised
    CHECK ((purpose = 'register') = (enrollment IS NOT NULL))
);

-- +goose Down
DROP TABLE webauthn_ceremonies;
DROP TABLE user_enrollments;
DROP TABLE user_sessions;
DROP TABLE user_passkeys;
DROP TABLE users;
