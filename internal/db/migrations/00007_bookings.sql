-- +goose Up

-- Guests are identified by email, which is how a returning guest is recognised
-- without an account (decision #11 puts guest logins out of scope). Case is
-- preserved for display but ignored for identity: nobody thinks
-- Ann@example.com is a different person from ann@example.com.
CREATE TABLE guests (
  id    bigserial PRIMARY KEY,
  email text NOT NULL,
  name  text NOT NULL,
  phone text NOT NULL DEFAULT '',

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT guest_email_looks_like_one CHECK (email LIKE '%_@_%')
);

CREATE UNIQUE INDEX guests_email_idx ON guests (lower(email));

-- One stay. Money is integer cents throughout; the only numeric is the tax rate
-- snapshot, which mirrors settings.tax_rate exactly and is scaled on the way out
-- so Go never decodes it into a float.
CREATE TABLE bookings (
  id       bigserial PRIMARY KEY,

  -- What the guest quotes on the phone. Generated in Go from an unambiguous
  -- alphabet; the uniqueness that matters is enforced here.
  code     text NOT NULL UNIQUE,

  guest_id bigint NOT NULL REFERENCES guests(id),

  --   pending    held, not yet paid — the hold row is what reserves the room
  --   confirmed  paid (step 4 promotes it from the Stripe webhook)
  --   cancelled  cancelled by guest or owner, refund settled separately
  --   expired    the hold lapsed before payment
  status text NOT NULL CHECK (status IN ('pending', 'confirmed', 'cancelled', 'expired')),

  -- Civil dates, never timestamps: a night at the inn is a night at the inn.
  -- Half-open like room_occupancy — checkout is not a night.
  checkin  date NOT NULL,
  checkout date NOT NULL,

  -- Capacity filter only, never a price input (decision #4).
  guests   integer NOT NULL CHECK (guests > 0),
  with_pet boolean NOT NULL DEFAULT false,

  -- The quote, snapshotted. Every one of these is what was true at the moment
  -- of booking, so a later rate or tax edit cannot change what a guest owes.
  --
  -- These are a snapshot, not a running ledger: deposit_cents and
  -- balance_due_cents describe how the quote splits and never change again.
  -- What is still collectable is derived from amount_paid_cents and status, so
  -- cancelling or refunding a booking never has to rewrite the split — which is
  -- what lets the reconciliation constraints below hold for the row's whole
  -- life rather than only at insert.
  room_subtotal_cents bigint NOT NULL CHECK (room_subtotal_cents >= 0),
  pet_fee_cents       bigint NOT NULL DEFAULT 0 CHECK (pet_fee_cents >= 0),
  tax_cents           bigint NOT NULL CHECK (tax_cents >= 0),
  tax_rate_snapshot   numeric(6, 5) NOT NULL CHECK (tax_rate_snapshot >= 0 AND tax_rate_snapshot < 1),
  total_cents         bigint NOT NULL CHECK (total_cents >= 0),
  deposit_cents       bigint NOT NULL CHECK (deposit_cents >= 0),
  balance_due_cents   bigint NOT NULL CHECK (balance_due_cents >= 0),
  amount_paid_cents   bigint NOT NULL DEFAULT 0 CHECK (amount_paid_cents >= 0),

  -- T-7 (decision #6). NULL for short-notice arrivals, which are charged in
  -- full at booking and never need the job (decision #7) — so a NULL here is
  -- the flag that says "no scheduled charge exists for this booking", not a
  -- missing value.
  balance_charge_at date,

  -- Populated in step 4. Empty rather than NULL so every read is total.
  stripe_customer_id       text NOT NULL DEFAULT '',
  stripe_payment_method_id text NOT NULL DEFAULT '',

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT booking_checkout_after_checkin CHECK (checkout > checkin),

  -- The two ways the money can silently stop adding up, both closed here rather
  -- than trusted to the handler that happens to write the row.
  CONSTRAINT booking_total_reconciles
    CHECK (total_cents = room_subtotal_cents + pet_fee_cents + tax_cents),
  CONSTRAINT booking_deposit_and_balance_reconcile
    CHECK (deposit_cents + balance_due_cents = total_cents),

  -- A pet fee on a booking that did not bring a pet is unchargeable, the same
  -- way a fee on a room that does not take pets is (see rooms).
  CONSTRAINT booking_pet_fee_requires_a_pet
    CHECK (with_pet OR pet_fee_cents = 0)
);

CREATE INDEX bookings_guest_id_idx ON bookings (guest_id);
CREATE INDEX bookings_checkin_idx  ON bookings (checkin);

-- Which rooms a booking occupies. Present from day one even though v1 sells one
-- room at a time (decision #10), so multi-room is a UI change rather than a
-- migration of live bookings.
CREATE TABLE booking_rooms (
  id         bigserial PRIMARY KEY,
  booking_id bigint NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  room_id    bigint NOT NULL REFERENCES rooms(id),

  -- Per-room dates, because a multi-room booking may not have every room for
  -- the whole stay.
  checkin  date NOT NULL,
  checkout date NOT NULL,

  -- The prices actually charged, one per night, in order. This is the snapshot
  -- that makes rates.rebuild safe: regenerating rate_calendar cannot reach in
  -- here.
  nightly_prices jsonb NOT NULL,

  UNIQUE (booking_id, room_id),

  CONSTRAINT booking_room_checkout_after_checkin CHECK (checkout > checkin),

  -- A snapshot with the wrong number of nights is a mispriced stay, and it
  -- would be found by an accountant rather than by a test.
  CONSTRAINT booking_room_prices_are_one_per_night
    CHECK (jsonb_typeof(nightly_prices) = 'array'
           AND jsonb_array_length(nightly_prices) = checkout - checkin)
);

CREATE INDEX booking_rooms_room_id_idx ON booking_rooms (room_id);

-- The column has been on room_occupancy since 00005 precisely so this could be
-- added without rebuilding the exclusion constraint on a populated table.
--
-- ON DELETE CASCADE means deleting a booking releases its room. That is the
-- right direction: occupancy exists to serve the booking, never the reverse.
--
-- Not constrained: "every 'booking' or 'hold' row must have a booking_id".
-- It is true of everything this application writes, but occupancy is tested as
-- a property of Postgres in isolation from bookings, and coupling the two would
-- buy an invariant the foreign key already half-covers at the cost of making
-- the concurrency tests construct a whole booking to claim a room.
ALTER TABLE room_occupancy
  ADD CONSTRAINT room_occupancy_booking_id_fkey
  FOREIGN KEY (booking_id) REFERENCES bookings(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE room_occupancy DROP CONSTRAINT room_occupancy_booking_id_fkey;
DROP TABLE booking_rooms;
DROP TABLE bookings;
DROP TABLE guests;
