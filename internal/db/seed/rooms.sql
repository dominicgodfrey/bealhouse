-- The seven real rooms, as supplied by the owner. Re-runnable: rooms upsert on
-- slug, and each room's beds are replaced wholesale.
--
-- NOT seeded here:
--
--   Rates.       Nightly prices belong to rate_season_prices. The owner's
--                "starting at" figures are 2-night totals, so the base
--                per-night rates are $200 (Mrs. Beal's, Garden, Flume Suite) and
--                $150 (Rose, Blue, Washington, Back Lavender).
--
--   Amenities.   Left empty deliberately. The owner adds and removes these in
--                the admin console; seeding guesses would only have to be
--                undone.
--
--   Photos.      Uploaded through admin once the image pipeline exists.
--
-- Accessibility: every room requires stairs, so is_accessible stays false on
-- all seven and the search filter is not offered. settings.accessibility_notice
-- carries the disclaimer shown to guests instead.
--
-- Pets: none, in any room. Back Lavender was the pet room until the owner
-- withdrew it (decision #23, revised), and the columns went with it.
--
-- Descriptions are marked placeholders on purpose: if one ever reaches the
-- live site it should be unmistakable rather than plausible. content.sql, run
-- after this file, replaces them with what the inn's current site actually
-- says — which is one description for one room and nothing for the other six.

BEGIN;

INSERT INTO rooms (slug, name, description, view, max_occupancy, sort_order)
VALUES
  ('mrs-beals-suite', 'Mrs. Beal''s Suite',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Street and mountain view in front, hill view in back', 3, 1),

  ('garden-suite', 'Garden Suite',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Front room has mountain and street view; back room has hill view', 4, 2),

  ('flume', 'Flume Suite',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Street and mountain view', 2, 3),

  ('rose-chamber', 'Rose Chamber',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Backyard, obstructed view', 2, 4),

  ('washington-room', 'Washington Room',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Street and mountain view', 2, 5),

  ('blue-room', 'Blue Room',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Hill view at the back', 2, 6),

  ('back-lavender', 'Back Lavender',
   'PLACEHOLDER — final copy to be supplied by the owner.',
   'Hill view over the backyard', 3, 7)

ON CONFLICT (slug) DO UPDATE SET
  name            = EXCLUDED.name,
  description     = EXCLUDED.description,
  view            = EXCLUDED.view,
  max_occupancy   = EXCLUDED.max_occupancy,
  sort_order      = EXCLUDED.sort_order,
  updated_at      = now();

DELETE FROM room_beds;

INSERT INTO room_beds (room_id, bed_type, count, location)
SELECT r.id, b.bed_type, b.count, b.location
FROM rooms r
JOIN (VALUES
  ('mrs-beals-suite', 'queen',  1, ''),
  ('mrs-beals-suite', 'daybed', 1, 'sitting room'),
  ('garden-suite',    'queen',  1, ''),
  ('garden-suite',    'full',   1, ''),
  ('flume',           'king',   1, ''),
  ('rose-chamber',    'queen',  1, ''),
  ('washington-room', 'queen',  1, ''),
  ('blue-room',       'queen',  1, ''),
  ('back-lavender',   'queen',  1, ''),
  ('back-lavender',   'twin',   1, 'back room')
) AS b(slug, bed_type, count, location) ON b.slug = r.slug;

COMMIT;
