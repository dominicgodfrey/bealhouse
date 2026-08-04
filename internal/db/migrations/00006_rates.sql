-- +goose Up

-- Owner-facing pricing rules. The owner edits seasons and a room x season price
-- grid; they never touch rate_calendar, which is generated from these.
--
-- NOTE ON DATE CONVENTIONS. Seasons use INCLUSIVE ends_on: "Jun 1 to Aug 31"
-- includes the night of Aug 31, because that is what an owner means. This is
-- deliberately different from room_occupancy's half-open [check-in, check-out),
-- where the checkout date is not a night. Mixing the two up is an easy and
-- expensive mistake, so every date here is a NIGHT, never a checkout.
CREATE TABLE rate_seasons (
  id        bigserial PRIMARY KEY,
  name      text NOT NULL,
  starts_on date NOT NULL,
  ends_on   date NOT NULL,

  -- NULL means "use settings.default_min_stay". A season may raise the minimum
  -- (a 3-night holiday weekend) but the global default is the floor.
  min_stay integer CHECK (min_stay IS NULL OR min_stay >= 1),

  -- Higher wins where seasons overlap. Without this, a Thanksgiving season
  -- sitting inside a Leaf Season range is undefined behaviour and the owner
  -- gets silently wrong prices.
  priority integer NOT NULL DEFAULT 0,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT season_ends_after_it_starts CHECK (ends_on >= starts_on)
);

CREATE INDEX rate_seasons_span_idx ON rate_seasons (starts_on, ends_on);

-- The seven-room grid. Per-room pricing is required, not optional: the rooms
-- are genuinely different, so a season sets a price per room.
CREATE TABLE rate_season_prices (
  season_id   bigint NOT NULL REFERENCES rate_seasons(id) ON DELETE CASCADE,
  room_id     bigint NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  price_cents integer NOT NULL CHECK (price_cents > 0),

  PRIMARY KEY (season_id, room_id)
);

-- Generated, never edited by hand. This is what availability reads, so a search
-- is an index lookup rather than a season-resolution exercise per request.
CREATE TABLE rate_calendar (
  room_id     bigint NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  -- The night, not a checkout date.
  date        date NOT NULL,
  price_cents integer NOT NULL CHECK (price_cents > 0),
  min_stay    integer NOT NULL CHECK (min_stay >= 1),

  PRIMARY KEY (room_id, date)
);

CREATE INDEX rate_calendar_date_idx ON rate_calendar (date);

-- The generator. It lives in the database rather than in Go so that the seed,
-- the admin "save season" path, and the monthly job all expand seasons the same
-- way. A second copy of this logic would eventually disagree with the first,
-- and the symptom would be wrong prices.
--
-- Being a single statement, it is atomic: availability never observes a
-- half-rebuilt calendar.
--
-- It rewrites only from p_from forward, so it cannot alter history. It also
-- cannot change what a guest already owes — booking_rooms snapshots nightly
-- prices and bookings snapshots the tax rate at the moment of booking.
-- +goose StatementBegin
CREATE FUNCTION rebuild_rate_calendar(p_from date, p_to date) RETURNS bigint AS $$
DECLARE
  inserted bigint;
BEGIN
  DELETE FROM rate_calendar WHERE date >= p_from;

  -- DISTINCT ON keeps the first row per (room, night) after ordering, so the
  -- highest-priority season covering that night wins. Ties go to the most
  -- recently created season, which is what an owner expects after adding an
  -- override on top of an existing range.
  --
  -- Nights no season covers get no row, and a room with no rate for a night is
  -- simply not sellable then. That is the correct failure: an unbookable night
  -- beats one sold at a guessed price.
  INSERT INTO rate_calendar (room_id, date, price_cents, min_stay)
  SELECT DISTINCT ON (p.room_id, g.night::date)
    p.room_id,
    g.night::date,
    p.price_cents,
    coalesce(s.min_stay, (SELECT default_min_stay FROM settings WHERE id))
  FROM generate_series(p_from, p_to, '1 day'::interval) AS g(night)
  JOIN rate_seasons s
    ON g.night::date BETWEEN s.starts_on AND s.ends_on
  JOIN rate_season_prices p
    ON p.season_id = s.id
  ORDER BY p.room_id, g.night::date, s.priority DESC, s.id DESC;

  GET DIAGNOSTICS inserted = ROW_COUNT;
  RETURN inserted;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS rebuild_rate_calendar(date, date);
DROP TABLE rate_calendar;
DROP TABLE rate_season_prices;
DROP TABLE rate_seasons;
