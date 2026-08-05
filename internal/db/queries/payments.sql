-- Recording a payment is the one write in this system that a hostile network can
-- ask for twice. Stripe's delivery is at-least-once, so every statement here is
-- written to be safe to run again rather than guarded by a flag somewhere else.

-- Record a movement of money, once.
--
-- DO NOTHING rather than DO UPDATE: a redelivered event carries the same
-- amounts, so there is nothing to update, and the empty result is the signal
-- the caller needs — this event has already been applied, stop here and do not
-- add to amount_paid_cents a second time.
--
-- The conflict target is (stripe_id, status), not stripe_id alone. A guest
-- whose card is declined retries on the same PaymentIntent, so one intent
-- legitimately produces a failed row and then a succeeded one; keying on the id
-- alone made the success collide with the failure and the payment was dropped
-- on the floor while the guest's card was charged.
-- name: RecordPayment :one
INSERT INTO payments (booking_id, stripe_id, kind, amount_cents, status)
VALUES (
  sqlc.arg(booking_id),
  sqlc.arg(stripe_id),
  sqlc.arg(kind),
  sqlc.arg(amount_cents),
  sqlc.arg(status)
)
ON CONFLICT (stripe_id, status) DO NOTHING
RETURNING id;

-- name: ListPaymentsForBooking :many
SELECT id, stripe_id, kind, amount_cents, status, created_at
FROM payments
WHERE booking_id = sqlc.arg(booking_id)
ORDER BY created_at, id;

-- Note this event as handled. Zero rows affected means it already was.
-- name: RecordStripeEvent :execrows
INSERT INTO stripe_events (id, type)
VALUES (sqlc.arg(id), sqlc.arg(type))
ON CONFLICT (id) DO NOTHING;

-- Add to the gross collected.
--
-- amount_paid_cents only ever grows. A refund is a row in payments, never a
-- subtraction here: pricing.Refund derives what to return from what was
-- collected, so reducing this column would make a second cancellation compute a
-- smaller refund off an already-reduced figure.
--
-- Separate from confirming the booking on purpose. Money can arrive for a stay
-- that cannot happen — the room was resold while the guest was paying — and the
-- ledger has to record that honestly without the status claiming the guest has
-- a room.
-- name: AddPaymentToBooking :exec
UPDATE bookings
SET amount_paid_cents = amount_paid_cents + sqlc.arg(amount_cents),
    updated_at        = now()
WHERE id = sqlc.arg(id);

-- name: ConfirmBooking :exec
UPDATE bookings
SET status     = 'confirmed',
    updated_at = now()
WHERE id = sqlc.arg(id);

-- How much of the room this booking still holds.
--
-- Asked after a promotion affects no rows, to tell the two reasons apart: a
-- stay that was already confirmed and is now taking its balance charge still
-- has its occupancy, while a hold the sweeper reclaimed has none — and only the
-- second one has to go and take the room back.
-- name: CountBookingOccupancy :one
SELECT count(*) FROM room_occupancy WHERE booking_id = sqlc.arg(booking_id);

-- Turn the checkout hold into the stay itself.
--
-- kind and expires_at have to move together: occupancy_holds_expire asserts
-- (kind = 'hold') = (expires_at IS NOT NULL), so setting either one alone would
-- violate it. Doing both in one statement is what makes the promotion legal
-- without ever dropping the row — the room is never, for any instant,
-- unoccupied and available to somebody else.
-- name: PromoteHoldToBooking :execrows
UPDATE room_occupancy
SET kind = 'booking', expires_at = NULL
WHERE booking_id = sqlc.arg(booking_id) AND kind = 'hold';

-- What the payment path needs to know about a booking. Deliberately narrower
-- than GetBookingByCode: no guest join, because nothing here emails anybody.
-- name: GetBookingForPayment :one
SELECT
  id,
  code,
  status,
  checkin,
  checkout,
  total_cents,
  deposit_cents,
  balance_due_cents,
  amount_paid_cents,
  balance_charge_at,
  payment_intent_id
FROM bookings
WHERE code = sqlc.arg(code);

-- Note that a PaymentIntent now exists for this booking, which is what stops
-- the sweeper expiring it out from under a guest who is mid-payment.
-- name: StartPayment :exec
UPDATE bookings
SET payment_intent_id  = sqlc.arg(payment_intent_id),
    payment_started_at = now(),
    updated_at         = now()
WHERE id = sqlc.arg(id);

-- The T-7 charge was declined. The stay stays confirmed — the guest is still
-- arriving — and the owner gets an unmissable flag instead.
-- name: MarkBalanceChargeFailed :exec
UPDATE bookings
SET balance_charge_failed_at = now(),
    updated_at               = now()
WHERE id = sqlc.arg(id);

-- name: CancelBooking :exec
UPDATE bookings
SET status     = 'cancelled',
    updated_at = now()
WHERE id = sqlc.arg(id);

-- Put the room back on sale. Used when a stay is cancelled, and on the path
-- where a payment succeeded for a room we could not re-claim.
-- name: ReleaseBookingOccupancy :execrows
DELETE FROM room_occupancy WHERE booking_id = sqlc.arg(booking_id);

-- Confirmed stays whose balance is due and not yet collected.
--
-- The amount_paid_cents < total_cents test is what makes the scan idempotent:
-- once the charge lands, the booking stops matching, so re-running the scan is
-- free and a missed day catches up by itself.
-- name: ListBookingsDueForBalanceCharge :many
SELECT id, code, total_cents, amount_paid_cents, balance_due_cents
FROM bookings
WHERE status = 'confirmed'
  AND balance_charge_at IS NOT NULL
  AND balance_charge_at <= sqlc.arg(on_date)
  AND amount_paid_cents < total_cents
  AND balance_charge_failed_at IS NULL
ORDER BY balance_charge_at, id;

-- Confirmed stays to warn the day before the charge (T-8).
--
-- A threshold and a marker rather than an equality on the charge date. Matching
-- the exact day meant a server that was down for it skipped the warning
-- permanently and charged the card at T-7 with no notice, which is the one
-- thing decision #6 asks this email to prevent. balance_warned_at IS NULL is
-- what stops the wider range warning the same guest every day instead.
--
-- Stays whose charge has already failed are left out: the balance_failed
-- message is the honest one to send at that point, not a warning about a charge
-- that has already been attempted.
-- name: ListBookingsToWarnAboutBalance :many
SELECT id, code, total_cents, amount_paid_cents, balance_due_cents
FROM bookings
WHERE status = 'confirmed'
  AND balance_charge_at IS NOT NULL
  AND balance_charge_at <= sqlc.arg(on_date)
  AND balance_warned_at IS NULL
  AND balance_charge_failed_at IS NULL
  AND amount_paid_cents < total_cents
ORDER BY balance_charge_at, id;

-- Note that the T-8 warning has gone out.
--
-- Belongs in the same transaction as queueing the email: the queue is a table,
-- so both writes commit together and a crash between them cannot either drop
-- the warning or send it twice.
-- name: MarkBalanceWarned :exec
UPDATE bookings
SET balance_warned_at = now(),
    updated_at        = now()
WHERE id = sqlc.arg(id);
