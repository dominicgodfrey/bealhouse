-- +goose Up

-- A sentence about each nearby highlight.
--
-- The list was a name, a distance and a link, which tells somebody who already
-- knows what Chutters is how far away it is. A guest choosing between Cannon
-- Mountain and Mount Eustis does not, and the whole point of the page is the
-- person who has never been to Littleton.
--
-- NOT NULL DEFAULT '' rather than nullable, on the same terms as `distance`
-- above it: empty is a real state — a place the owner has not got round to
-- describing — and the page renders the row without a sentence rather than with
-- an invented one. Nullable would add a third state that means the same thing.
--
-- Additive and backfilled by its own default, which is what the deploy needs:
-- the old binary runs against this schema for a second or two and never selects
-- the column.
ALTER TABLE local_attractions ADD COLUMN description text NOT NULL DEFAULT '';

COMMENT ON COLUMN local_attractions.description IS
  'One or two sentences about the place. Empty means the row renders as name and distance alone.';

-- +goose Down
ALTER TABLE local_attractions DROP COLUMN description;
