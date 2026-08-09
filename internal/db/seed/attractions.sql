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
-- THE DESCRIPTIONS ARE NOT THE OWNER'S. Their site lists these as names and
-- distances alone, and a name plus a distance only helps somebody who already
-- knows what Chutters is — which is nobody the local-area page is written for.
-- Each sentence below states what the place is, checked against that place's
-- own site rather than recalled, and says nothing about whether it is worth
-- going to: a recommendation is the owner's to make and this is a label.
--
-- They are, along with the links, the lines most worth the owner's review, and
-- the console can now edit them. Nothing here describes the inn.
--
-- Re-runnable: the list is replaced wholesale.

BEGIN;

DELETE FROM local_attractions;

INSERT INTO local_attractions (name, distance, description, url, sort_order) VALUES
  ('Franconia Notch State Park', '15 minutes away',
   'The mountain pass at the heart of the White Mountains, with the Flume Gorge, the Basin and Echo Lake inside it.',
   'https://www.nhstateparks.org/find-parks-trails/franconia-notch-state-park', 0),

  ('Chutters', 'Walking distance',
   'A Main Street sweet shop, known for a candy counter long enough to have held a world record.',
   'https://www.chutters.com/', 1),

  ('Cannon Mountain', '18 minutes',
   'The state-run ski area in Franconia Notch. Its aerial tramway runs in summer too, up to the ridge and back.',
   'https://www.cannonmt.com/', 2),

  ('Schilling Beer Co.', 'Walking distance',
   'A brewery and wood-fired pizza taproom in a mill building over the Ammonoosuc River.',
   'https://schillingbeer.com/', 3),

  ('Mount Washington', '30 minutes away',
   'The highest peak in the Northeast, reached on foot, by the Cog Railway or by the Auto Road.',
   NULL, 4),

  ('Littleton Coin Company', '7 minutes away',
   'A coin and paper-money dealer that has been in Littleton since the 1940s, with a retail counter in town.',
   'https://www.littletoncoin.com/', 5),

  ('Santa''s Village', '31 minutes away',
   'A Christmas theme park in Jefferson, with rides, reindeer and a seasonal calendar.',
   'https://www.santasvillage.com/', 6),

  ('Bretton Woods', '31 minutes away',
   'New Hampshire''s largest ski area, on the slopes below Mount Washington.',
   'https://www.brettonwoods.com/', 7),

  ('Mount Eustis Ski Hill', '4 minutes away',
   'The town''s own ski hill, run by volunteers, with a rope tow and night skiing in season.',
   'https://www.mteustis.org/', 8),

  ('Littleton Historical Museum', 'Walking distance',
   'The town''s history, kept and shown by the Littleton Area Historical Society.',
   'https://littletonnhmuseum.com/', 9),

  ('Littleton Opera House', 'Walking distance',
   'The theatre upstairs in the town building, still in use for concerts, films and town events.',
   'https://littletonoperahouse.com/', 10);

COMMIT;
