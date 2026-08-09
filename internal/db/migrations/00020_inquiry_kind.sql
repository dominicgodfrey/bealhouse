-- +goose Up

-- Which form wrote an inbox row.
--
-- The events enquiry is no longer the only thing that writes to this table: the
-- home page has a contact form now. One inbox with a kind on each row rather
-- than a second table, because the owner reads them in one place, answers them
-- the same way, and the columns are the same columns — an event enquiry just
-- fills two more of them in.
--
-- DEFAULT 'event' backfills every existing row correctly: until this migration
-- there was nothing else it could have been. That also means an older binary
-- running against this schema during a deploy inserts a correct row without
-- knowing the column exists.
ALTER TABLE event_inquiries
  ADD COLUMN kind text NOT NULL DEFAULT 'event'
    CHECK (kind IN ('event', 'contact'));

CREATE INDEX event_inquiries_kind_created_at_idx
  ON event_inquiries (kind, created_at DESC);

COMMENT ON COLUMN event_inquiries.kind IS
  'Which form wrote the row: the events enquiry form, or the general contact form on the home page.';

-- +goose Down
DROP INDEX event_inquiries_kind_created_at_idx;
ALTER TABLE event_inquiries DROP COLUMN kind;
