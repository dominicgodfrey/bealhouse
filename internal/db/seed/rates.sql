-- A single placeholder season so the system is usable before the owner enters
-- real seasons in the admin console. Prices are the owner's "starting at"
-- figures halved, since those were quoted as 2-night totals.
--
-- min_stay is left NULL so it falls back to settings.default_min_stay, which is
-- 2 for every room. Real seasons will raise it where a holiday weekend needs 3.
--
-- Re-runnable: seasons are replaced wholesale and the calendar regenerated.

BEGIN;

-- Cascades to rate_season_prices.
DELETE FROM rate_seasons;

WITH season AS (
  INSERT INTO rate_seasons (name, starts_on, ends_on, min_stay, priority)
  VALUES ('Base rate (placeholder)', DATE '2026-01-01', DATE '2032-12-31', NULL, 0)
  RETURNING id
)
INSERT INTO rate_season_prices (season_id, room_id, price_cents)
SELECT season.id, r.id,
  CASE r.slug
    WHEN 'mrs-beals-suite' THEN 20000
    WHEN 'garden-suite'    THEN 20000
    WHEN 'flume'           THEN 20000
    ELSE 15000
  END
FROM season CROSS JOIN rooms r;

-- Generate two years forward from today at the inn. Postgres' current_date
-- follows the session timezone, which is UTC in the container, so the inn's
-- date is computed explicitly rather than assumed.
SELECT rebuild_rate_calendar(
  (now() AT TIME ZONE 'America/New_York')::date,
  ((now() AT TIME ZONE 'America/New_York')::date + INTERVAL '24 months')::date
) AS nights_generated;

COMMIT;
