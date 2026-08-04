-- The seven real rooms, as supplied by the owner. Re-runnable: rooms upsert on
-- slug, and each room's beds are replaced wholesale.
--
-- NOT seeded here: nightly rates. Those belong to rate_season_prices, which
-- arrives with the rates schema. The owner's "starting at" prices are 2-night
-- totals, so the base per-night rates are $200 (Mrs. Beal's, Garden, Flume) and
-- $150 (Rose, Blue, Washington, Back Lavender).
--
-- ACCESSIBILITY: the owner marked Mrs. Beal's Suite and Rose Chamber as
-- "accessibility friendly", but did not say which features that means. The
-- rooms table refuses is_accessible = true without at least one named feature,
-- so both are seeded false rather than making a promise nobody has verified.
-- Flip them on once the real features are known -- see the commented UPDATE at
-- the bottom of this file.

BEGIN;

INSERT INTO rooms (slug, name, view, max_occupancy, is_pet_friendly, pet_fee_cents, sort_order)
VALUES
  ('mrs-beals-suite', 'Mrs. Beal''s Suite',
   'Street and mountain view in front, hill view in back', 3, false, 0, 1),

  ('garden-suite', 'Garden Suite',
   'Front room has mountain and street view; back room has hill view', 4, false, 0, 2),

  ('flume', 'Flume',
   'Street and mountain view', 2, false, 0, 3),

  ('rose-chamber', 'Rose Chamber',
   NULL, 2, false, 0, 4),

  ('washington-room', 'Washington Room',
   'Street and mountain view', 2, false, 0, 5),

  ('blue-room', 'Blue Room',
   'Hill view at the back', 2, false, 0, 6),

  ('back-lavender', 'Back Lavender',
   'Hill view over the backyard', 3, true, 5000, 7)

ON CONFLICT (slug) DO UPDATE SET
  name            = EXCLUDED.name,
  view            = EXCLUDED.view,
  max_occupancy   = EXCLUDED.max_occupancy,
  is_pet_friendly = EXCLUDED.is_pet_friendly,
  pet_fee_cents   = EXCLUDED.pet_fee_cents,
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

-- Pending the owner's answer on which accessibility features each room has:
--
-- UPDATE rooms SET is_accessible = true,
--   accessibility_features = ARRAY['step_free_entry', 'ground_floor']
-- WHERE slug IN ('mrs-beals-suite', 'rose-chamber');
