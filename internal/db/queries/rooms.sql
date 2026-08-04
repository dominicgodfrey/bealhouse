-- name: GetRoomIDBySlug :one
SELECT id FROM rooms WHERE slug = sqlc.arg(slug);

-- The tax rate is scaled to hundred-thousandths in SQL so Go never decodes a
-- numeric into a float. pricing.Rate expects exactly this scale.
-- name: GetSettings :one
SELECT
  default_min_stay,
  (tax_rate * 100000)::bigint AS tax_rate_scaled,
  hold_ttl_minutes,
  checkin_time,
  checkout_time,
  accessibility_notice
FROM settings WHERE id;
