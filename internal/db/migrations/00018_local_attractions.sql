-- +goose Up

-- What is near the inn: the list on the local-area page.
--
-- A table rather than more lines in page_copy, for the same reason the menu is
-- one: it has fields. A name, how far away it is and where to read more are
-- three things, and squeezing them into a paragraph of plain text means either
-- a parser in the bundle to pull them back out or a page that cannot link
-- anything. page_copy stays what it is — prose — and this carries the list.
--
-- url is nullable and means "no link", not "link to nothing". Some of these are
-- a hill with a rope tow and a Facebook page; a row with no site should render
-- as plain text rather than as a dead link, and an owner who does not know a
-- URL must be able to leave it out.
--
-- distance is free text, not minutes. "Walking distance" is the honest answer
-- for half this list and is not a number; an integer column would force the
-- owner to invent one and the page to print "0 minutes away".
CREATE TABLE local_attractions (
  id         bigserial PRIMARY KEY,
  name       text NOT NULL,
  distance   text NOT NULL DEFAULT '',
  url        text,
  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT local_attraction_name_present CHECK (btrim(name, E' \t\r\n') <> ''),

  -- A link the console can save is one a browser can follow. This is the same
  -- boundary the prose linker draws in the front end and it is drawn here too,
  -- because the console is a form and a form can post anything: without it,
  -- `javascript:` in this column would be a script tag by another name on the
  -- one page that renders owner-supplied hrefs.
  CONSTRAINT local_attraction_url_is_http
    CHECK (url IS NULL OR url ~* '^https?://.')
);

CREATE INDEX local_attractions_sort_idx ON local_attractions (sort_order, id);

COMMENT ON TABLE local_attractions IS
  'The nearby-highlights list on the local-area page. Owner-managed; url NULL means the entry renders as plain text.';

-- +goose Down
DROP TABLE local_attractions;
