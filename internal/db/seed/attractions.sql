-- The nearby-highlights list, from the inn's current site.
--
-- The names and the distances are the owner's, in their order and their
-- wording. The links are NOT on their site — they are the official site of each
-- place, looked up and checked rather than guessed, because a wrong link on a
-- recommendation is worse than no link at all.
--
-- Mount Washington deliberately has none. "Mount Washington, 30 minutes away"
-- could be the Cog Railway, the Auto Road or the state park at the summit, and
-- which one the owner means is not something to decide for them. The row
-- renders as plain text until they say. That is what a NULL url is for.
--
-- Re-runnable: the list is replaced wholesale.

BEGIN;

DELETE FROM local_attractions;

INSERT INTO local_attractions (name, distance, url, sort_order) VALUES
  ('Franconia Notch State Park', '15 minutes away', 'https://www.nhstateparks.org/find-parks-trails/franconia-notch-state-park', 0),
  ('Chutters',                   'Walking distance', 'https://www.chutters.com/',        1),
  ('Cannon Mountain',            '18 minutes',       'https://www.cannonmt.com/',        2),
  ('Schilling Beer Co.',         'Walking distance', 'https://schillingbeer.com/',       3),
  ('Mount Washington',           '30 minutes away',  NULL,                               4),
  ('Littleton Coin Company',     '7 minutes away',   'https://www.littletoncoin.com/',   5),
  ('Santa''s Village',           '31 minutes away',  'https://www.santasvillage.com/',   6),
  ('Bretton Woods',              '31 minutes away',  'https://www.brettonwoods.com/',    7),
  ('Mount Eustis Ski Hill',      '4 minutes away',   'https://www.mteustis.org/',        8),
  ('Littleton Historical Museum', 'Walking distance', 'https://littletonnhmuseum.com/',  9),
  ('Littleton Opera House',      'Walking distance', 'https://littletonoperahouse.com/', 10);

COMMIT;
