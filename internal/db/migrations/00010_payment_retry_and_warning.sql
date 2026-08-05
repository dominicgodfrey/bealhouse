-- +goose Up

-- A Stripe id is unique per *attempt outcome*, not per object.
--
-- The original index was unique on stripe_id alone, which assumed one row per
-- Stripe object. The Payment Element breaks that assumption on the most
-- ordinary failure there is: a declined card leaves the PaymentIntent in
-- requires_payment_method and the guest retries **on the same intent**, so the
-- succeeded event carries the id a failed row already holds. The insert
-- conflicted, the handler read that as "already applied", and the guest was
-- charged for a booking that stayed pending until the sweeper resold the room.
--
-- (stripe_id, status) is what was actually meant: one failure and one success
-- per intent are two different facts about two different attempts, and both are
-- worth keeping. Idempotency is unharmed — a redelivered succeeded event still
-- carries the same pair and still inserts nothing.
DROP INDEX payments_stripe_id_idx;

CREATE UNIQUE INDEX payments_stripe_id_status_idx ON payments (stripe_id, status);

COMMENT ON INDEX payments_stripe_id_status_idx IS
  'Idempotency anchor for the webhook path. Keyed on the outcome as well as the id because a declined-then-retried PaymentIntent legitimately produces one failed row and one succeeded row under the same stripe_id.';

ALTER TABLE bookings
  -- When the T-8 "you will be charged tomorrow" email was queued.
  --
  -- The scan that finds these used to match balance_charge_at on the exact day,
  -- which silently skipped the warning entirely if the server was down for it —
  -- and the whole purpose of the warning is that the T-7 charge is not a
  -- surprise (decision #6). A NULL marker plus a range makes the scan catch up
  -- the way the charge scan already does, without warning twice.
  ADD COLUMN balance_warned_at timestamptz;

-- What the T-8 scan reads: confirmed stays with a scheduled charge not yet
-- warned about.
CREATE INDEX bookings_balance_warn_idx
  ON bookings (balance_charge_at)
  WHERE status = 'confirmed' AND balance_charge_at IS NOT NULL AND balance_warned_at IS NULL;

-- +goose Down
--
-- Note that this down migration fails, correctly, on any database where a guest
-- has retried a declined card: the narrower index cannot be built over a
-- stripe_id that now legitimately holds both a failed and a succeeded row.
-- There is no automatic way back that does not throw away a payment record, and
-- silently deleting one to make an index fit would be the worse bug. Rolling
-- back this far means deciding by hand which rows to keep.
DROP INDEX bookings_balance_warn_idx;
ALTER TABLE bookings DROP COLUMN balance_warned_at;
DROP INDEX payments_stripe_id_status_idx;
CREATE UNIQUE INDEX payments_stripe_id_idx ON payments (stripe_id);
