-- The inn's real seasons and prices, as confirmed by the owner on 2026-09-07.
--
-- Nightly, before the 8.5% Meals & Rooms tax, and there is no other fee of any
-- kind. Two nights minimum in every season with no holiday exceptions, so
-- min_stay is NULL on every row and settings.default_min_stay (2) applies.
--
-- The owner priced five named seasons — Winter, Low Spring, Summer, Fall, Low
-- Fall — and four of the five carry the same price for every room. Only Fall
-- differs. So what is seeded is the shape of the grid rather than its labels:
--
--   Standard   every night, $200 for the three suites and $150 for the four
--              rooms, one row spanning the years
--   Fall       August 14th to October 31st, one row per year at priority 1,
--              $250 and $200
--
-- Two rows a year cannot have a gap — Standard covers every night Fall does
-- not — where the five as written did: Fall ended on the 30th and Low Fall
-- began on November 1st, and a night no season covers is a night no room is
-- sold on, with nothing in any log to say so. The owner took Fall through the
-- 31st. If they later want a real winter price, it is a third row laid over
-- Standard, not a re-plan.
--
-- Seeded through 2030. rates.rebuild generates the calendar 24 months forward,
-- so this needs extending (from /admin/rates, one Fall row a year) before the
-- autumn of 2029, or the horizon creeps in with no error anywhere.
--
-- Re-runnable: seasons are replaced wholesale and the calendar regenerated.

BEGIN;

-- Cascades to rate_season_prices.
DELETE FROM rate_seasons;

WITH standard AS (
  INSERT INTO rate_seasons (name, starts_on, ends_on, min_stay, priority)
  VALUES ('Standard', DATE '2026-01-01', DATE '2030-12-31', NULL, 0)
  RETURNING id
)
INSERT INTO rate_season_prices (season_id, room_id, price_cents)
SELECT standard.id, r.id,
  CASE r.slug
    WHEN 'mrs-beals-suite' THEN 20000
    WHEN 'garden-suite'    THEN 20000
    WHEN 'flume'           THEN 20000
    ELSE 15000
  END
FROM standard CROSS JOIN rooms r;

WITH fall AS (
  INSERT INTO rate_seasons (name, starts_on, ends_on, min_stay, priority)
  SELECT 'Fall ' || y, make_date(y, 8, 14), make_date(y, 10, 31), NULL, 1
  FROM generate_series(2026, 2030) AS y
  RETURNING id
)
INSERT INTO rate_season_prices (season_id, room_id, price_cents)
SELECT fall.id, r.id,
  CASE r.slug
    WHEN 'mrs-beals-suite' THEN 25000
    WHEN 'garden-suite'    THEN 25000
    WHEN 'flume'           THEN 25000
    ELSE 20000
  END
FROM fall CROSS JOIN rooms r;

-- Generate two years forward from today at the inn. Postgres' current_date
-- follows the session timezone, which is UTC in the container, so the inn's
-- date is computed explicitly rather than assumed.
SELECT rebuild_rate_calendar(
  (now() AT TIME ZONE 'America/New_York')::date,
  ((now() AT TIME ZONE 'America/New_York')::date + INTERVAL '24 months')::date
) AS nights_generated;

COMMIT;
