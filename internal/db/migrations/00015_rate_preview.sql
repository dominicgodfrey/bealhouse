-- +goose Up

-- The rate editor's "show a diff before saving" (ARCHITECTURE, decision #21),
-- and the removal of the duplicate that would otherwise have to exist to
-- produce it.
--
-- rebuild_rate_calendar already resolves seasons into nights. A preview needs
-- exactly that resolution and must not write, so the resolution is lifted out
-- into its own function and the rebuild is redefined to call it. One copy of
-- the priority rule, not two — a second copy would eventually disagree with the
-- first and the symptom would be a preview that lied about what saving does.
-- +goose StatementBegin
CREATE FUNCTION generated_rate_calendar(p_from date, p_to date)
RETURNS TABLE (room_id bigint, night date, price_cents integer, min_stay integer) AS $$
  -- DISTINCT ON keeps the first row per (room, night) after ordering, so the
  -- highest-priority season covering that night wins. Ties go to the most
  -- recently created season, which is what an owner expects after adding an
  -- override on top of an existing range.
  --
  -- Nights no season covers get no row, and a room with no rate for a night is
  -- simply not sellable then. That is the correct failure: an unbookable night
  -- beats one sold at a guessed price.
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
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

-- Unchanged in behaviour: still atomic, still future-only, still unable to
-- reach a confirmed booking's snapshotted prices. Only the resolution moved.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION rebuild_rate_calendar(p_from date, p_to date) RETURNS bigint AS $$
DECLARE
  inserted bigint;
BEGIN
  DELETE FROM rate_calendar WHERE date >= p_from;

  INSERT INTO rate_calendar (room_id, date, price_cents, min_stay)
  SELECT room_id, night, price_cents, min_stay
  FROM generated_rate_calendar(p_from, p_to);

  GET DIAGNOSTICS inserted = ROW_COUNT;
  RETURN inserted;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- What a rebuild over this window would change, without changing it.
--
-- A FULL OUTER JOIN rather than an inner one, because the three interesting
-- cases are not all "the number moved": a night that gains a price (a season
-- now covers it, so the room becomes sellable) and a night that loses one (no
-- season covers it any more, so the room quietly drops out of every search) are
-- the two an owner most needs to be shown before saving, and an inner join
-- would report neither.
--
-- IS DISTINCT FROM rather than <>, for the same reason: one side is NULL in
-- exactly those two cases and <> would answer NULL, which is not true.
-- +goose StatementBegin
CREATE FUNCTION rate_calendar_changes(p_from date, p_to date)
RETURNS TABLE (
  room_id   bigint,
  night     date,
  old_price integer,
  new_price integer,
  old_min   integer,
  new_min   integer
) AS $$
  SELECT
    coalesce(c.room_id, n.room_id),
    coalesce(c.date, n.night),
    c.price_cents,
    n.price_cents,
    c.min_stay,
    n.min_stay
  FROM (
    SELECT * FROM rate_calendar WHERE date BETWEEN p_from AND p_to
  ) c
  FULL OUTER JOIN generated_rate_calendar(p_from, p_to) n
    ON n.room_id = c.room_id AND n.night = c.date
  WHERE c.price_cents IS DISTINCT FROM n.price_cents
     OR c.min_stay   IS DISTINCT FROM n.min_stay;
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP FUNCTION IF EXISTS rate_calendar_changes(date, date);
-- +goose StatementEnd

-- Back to the self-contained version, so dropping the helper below is safe.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION rebuild_rate_calendar(p_from date, p_to date) RETURNS bigint AS $$
DECLARE
  inserted bigint;
BEGIN
  DELETE FROM rate_calendar WHERE date >= p_from;

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

-- +goose StatementBegin
DROP FUNCTION IF EXISTS generated_rate_calendar(date, date);
-- +goose StatementEnd
