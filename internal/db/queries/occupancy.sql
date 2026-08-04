-- Ranges are built and unpacked inside SQL rather than passed across the
-- boundary as range types. Go therefore only ever sees plain dates, and the
-- half-open convention stays in one place instead of being re-implemented by
-- every caller.

-- Claiming a room takes a per-room advisory lock first.
--
-- Without it, concurrent inserts wait on each other's uncommitted rows and
-- Postgres periodically finds a cycle among the waiters, aborting one with
-- 40P01 deadlock_detected. That is a raw driver error during checkout which a
-- guest cannot distinguish from the room genuinely being gone. Retrying helps
-- but is probabilistic: under staggered overlapping spans it still leaks.
--
-- The lock makes waiters queue in a defined order instead of forming a cycle,
-- so a loser gets a clean 23P01 every time. It is scoped per room, so the other
-- six are unaffected, and it is held only for this statement.
--
-- 4771 namespaces the lock so it cannot collide with any other advisory lock
-- added later. The CTE is evaluated before the insert because the insert reads
-- from it.
-- name: CreateOccupancy :one
WITH room_lock AS (
  SELECT pg_advisory_xact_lock(4771, sqlc.arg(room_id)::int) AS acquired
)
INSERT INTO room_occupancy (room_id, during, kind, source, booking_id, expires_at, reason)
SELECT
  sqlc.arg(room_id),
  daterange(sqlc.arg(checkin)::date, sqlc.arg(checkout)::date, '[)'),
  sqlc.arg(kind),
  sqlc.arg(source),
  sqlc.narg(booking_id),
  sqlc.narg(expires_at),
  sqlc.arg(reason)
FROM room_lock
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
