-- A MOCK MENU. Every dish here is invented and says so on its face.
--
-- This is the one file in seed/ that is not the owner's content and does not
-- pretend to be. The names are "Food item #1", the ingredients are "Ingredient
-- one, ingredient two", and that is the point: it exists so the restaurant page
-- can be laid out and the dietary icons can be seen working, and if it ever
-- reached the live site it would be unmistakable within one second rather than
-- plausible for a month. The same reasoning as the PLACEHOLDER descriptions in
-- rooms.sql.
--
-- DELETE THIS FILE, and the rows it writes, the moment the kitchen has a real
-- menu. `DELETE FROM menu_sections;` is the whole of the undo — items cascade.
--
-- Five dishes: two starters, two mains, one dessert. It still covers the two
-- cases a layout gets wrong — an item with no price (zero cents), which must
-- render nothing rather than "$0.00", and a spread of the dietary flags
-- including a dish with none, so the icons and the key can both be checked.
--
-- The "off tonight" case is no longer represented, since showing it would mean
-- a sixth row the public menu hides. Untick "On tonight" on any dish in the
-- console to see it.
--
-- Re-runnable: sections are replaced wholesale and items cascade with them.

BEGIN;

DELETE FROM menu_sections;

WITH s AS (
  INSERT INTO menu_sections (name, description, sort_order) VALUES
    ('Starters', 'Mock course — placeholder content, not the kitchen''s menu.', 0),
    ('Mains',    'Mock course — placeholder content, not the kitchen''s menu.', 1),
    ('Desserts', 'Mock course — placeholder content, not the kitchen''s menu.', 2)
  RETURNING id, name
)
INSERT INTO menu_items (
  section_id, name, description, price_cents,
  is_available, is_gluten_free, is_vegan, is_vegetarian, sort_order
)
SELECT s.id, i.name, i.description, i.price_cents,
       i.is_available, i.gf, i.vegan, i.veg, i.sort_order
FROM s JOIN (VALUES
  -- section,     name,            description,                                              cents, avail,  gf,    vegan, veg,   order
  ('Starters', 'Food item #1', 'Ingredient one, ingredient two, ingredient three',            1200, true,  true,  true,  true,  0),
  ('Starters', 'Food item #2', 'Ingredient four, ingredient five, a sauce',                   1400, true,  false, false, true,  1),

  ('Mains',    'Food item #3', 'Ingredient six, ingredient seven, ingredient eight, a grain', 2600, true,  false, false, false, 0),
  -- No price of its own: the page must print nothing here, not "$0.00".
  ('Mains',    'Food item #4', 'Whatever the market had that morning — ask us',                  0, true,  true,  false, true,  1),

  ('Desserts', 'Food item #5', 'Ingredient nine, ingredient ten, cream',                      1100, true,  false, false, true,  0)
) AS i(section, name, description, price_cents, is_available, gf, vegan, veg, sort_order)
  ON i.section = s.name;

COMMIT;
