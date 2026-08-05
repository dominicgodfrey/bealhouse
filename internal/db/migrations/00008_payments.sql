-- +goose Up

-- Every movement of money, one row each. This is the ledger the booking's own
-- columns deliberately are not: bookings.deposit_cents and balance_due_cents
-- describe how the quote splits and never change again, so what has actually
-- been collected has to be recorded somewhere that grows.
--
-- stripe_id holds the id of the Stripe object this row records — a PaymentIntent
-- (pi_...) for a charge, a Refund (re_...) for a refund. Both are globally
-- unique at Stripe, so one unique index over the column is the idempotency
-- anchor for the whole webhook path: a redelivered event inserts zero rows and
-- the handler knows to stop. Getting this wrong charges a guest twice.
CREATE TABLE payments (
  id         bigserial PRIMARY KEY,
  booking_id bigint NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,

  stripe_id text NOT NULL,

  --   deposit  the 50% taken at booking (decision #8)
  --   full     a short-notice stay charged in one go (decision #7)
  --   balance  the off-session T-7 charge (decision #6)
  --   refund   money returned, on the terms in decision #9
  kind text NOT NULL CHECK (kind IN ('deposit', 'full', 'balance', 'refund')),

  -- Always positive. Direction is carried by kind, not by the sign, so a query
  -- that forgets to filter cannot silently net a refund against a charge.
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),

  status text NOT NULL CHECK (status IN ('succeeded', 'failed')),

  created_at timestamptz NOT NULL DEFAULT now()
);

-- The idempotency anchor. Stripe redelivers events on any non-2xx and on
-- timeouts, and delivery is at-least-once by design.
CREATE UNIQUE INDEX payments_stripe_id_idx ON payments (stripe_id);

CREATE INDEX payments_booking_id_idx ON payments (booking_id);

COMMENT ON TABLE payments IS
  'The ledger. bookings.amount_paid_cents is the gross of the succeeded charges here and is never reduced by a refund - pricing.Refund derives from what was collected, so decrementing it would make a second cancellation compute a different answer.';

-- Webhook events already handled, by Stripe's event id.
--
-- The unique index on payments.stripe_id already stops a redelivered charge
-- from being recorded twice, but not every event type writes a payment row, and
-- one that does not would otherwise be reprocessed on every redelivery. An
-- INSERT ... ON CONFLICT DO NOTHING that affects zero rows is the whole check.
CREATE TABLE stripe_events (
  id          text PRIMARY KEY,
  type        text NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now()
);

-- The durable job table (ARCHITECTURE.md, "The job runner"). Polled with
-- FOR UPDATE SKIP LOCKED, so several servers could run one of these without
-- coordinating, and a crash mid-job loses nothing.
CREATE TABLE jobs (
  id   bigserial PRIMARY KEY,
  kind text NOT NULL,

  -- When this becomes runnable. Also the lease: a claimed job has run_at pushed
  -- into the future, so a runner that dies mid-job leaves work that comes back
  -- on its own rather than a row locked forever.
  run_at timestamptz NOT NULL DEFAULT now(),

  payload jsonb NOT NULL DEFAULT '{}',

  attempts   integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text NOT NULL DEFAULT '',

  -- Enqueueing the same work twice is the ordinary case, not the exception:
  -- the T-7 scan runs every tick and will keep finding the same booking until
  -- the charge lands. A stable key per unit of work makes enqueue idempotent,
  -- so the scan can stay a dumb repeated query.
  unique_key text,

  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX jobs_unique_key_idx ON jobs (unique_key) WHERE unique_key IS NOT NULL;

-- The claim query orders by run_at.
CREATE INDEX jobs_run_at_idx ON jobs (run_at);

ALTER TABLE bookings
  -- Set when a PaymentIntent is created for this booking, so the sweeper can
  -- tell a guest who is mid-checkout from one who closed the tab. Empty rather
  -- than NULL, matching the other two stripe columns.
  ADD COLUMN payment_intent_id text NOT NULL DEFAULT '',

  -- When that intent was created. The sweeper leaves a booking alone while this
  -- is recent (see settings.payment_grace_minutes): expiring a booking whose
  -- 3-D Secure challenge is still on screen tells the guest their room is gone
  -- moments before the payment succeeds.
  --
  -- This does not hold the room. The hold is swept on its own TTL either way,
  -- so the grace period only delays the booking's bookkeeping, never a room's
  -- return to sale.
  ADD COLUMN payment_started_at timestamptz,

  -- The T-7 charge was declined (decision #6's failure branch). Not a status:
  -- the stay is still confirmed and the guest is still arriving, there is just
  -- money outstanding that the owner has to chase. Admin flags this in red.
  ADD COLUMN balance_charge_failed_at timestamptz;

-- What the T-7 scan reads: confirmed stays that still have a scheduled charge.
CREATE INDEX bookings_balance_charge_at_idx
  ON bookings (balance_charge_at)
  WHERE status = 'confirmed' AND balance_charge_at IS NOT NULL;

ALTER TABLE settings
  ADD COLUMN payment_grace_minutes integer NOT NULL DEFAULT 30
    CHECK (payment_grace_minutes > 0);

COMMENT ON COLUMN settings.payment_grace_minutes IS
  'How long after a PaymentIntent is created the sweeper leaves a pending booking alone. Longer than hold_ttl_minutes on purpose: a 3-D Secure challenge can outlive the hold.';

-- +goose Down
ALTER TABLE settings DROP COLUMN payment_grace_minutes;
DROP INDEX bookings_balance_charge_at_idx;
ALTER TABLE bookings
  DROP COLUMN payment_intent_id,
  DROP COLUMN payment_started_at,
  DROP COLUMN balance_charge_failed_at;
DROP TABLE jobs;
DROP TABLE stripe_events;
DROP TABLE payments;
