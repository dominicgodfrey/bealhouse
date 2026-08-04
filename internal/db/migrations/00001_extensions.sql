-- +goose Up
-- btree_gist lets a GiST index mix an equality column (room_id) with a range
-- column (during), which is what the room_occupancy exclusion constraint in
-- step 2 requires. Nothing else in the schema works without it.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose Down
DROP EXTENSION IF EXISTS btree_gist;
