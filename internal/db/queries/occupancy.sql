-- Ranges are built and unpacked inside SQL rather than passed across the
-- boundary as range types. Go therefore only ever sees plain dates, and the
-- half-open convention stays in one place instead of being re-implemented by
-- every caller.

-- name: CreateOccupancy :one
INSERT INTO room_occupancy (room_id, during, kind, source, booking_id, expires_at, reason)
VALUES (
  sqlc.arg(room_id),
  daterange(sqlc.arg(checkin)::date, sqlc.arg(checkout)::date, '[)'),
  sqlc.arg(kind),
  sqlc.arg(source),
  sqlc.narg(booking_id),
  sqlc.narg(expires_at),
  sqlc.arg(reason)
)
RETURNING id;

-- name: DeleteOccupancy :execrows
DELETE FROM room_occupancy WHERE id = sqlc.arg(id);

-- name: SweepExpiredHolds :execrows
DELETE FROM room_occupancy
WHERE kind = 'hold' AND expires_at <= now();

-- name: ListRoomOccupancy :many
SELECT
  id,
  room_id,
  lower(during)::date AS checkin,
  upper(during)::date AS checkout,
  kind,
  source,
  booking_id,
  expires_at
FROM room_occupancy
WHERE room_id = sqlc.arg(room_id)
ORDER BY during;

-- Rooms with nothing overlapping the requested span. This is the occupancy half
-- of availability; capacity, rates and minimum stay join on top of it.
-- name: ListRoomsFreeBetween :many
SELECT r.id
FROM rooms r
WHERE NOT EXISTS (
  SELECT 1 FROM room_occupancy o
  WHERE o.room_id = r.id
    AND o.during && daterange(sqlc.arg(checkin)::date, sqlc.arg(checkout)::date, '[)')
)
ORDER BY r.sort_order;
