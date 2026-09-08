-- +goose Up

-- The pet room and its fee are withdrawn (decision #23, revised 2026-09-07):
-- the inn no longer takes pets, so there is nothing for the flag to mark or the
-- fee to charge. Dropped rather than left dormant, because a column nobody
-- sets is a search filter nobody can pass and a quote line that never appears,
-- and both would still have to be carried by every query and screen.
--
-- Not additive, and deliberately so. The deploy runs the new binary's
-- migrations before installing it, so the old binary briefly serves a schema
-- it does not expect — which is why migrations here are normally add-now,
-- require-later. This one runs before the first deploy, so there is no old
-- binary to protect; on a live system it would have to be two releases.

-- A booking that did pay a fee keeps its total. The fee was taxed with the
-- room and collected with it, so folding it into the room subtotal is the
-- honest reading of what the guest paid, and it is what lets the reconciliation
-- constraint below hold for every row rather than only new ones.
UPDATE bookings
SET room_subtotal_cents = room_subtotal_cents + pet_fee_cents
WHERE pet_fee_cents > 0;

ALTER TABLE bookings
  DROP CONSTRAINT booking_total_reconciles,
  DROP CONSTRAINT booking_pet_fee_requires_a_pet,
  DROP COLUMN with_pet,
  DROP COLUMN pet_fee_cents,
  ADD CONSTRAINT booking_total_reconciles
    CHECK (total_cents = room_subtotal_cents + tax_cents);

ALTER TABLE rooms
  DROP CONSTRAINT pet_fee_requires_pet_friendly,
  DROP COLUMN is_pet_friendly,
  DROP COLUMN pet_fee_cents;

-- +goose Down

ALTER TABLE rooms
  ADD COLUMN is_pet_friendly boolean NOT NULL DEFAULT false,
  ADD COLUMN pet_fee_cents   integer NOT NULL DEFAULT 0 CHECK (pet_fee_cents >= 0),
  ADD CONSTRAINT pet_fee_requires_pet_friendly
    CHECK (is_pet_friendly OR pet_fee_cents = 0);

ALTER TABLE bookings
  DROP CONSTRAINT booking_total_reconciles,
  ADD COLUMN with_pet      boolean NOT NULL DEFAULT false,
  ADD COLUMN pet_fee_cents bigint  NOT NULL DEFAULT 0 CHECK (pet_fee_cents >= 0),
  ADD CONSTRAINT booking_total_reconciles
    CHECK (total_cents = room_subtotal_cents + pet_fee_cents + tax_cents),
  ADD CONSTRAINT booking_pet_fee_requires_a_pet
    CHECK (with_pet OR pet_fee_cents = 0);
