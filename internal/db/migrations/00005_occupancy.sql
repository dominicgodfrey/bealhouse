-- +goose Up

-- Everything that occupies a room lives in this one table: a confirmed booking,
-- a checkout hold, or an owner's manual block. Keeping them together is what
-- lets a single constraint make overlapping occupancy impossible rather than
-- merely unlikely.
CREATE TABLE room_occupancy (
  id      bigserial PRIMARY KEY,
  room_id bigint NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,

  -- Half-open [check-in, check-out). A guest leaving Jun 13 and one arriving
  -- Jun 13 do not overlap, because [10,13) and [13,15) do not intersect.
  -- Getting this wrong costs a sellable night at every changeover. Postgres
  -- canonicalises daterange to '[)' automatically, so this is the stored form
  -- regardless of how a caller writes the literal.
  during daterange NOT NULL,

  kind   text NOT NULL CHECK (kind IN ('booking', 'hold', 'block')),

  -- Direct today. Airbnb and Booking.com become additional values here rather
  -- than a second availability system.
  source text NOT NULL DEFAULT 'direct',

  -- The foreign key to bookings is added with that table; the column exists now
  -- so the exclusion constraint is never rebuilt on a populated table.
  booking_id bigint,

  -- Holds only. The sweeper reclaims these; everything else is permanent until
  -- explicitly removed.
  expires_at timestamptz,

  -- Blocks only: why the owner took the room off sale.
  reason text NOT NULL DEFAULT '',

  created_at timestamptz NOT NULL DEFAULT now(),

  -- A zero-night stay would produce an empty range, which overlaps nothing and
  -- would slip past the exclusion constraint entirely.
  CONSTRAINT occupancy_span_not_empty CHECK (NOT isempty(during)),

  -- A hold without an expiry never gets swept and silently removes a room from
  -- sale forever. A booking with one would evaporate.
  CONSTRAINT occupancy_holds_expire CHECK ((kind = 'hold') = (expires_at IS NOT NULL)),

  -- The whole design in one line. Two transactions racing for the last room
  -- cannot both commit: one wins, the other gets 23P01 and the API turns that
  -- into "just taken, please pick again". No application locking, no
  -- read-then-write race, and Postgres is the only referee.
  EXCLUDE USING gist (room_id WITH =, during WITH &&)
);

-- The exclusion constraint's GiST index also serves availability lookups, so no
-- separate index on (room_id, during) is needed.

-- Sweeping expired holds scans by expiry, not by room.
CREATE INDEX room_occupancy_hold_expiry_idx
  ON room_occupancy (expires_at)
  WHERE kind = 'hold';

-- +goose Down
DROP TABLE room_occupancy;
