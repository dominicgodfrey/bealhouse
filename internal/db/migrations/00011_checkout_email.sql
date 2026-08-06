-- +goose Up

ALTER TABLE bookings
  -- When the departure-morning email was queued.
  --
  -- The marker, not the schedule. The scan that sends this matches on the
  -- checkout date, so what stops a guest hearing from the inn every fifteen
  -- minutes all day is this column being set in the same transaction that
  -- queues the message — the same discipline balance_warned_at lives under.
  ADD COLUMN checkout_email_sent_at timestamptz;

COMMENT ON COLUMN bookings.checkout_email_sent_at IS
  'Set in the transaction that queues the departure-morning email. NULL means it has not gone out; the scan reads nothing else to decide.';

-- What the checkout scan reads: confirmed stays leaving on a given day that
-- have not been written to yet.
--
-- Partial, like the balance-warning index, because the rows that matter are a
-- vanishing fraction of the table and the ones already sent never need looking
-- at again.
CREATE INDEX bookings_checkout_email_idx
  ON bookings (checkout)
  WHERE status = 'confirmed' AND checkout_email_sent_at IS NULL;

-- +goose Down
DROP INDEX bookings_checkout_email_idx;
ALTER TABLE bookings DROP COLUMN checkout_email_sent_at;
