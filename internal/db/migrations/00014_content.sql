-- +goose Up

-- The tables behind the marketing site and the console's content screens: the
-- restaurant menu, the events business, the inquiries it generates, the notes
-- the owner keeps about a guest, and the prose on the four public pages.
--
-- Everything in here is the owner's, so everything in here ships empty. There
-- is no seed for any of it and the pages are written to say "nothing here yet"
-- rather than to show invented copy — a placeholder in the database is one
-- somebody has to remember to delete.

-- The restaurant menu (decision #12). Structured rather than a blob of text,
-- because the same rows render the page and the JSON-LD `Menu` that gets the
-- inn into a search result.
CREATE TABLE menu_sections (
  id          bigserial PRIMARY KEY,
  name        text NOT NULL,
  -- A line under the heading — "served until 3pm", and so on. Optional.
  description text NOT NULL DEFAULT '',
  sort_order  integer NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT menu_section_name_present CHECK (btrim(name, E' \t\r\n') <> '')
);

CREATE INDEX menu_sections_sort_idx ON menu_sections (sort_order);

CREATE TABLE menu_items (
  id          bigserial PRIMARY KEY,
  section_id  bigint NOT NULL REFERENCES menu_sections(id) ON DELETE CASCADE,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',

  -- Integer cents, like every other price in this schema. Zero is allowed and
  -- means the item carries no price of its own — a market-price special, or a
  -- side listed under a prix fixe.
  price_cents integer NOT NULL DEFAULT 0 CHECK (price_cents >= 0),

  -- Off rather than deleted, so tonight's sold-out dish keeps its description
  -- and its place in the order for tomorrow.
  is_available boolean NOT NULL DEFAULT true,

  sort_order integer NOT NULL DEFAULT 0,

  CONSTRAINT menu_item_name_present CHECK (btrim(name, E' \t\r\n') <> '')
);

CREATE INDEX menu_items_section_sort_idx ON menu_items (section_id, sort_order);

-- The events business. A civil date, not a timestamp, for the same reason
-- check-in is one: "the barn dance is on the 14th" is a statement about the
-- calendar in Littleton and not about an instant.
CREATE TABLE events (
  id          bigserial PRIMARY KEY,
  title       text NOT NULL,
  happens_on  date,
  description text NOT NULL DEFAULT '',

  -- Drafting an event and publishing it are different acts. Unpublished rows
  -- are invisible to the public endpoint and visible in the console.
  is_published boolean NOT NULL DEFAULT false,

  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT event_title_present CHECK (btrim(title, E' \t\r\n') <> '')
);

CREATE INDEX events_happens_on_idx ON events (happens_on);

-- A table rather than the `photos text[]` the data model sketches, because a
-- photo needs alt text and an array cannot carry it. room_photos makes alt text
-- NOT NULL for exactly this reason: a gallery image with no alt text is
-- invisible to a screen reader, and the honesty rules in this schema are
-- enforced by the database rather than by whichever form happened to write the
-- row.
CREATE TABLE event_photos (
  id         bigserial PRIMARY KEY,
  event_id   bigint NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  path       text NOT NULL,
  alt_text   text NOT NULL,
  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT event_photo_alt_text_present CHECK (btrim(alt_text, E' \t\r\n') <> '')
);

CREATE INDEX event_photos_event_sort_idx ON event_photos (event_id, sort_order);

-- What the events page's form produces. Not a booking: decision #11 puts event
-- booking and deposits out of scope, so this is a message the owner answers,
-- and the only thing the system does with it is refuse to lose it.
CREATE TABLE event_inquiries (
  id         bigserial PRIMARY KEY,
  name       text NOT NULL,
  email      text NOT NULL,
  phone      text NOT NULL DEFAULT '',
  event_date date,
  party_size integer CHECK (party_size IS NULL OR party_size > 0),
  message    text NOT NULL DEFAULT '',

  --   new        nobody has looked at it
  --   contacted  the owner has replied
  --   closed     done with, either way
  status text NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'contacted', 'closed')),

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT inquiry_name_present CHECK (btrim(name, E' \t\r\n') <> ''),
  CONSTRAINT inquiry_email_looks_like_one CHECK (email LIKE '%_@_%')
);

-- Newest first is the only order this is ever read in.
CREATE INDEX event_inquiries_created_at_idx ON event_inquiries (created_at DESC);

-- What the owner remembers about a guest, which is most of what a seven-room
-- inn runs on and none of which fits in a booking.
--
-- The author is recorded because two people run this console and "who said the
-- dog is fine" is a question with an answer. ON DELETE SET NULL rather than
-- CASCADE: removing a user must not silently delete what they wrote about a
-- guest who is still coming back.
CREATE TABLE guest_notes (
  id             bigserial PRIMARY KEY,
  guest_id       bigint NOT NULL REFERENCES guests(id) ON DELETE CASCADE,
  author_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
  body           text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT guest_note_body_present CHECK (btrim(body, E' \t\r\n') <> '')
);

CREATE INDEX guest_notes_guest_id_idx ON guest_notes (guest_id, created_at DESC);

-- The prose on the four public pages, on exactly the terms email_templates
-- holds the prose in the seven messages: a row is an override, and no row means
-- the page renders its structure with nothing in the slot.
--
-- No CHECK against a list of slugs, for the same reason email_templates has
-- none: which pages exist is a property of the binary, and a row naming one
-- that does not is inert rather than harmful.
--
-- The body is plain text — paragraphs separated by blank lines — and not
-- markdown or HTML. The owner is writing sentences about an inn, and a rich
-- editor here would mean either a parser in the bundle or a way to put a
-- <script> on the public site from a phone.
CREATE TABLE page_copy (
  slug       text PRIMARY KEY,
  heading    text NOT NULL DEFAULT '',
  body       text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE page_copy IS
  'Owner-edited prose for the public pages. A row overrides the blank slot the page ships with; deleting the row empties it again.';

-- +goose Down
DROP TABLE page_copy;
DROP TABLE guest_notes;
DROP TABLE event_inquiries;
DROP TABLE event_photos;
DROP TABLE events;
DROP TABLE menu_items;
DROP TABLE menu_sections;
