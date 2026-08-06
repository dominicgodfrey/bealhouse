-- The tax rate crosses this boundary pre-scaled to hundred-thousandths in both
-- directions, matching pricing.Rate, so no numeric is ever decoded into a
-- float64. The division below is numeric arithmetic, not floating point.

-- A returning guest is matched on email, case-insensitively, and their name and
-- phone are refreshed from the booking they just made — that is the most recent
-- thing they have told us.
-- name: UpsertGuest :one
INSERT INTO guests (email, name, phone)
VALUES (sqlc.arg(email), sqlc.arg(name), sqlc.arg(phone))
ON CONFLICT (lower(email)) DO UPDATE SET
  email      = EXCLUDED.email,
  name       = EXCLUDED.name,
  phone      = EXCLUDED.phone,
  updated_at = now()
RETURNING id;

-- name: CreateBooking :one
INSERT INTO bookings (
  code, guest_id, status, checkin, checkout, guests, with_pet,
  room_subtotal_cents, pet_fee_cents, tax_cents, tax_rate_snapshot,
  total_cents, deposit_cents, balance_due_cents, balance_charge_at
)
VALUES (
  sqlc.arg(code),
  sqlc.arg(guest_id),
  sqlc.arg(status),
  sqlc.arg(checkin),
  sqlc.arg(checkout),
  sqlc.arg(guests),
  sqlc.arg(with_pet),
  sqlc.arg(room_subtotal_cents),
  sqlc.arg(pet_fee_cents),
  sqlc.arg(tax_cents),
  sqlc.arg(tax_rate_scaled)::bigint::numeric / 100000,
  sqlc.arg(total_cents),
  sqlc.arg(deposit_cents),
  sqlc.arg(balance_due_cents),
  sqlc.narg(balance_charge_at)
)
RETURNING id;

-- name: CreateBookingRoom :exec
INSERT INTO booking_rooms (booking_id, room_id, checkin, checkout, nightly_prices)
VALUES (
  sqlc.arg(booking_id),
  sqlc.arg(room_id),
  sqlc.arg(checkin),
  sqlc.arg(checkout),
  sqlc.arg(nightly_prices)
);

-- One booking as the confirm page needs it.
--
-- hold_expires_at comes from the occupancy row rather than being copied onto
-- the booking: the hold is what actually reserves the room, so it is the only
-- honest source for the countdown. A pending booking with no hold row is one
-- the sweeper has already reclaimed, and the NULL says so.
-- name: GetBookingByCode :one
SELECT
  b.id,
  b.code,
  b.status,
  b.checkin,
  b.checkout,
  b.guests,
  b.with_pet,
  b.room_subtotal_cents,
  b.pet_fee_cents,
  b.tax_cents,
  (b.tax_rate_snapshot * 100000)::bigint AS tax_rate_scaled,
  b.total_cents,
  b.deposit_cents,
  b.balance_due_cents,
  b.amount_paid_cents,
  b.balance_charge_at,
  b.created_at,
  g.email AS guest_email,
  g.name  AS guest_name,
  g.phone AS guest_phone,
  hold.expires_at AS hold_expires_at
FROM bookings b
JOIN guests g ON g.id = b.guest_id
-- Lateral rather than a scalar subquery so the NULL survives into Go as a
-- pointer. "No hold" is the answer the confirm page needs most.
-- The latest expiry rather than an aggregate, because a multi-room booking's
-- holds all share one TTL and a plain column keeps the type honest.
LEFT JOIN LATERAL (
  SELECT o.expires_at
  FROM room_occupancy o
  WHERE o.booking_id = b.id AND o.kind = 'hold'
  ORDER BY o.expires_at DESC
  LIMIT 1
) hold ON true
WHERE b.code = sqlc.arg(code);

-- name: ListBookingRooms :many
SELECT
  br.room_id,
  br.checkin,
  br.checkout,
  br.nightly_prices,
  r.slug,
  r.name,
  r.view
FROM booking_rooms br
JOIN rooms r ON r.id = br.room_id
WHERE br.booking_id = sqlc.arg(booking_id)
ORDER BY r.sort_order;

-- Expire pending bookings whose hold is gone. Run after the sweeper: the hold
-- is what reserved the room, so once it has lapsed the booking is dead and
-- should say so rather than sit pending forever.
--
-- ...unless a payment is in flight. A guest working through a 3-D Secure
-- challenge can outlive the fifteen-minute hold, and marking their booking
-- expired seconds before the webhook confirms it would tell them the room was
-- gone while their money was on its way. The grace period is settings-driven
-- and starts when the PaymentIntent is created.
--
-- This does not keep the room off sale: the hold has already been swept on its
-- own TTL, so a guest who pays after the grace period still finds their booking
-- expired and gets it resolved by the re-claim path in internal/payments — with
-- a refund if the room really did go to somebody else.
-- name: ExpireAbandonedBookings :execrows
UPDATE bookings b
SET status = 'expired', updated_at = now()
FROM settings s
WHERE b.status = 'pending'
  AND NOT EXISTS (
    SELECT 1 FROM room_occupancy o
    WHERE o.booking_id = b.id AND o.kind = 'hold'
  )
  AND (
    b.payment_started_at IS NULL
    OR b.payment_started_at <= now() - (s.payment_grace_minutes * interval '1 minute')
  );

-- Confirmed stays leaving on a given day that have not had their departure
-- email yet.
--
-- **Equality on the date, not a threshold.** The T-8 balance warning uses
-- `<=` so a server that was switched off catches up and sends it late, because
-- a late warning still does its job. This message does not survive that: it
-- says the guest is leaving today, and a copy that arrives three days after
-- they got home is a lie the inn told them. A day the server spent entirely off
-- is a departure note that does not go out, which is the honest outcome.
--
-- The room names are not joined in. The scan wants a row per booking and the
-- message wants a list per booking, and one query cannot be both without
-- either an aggregate here or duplicate bookings to fold up in Go — so the
-- rooms are loaded per guest, in the same transaction that queues the mail.
-- name: ListBookingsDueForCheckoutEmail :many
SELECT
  b.id,
  b.code,
  b.checkin,
  b.checkout,
  g.email AS guest_email,
  g.name  AS guest_name
FROM bookings b
JOIN guests g ON g.id = b.guest_id
WHERE b.status = 'confirmed'
  AND b.checkout = sqlc.arg(on_date)
  AND b.checkout_email_sent_at IS NULL
ORDER BY b.id;

-- Note that the departure email has gone out.
--
-- Belongs in the same transaction as queueing it. The queue is a table, so both
-- writes commit together and a crash between them can neither drop the message
-- nor send it every fifteen minutes until midnight.
-- name: MarkCheckoutEmailSent :exec
UPDATE bookings
SET checkout_email_sent_at = now(),
    updated_at             = now()
WHERE id = sqlc.arg(id);
