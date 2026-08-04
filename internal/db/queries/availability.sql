-- Rooms that can actually be sold for a span, with their nightly prices.
--
-- Four conditions have to hold at once, and every one of them is a real way to
-- oversell or misprice a room:
--
--   capacity   the room sleeps the party
--   pets       when the guest is travelling with one, the room accepts them
--   occupancy  nothing overlaps the stay, including holds and owner blocks
--   rates      every night of the stay is priced, and the arrival night's
--              minimum stay is satisfied
--
-- The count(*) = nights check is what enforces "every night is priced". A room
-- whose season coverage stops mid-stay drops out rather than being sold at a
-- partial price.
--
-- Minimum stay is evaluated at the arrival night specifically, not as the
-- maximum across the stay: a 3-night holiday season should stop a booking that
-- starts inside it, not one that merely passes through.
-- name: SearchAvailability :many
SELECT
  r.id,
  r.slug,
  r.name,
  r.description,
  r.view,
  r.max_occupancy,
  r.amenities,
  r.is_pet_friendly,
  r.pet_fee_cents,
  array_agg(c.price_cents ORDER BY c.date)::int[] AS nightly_prices
FROM rooms r
JOIN rate_calendar c
  ON c.room_id = r.id
 AND c.date >= sqlc.arg(checkin)::date
 AND c.date <  sqlc.arg(checkout)::date
WHERE r.max_occupancy >= sqlc.arg(guests)::int
  -- An unchecked pet box is not a filter: Back Lavender is an ordinary room
  -- that also happens to accept pets, and hiding it would cost bookings.
  AND (NOT sqlc.arg(with_pet)::boolean OR r.is_pet_friendly)
  AND NOT EXISTS (
    SELECT 1 FROM room_occupancy o
    WHERE o.room_id = r.id
      AND o.during && daterange(sqlc.arg(checkin)::date, sqlc.arg(checkout)::date, '[)')
  )
GROUP BY r.id
HAVING count(*) = (sqlc.arg(checkout)::date - sqlc.arg(checkin)::date)
   AND (sqlc.arg(checkout)::date - sqlc.arg(checkin)::date)
       >= max(c.min_stay) FILTER (WHERE c.date = sqlc.arg(checkin)::date)
ORDER BY r.sort_order;

-- name: ListPhotosForRooms :many
SELECT room_id, path, alt_text, sort_order
FROM room_photos
WHERE room_id = ANY(sqlc.arg(room_ids)::bigint[])
ORDER BY room_id, sort_order, id;

-- name: ListBedsForRooms :many
SELECT room_id, bed_type, count, location
FROM room_beds
WHERE room_id = ANY(sqlc.arg(room_ids)::bigint[])
ORDER BY room_id, bed_type;
