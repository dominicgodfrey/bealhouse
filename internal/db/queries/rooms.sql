-- name: GetRoomIDBySlug :one
SELECT id FROM rooms WHERE slug = sqlc.arg(slug);

-- name: GetSettings :one
SELECT * FROM settings WHERE id;
