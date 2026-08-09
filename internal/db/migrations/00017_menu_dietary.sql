-- +goose Up

-- What a dish suits: the three the kitchen is asked about most.
--
-- Three booleans rather than a `dietary text[]`, on purpose. These are not a
-- list the owner extends — they are three specific claims, each of which a
-- guest with coeliac disease or an allergy may act on, and a typed column is
-- what stops "vegen" becoming a fourth silent category that matches nothing and
-- shows no icon. Rooms use text[] for amenities because those genuinely are an
-- open list nobody's health depends on.
--
-- All three default false. A dish nobody has marked claims nothing, which is
-- the only safe default: the failure mode of a missing flag is a guest asking,
-- and the failure mode of a wrong one is a guest getting ill.
--
-- Note that vegan is NOT enforced to imply vegetarian. It is true of the food
-- and a CHECK would be defensible, but the console shows three independent
-- buttons and an owner who ticks only "vegan" has said something true; refusing
-- their save over a taxonomy they did not ask about is the wrong trade. The
-- rendered menu shows exactly the icons that were ticked.
ALTER TABLE menu_items
  ADD COLUMN is_gluten_free boolean NOT NULL DEFAULT false,
  ADD COLUMN is_vegan       boolean NOT NULL DEFAULT false,
  ADD COLUMN is_vegetarian   boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN menu_items.is_gluten_free IS
  'The kitchen states this dish is gluten free. False means unmarked, not "contains gluten".';

-- +goose Down
ALTER TABLE menu_items
  DROP COLUMN is_gluten_free,
  DROP COLUMN is_vegan,
  DROP COLUMN is_vegetarian;
