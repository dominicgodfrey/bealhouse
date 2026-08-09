-- +goose Up

-- A sentence about each nearby highlight.
--
-- A name and a distance only helps somebody who already knows what Chutters is,
-- and the page is written for the person who has never been to Littleton.
--
-- NOT NULL DEFAULT '' rather than nullable, on the same terms as `distance`:
-- empty is a real state and the page renders the row without a sentence.
-- Additive and backfilled by its own default, so the old binary can run against
-- this schema for the second or two the deploy takes.
ALTER TABLE local_attractions ADD COLUMN description text NOT NULL DEFAULT '';

COMMENT ON COLUMN local_attractions.description IS
  'One or two sentences about the place. Empty means the row renders as name and distance alone.';

-- +goose Down
ALTER TABLE local_attractions DROP COLUMN description;
