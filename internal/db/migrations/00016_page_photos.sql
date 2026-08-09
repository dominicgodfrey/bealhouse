-- +goose Up

-- Photographs on the marketing pages, keyed by the same slug page_copy uses.
--
-- Rooms have room_photos and an event has event_photos, but the restaurant,
-- events and local-area pages had nowhere to put a picture at all — the pages
-- were structure and prose only. This is that missing slot, and one table
-- rather than three because the four pages differ in what they say and not at
-- all in how they show a photograph.
--
-- Deliberately NOT a foreign key to page_copy. A page with photographs and no
-- prose is a real state — the restaurant page for most of this year — and
-- page_copy's own contract is that no row means nothing written. Making this
-- reference it would mean an empty copy row had to be kept alive to hang
-- pictures off, which is exactly the second way of saying "nothing" that
-- DeletePageCopy exists to avoid. Which slugs are real is console.PageSlugs(),
-- a property of the binary, and the save checks against it.
--
-- alt_text is NOT NULL with the same CHECK room_photos and event_photos carry.
-- A gallery image with no alt text is invisible to a screen reader, and this
-- schema enforces that in the database rather than in whichever form wrote the
-- row.
CREATE TABLE page_photos (
  id         bigserial PRIMARY KEY,
  slug       text NOT NULL,
  path       text NOT NULL,
  alt_text   text NOT NULL,
  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT page_photo_alt_text_present CHECK (btrim(alt_text, E' \t\r\n') <> '')
);

CREATE INDEX page_photos_slug_sort_idx ON page_photos (slug, sort_order);

COMMENT ON TABLE page_photos IS
  'Owner-uploaded photographs for the public pages, keyed by the same slug as page_copy. Independent of it: a page may have pictures and no prose.';

-- +goose Down
DROP TABLE page_photos;
