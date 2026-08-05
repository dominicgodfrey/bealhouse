-- name: GetRoomIDBySlug :one
SELECT id FROM rooms WHERE slug = sqlc.arg(slug);

-- Everything the room page shows. Accessibility comes back even though the
-- filter is switched off (decision #22): the page should describe what is
-- actually true about a room, and the day real feature data exists it renders
-- without a query change.
-- name: GetRoomBySlug :one
SELECT
  id,
  slug,
  name,
  description,
  view,
  max_occupancy,
  amenities,
  is_accessible,
  accessibility_features,
  is_pet_friendly,
  pet_fee_cents
FROM rooms
WHERE slug = sqlc.arg(slug);

-- The tax rate is scaled to hundred-thousandths in SQL so Go never decodes a
-- numeric into a float. pricing.Rate expects exactly this scale.
-- name: GetSettings :one
SELECT
  default_min_stay,
  max_stay_nights,
  (tax_rate * 100000)::bigint AS tax_rate_scaled,
  (refund_processing_rate * 100000)::bigint AS refund_processing_rate_scaled,
  hold_ttl_minutes,
  payment_grace_minutes,
  checkin_time,
  checkout_time,
  accessibility_notice
FROM settings WHERE id;
