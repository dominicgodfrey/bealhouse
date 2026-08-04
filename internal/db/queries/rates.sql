-- Regenerate the nightly calendar from p_from forward. Atomic, and future-only:
-- it cannot rewrite history or re-price a confirmed stay.
-- name: RebuildRateCalendar :one
SELECT rebuild_rate_calendar(sqlc.arg(from_date), sqlc.arg(to_date));

-- name: GetRateCalendarEntry :one
SELECT room_id, date, price_cents, min_stay
FROM rate_calendar
WHERE room_id = sqlc.arg(room_id) AND date = sqlc.arg(date);

-- Nightly rates for one room across a stay. The checkout date is excluded
-- because it is not a night.
-- name: ListNightlyRates :many
SELECT date, price_cents, min_stay
FROM rate_calendar
WHERE room_id = sqlc.arg(room_id)
  AND date >= sqlc.arg(checkin)
  AND date < sqlc.arg(checkout)
ORDER BY date;

-- name: CreateRateSeason :one
INSERT INTO rate_seasons (name, starts_on, ends_on, min_stay, priority)
VALUES (sqlc.arg(name), sqlc.arg(starts_on), sqlc.arg(ends_on), sqlc.narg(min_stay), sqlc.arg(priority))
RETURNING id;

-- name: SetSeasonPrice :exec
INSERT INTO rate_season_prices (season_id, room_id, price_cents)
VALUES (sqlc.arg(season_id), sqlc.arg(room_id), sqlc.arg(price_cents))
ON CONFLICT (season_id, room_id) DO UPDATE SET price_cents = EXCLUDED.price_cents;

-- name: DeleteAllRateSeasons :execrows
DELETE FROM rate_seasons;
