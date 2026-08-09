-- +goose Up

-- When the guest accepted the policies.
--
-- The tick-box on the confirm step is a UI gate; this is the record.
--
-- Nullable, and it has to be: the deploy runs migrations with the NEW binary
-- before installing it, so the old one serves guests against this schema for a
-- second or two and would insert without this column. Additive now, and it can
-- be made NOT NULL in a later release once every row has one — which is the
-- rule the whole deploy depends on.
--
-- The TIME is the server's, never the browser's. `now()` inside the inserting
-- transaction, so it agrees with created_at and cannot be back-dated by a
-- client that fancies it. What the browser sends is a boolean saying the box
-- was ticked, and booking.Create refuses without it; that refusal is the
-- enforcement, and this column is the evidence.
--
-- It records WHEN, not WHAT. If the policy text ever has to be defensible
-- version-by-version, that is a second column holding a hash of the terms as
-- rendered, and a real decision about versioning rather than something to
-- smuggle in here.
ALTER TABLE bookings
  ADD COLUMN policies_accepted_at timestamptz;

COMMENT ON COLUMN bookings.policies_accepted_at IS
  'When the guest ticked the policies box, stamped server-side. NULL for rows written before the box existed.';

-- +goose Down
ALTER TABLE bookings DROP COLUMN policies_accepted_at;
