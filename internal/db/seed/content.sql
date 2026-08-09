-- Provisional copy, transcribed from the inn's current site (thebealhouse.com)
-- on 2026-08-08. Every sentence below is the owner's own, taken off a page they
-- wrote; nothing here was composed for this repository.
--
-- It exists so the site can be looked at with real words in it. It is still
-- temporary: the console is where this content belongs, and the moment the
-- owner edits a page or a room there, that edit is the truth and this file is
-- only how the row got its first value.
--
-- WHY THIS IS A SEPARATE FILE from rooms.sql. rooms.sql describes the seven
-- rooms as facts — occupancy, beds, views, the pet room — and argues in its own
-- header that descriptions stay PLACEHOLDER so a leak onto the live site is
-- unmistakable rather than plausible. That argument still holds; this file is
-- the copy, and deleting it is how you get back to the state that argument
-- describes. Run it AFTER rooms.sql, which upserts description back to the
-- placeholder.
--
-- WHAT IS DELIBERATELY MISSING. The current site publishes five of the seven
-- rooms, and only one of those five carries a written description. So exactly
-- one room gets one below. Flume, Back Lavender, Garden Suite, Rose Chamber,
-- Blue Room and Washington Room are left blank on purpose — the pages render no
-- paragraph at all rather than a sentence somebody here invented, which is the
-- same rule page_copy follows. Do not fill these in from imagination; ask the
-- owner.
--
-- Re-runnable.

BEGIN;

-- The one room the current site describes.
UPDATE rooms SET
  description = 'Mrs. Beal''s suite features a queen-sized bedroom in the back, a sitting room with a single pull out sofa in the front room and a full bath.',
  updated_at  = now()
WHERE slug = 'mrs-beals-suite';

-- The other six say nothing yet, and an empty string is how the room page and
-- the card both know to print nothing. This also undoes the PLACEHOLDER text
-- rooms.sql leaves behind, so the two files can be run in either order and the
-- last word is still the owner's.
UPDATE rooms SET
  description = '',
  updated_at  = now()
WHERE slug <> 'mrs-beals-suite';

-- Amenities, as the owner has since described them.
--
-- Built up in three statements rather than written out seven times, so "every
-- room has X" is stated once and the exceptions are visible as exceptions.
-- "Full bathroom" is gone: shower, bathtub and towels say the same thing more
-- specifically, and the home page's "suites with full bathrooms" line still
-- makes the general claim.
--
-- Non-smoking is an amenity here rather than a column, alongside the rest. It
-- is true of all seven and always will be, but the amenities array is exactly
-- the open list of house facts a guest scans, and a boolean column would need
-- a migration, an API field and a line of UI to say what one string says.
UPDATE rooms SET amenities = ARRAY[
  'Non-smoking',
  'Heat',
  'Wifi',
  'Shower',
  'Bathtub',
  'Towels',
  'Slippers',
  'Shampoo',
  'Conditioner',
  'Coffee and tea in the morning'
], updated_at = now();

-- Every room but the Rose Chamber. ARRAY[...] rather than a bare string on the
-- left: `'x' || amenities` leaves Postgres unable to tell a text operand from
-- an array one and it reads the literal as a malformed array.
UPDATE rooms SET amenities = ARRAY['Air conditioning'] || amenities, updated_at = now()
WHERE slug <> 'rose-chamber';

-- The three largest.
UPDATE rooms SET amenities = amenities || ARRAY['Jacuzzi', 'Dedicated workspace'],
  updated_at = now()
WHERE slug IN ('mrs-beals-suite', 'garden-suite', 'flume');

-- The About page is back and the owner's story is on it. The home page is one
-- screenful now, so a paragraph there had nowhere to go.
--
-- The 'home' slot stays — its photographs are the backdrop and its copy is the
-- meta description — but it no longer renders a paragraph, so the row is
-- deleted here and the same words go in under 'about' below.
DELETE FROM page_copy WHERE slug = 'home';

-- Page prose. Same contract as the console writes: a row is an override, and
-- deleting the row empties the slot. Headings are left blank where the page
-- already carries an h1 that says the same thing.
INSERT INTO page_copy (slug, heading, body) VALUES

  -- The owner's own account of themselves, from their About page. Their
  -- "Welcome to the Beal House" banner is deliberately not carried over: the
  -- home page's search already says the inn is open and taking bookings.
  --
  -- The heading is blank because the About page carries its own h1.
  ('about', '',
   E'Hwasoo and Tom met in Seoul, Korea. We have spent our careers in service and food in the United States and in Asia. We could not be more delighted to have found Littleton and the Beal House!\n\nWe look forward to being another joyful place where community members gather and break bread together.'),

  ('rooms', '',
   'The Beal House has seven classic and comfortable suites with full bathrooms available for booking with a two-night minimum stay.'),

  ('restaurant', '',
   E'When open, the restaurant will feature a rotating menu of homemade dishes prepared by Hwasoo from around the world and here at home.\n\nWe look forward to serving you once our restaurant is open!'),

  ('events', '',
   E'Littleton hosts fun events all year round! Join us for Music in the Streets, Pumpkins on the River, Octoberfest, holiday parades and concerts in the parks just to name a few.\n\nThe Beal House has hosted intimate and special gatherings for your families for years. We are honored to continue that tradition. Contact us to discuss event hosting at The Beal House.'),

  -- The local-area page, from the site's own Local Area page.
  --
  -- The "Nearby Highlights" list is NOT here — it is local_attractions, seeded
  -- from attractions.sql. It has three fields per entry and one of them is a
  -- link, and squeezing that into prose meant either a parser in the bundle or
  -- a page that could not link anything.
  ('local-area', 'Littleton, New Hampshire',
   -- Concatenated with || rather than by juxtaposition: only the first fragment
   -- of an adjacent-literal string may carry the E prefix, so the \n\n in a
   -- continuation would come out as a literal backslash-n.
   E'Littleton, New Hampshire is known as the "Glad Town" and birthplace of Pollyanna author Eleanor H. Porter.\n\n'
   || 'Start your stay with a stroll down our award-winning Main Street featuring an array of unique boutiques and galleries. '
   || 'Nature enthusiasts can explore the scenic Littleton Riverwalk, cross the iconic covered bridge, or venture onto the '
   || 'Parker Mountain Trails for miles of premier hiking and mountain biking. After your adventures, unwind with a craft brew '
   || 'at one of our renowned local breweries or cozy cafes. Whether you''re waving at the Pollyanna sculpture or discovering '
   || E'the vibrant River District, Littleton offers a quintessential New England experience.\n\n'
   || E'Discover more about Littleton at www.GoLittleton.com and www.DiscoverLittleton.com'),

  -- The policies page carries ONLY what the owner has to say in words. Every
  -- rule with a number in it — the deposit split, the cancellation deadline,
  -- the hold, the stay limits, the tax — is rendered from settings and from
  -- the pricing package, so the page a guest agrees to states what the code
  -- will actually do. Typing those numbers here is how they come to disagree.
  ('policies', '',
   E'These are the terms you agree to when you book. The house rules below are ours; the booking and refund rules are applied automatically by the booking system.\n\n'
   || E'The Beal House is entirely non-smoking, indoors and on the porches.\n\n'
   || E'It is a historic home and every guest room is up stairs. Please call us before booking if that is difficult and we will help you find the room that suits you best.\n\n'
   || 'Questions about any of this are welcome before you book rather than after. The inn answers its telephone.')

ON CONFLICT (slug) DO UPDATE SET
  heading    = EXCLUDED.heading,
  body       = EXCLUDED.body,
  updated_at = now();

COMMIT;
