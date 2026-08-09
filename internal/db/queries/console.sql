-- The owner's console (build-order step 6).
--
-- Read models rather than a general query surface: each of these answers one
-- screen's question in one round trip, because the console is used one-handed
-- on a phone on a bad connection and a screen that needs five queries to draw
-- itself is a screen that half-draws.
--
-- Money is integer cents here as everywhere. The two rates cross the boundary
-- pre-scaled to hundred-thousandths so no numeric is ever decoded into a float.

-- ---------------------------------------------------------------------------
-- Today, and the booking list
-- ---------------------------------------------------------------------------

-- Everything happening at the inn on one day (decision: the console's first
-- screen).
--
-- One query and one bucket column rather than three round trips, because the
-- three questions share a row shape and the answer to all of them is "the
-- confirmed stays that touch this date". checkin <= on <= checkout catches all
-- three: an arrival is a stay whose check-in is today, a departure one whose
-- checkout is today, and everything between is in-house. They cannot overlap —
-- a booking with checkin = checkout is refused by a CHECK constraint.
--
-- Confirmed only. A pending booking is an unpaid hold in somebody's browser,
-- and putting it on the arrivals board would have the owner making up a bed for
-- a guest who never paid.
-- name: TodayBoard :many
SELECT
  b.id,
  b.code,
  b.checkin,
  b.checkout,
  b.guests,
  b.with_pet,
  b.total_cents,
  b.amount_paid_cents,
  b.balance_charge_at,
  b.balance_charge_failed_at,
  g.name  AS guest_name,
  g.email AS guest_email,
  g.phone AS guest_phone,
  coalesce((
    SELECT string_agg(r.name, ', ' ORDER BY r.sort_order)
    FROM booking_rooms br JOIN rooms r ON r.id = br.room_id
    WHERE br.booking_id = b.id
  ), '')::text AS room_names,
  CASE
    WHEN b.checkin  = sqlc.arg(on_date)::date THEN 'arrival'
    WHEN b.checkout = sqlc.arg(on_date)::date THEN 'departure'
    ELSE 'in_house'
  END::text AS bucket
FROM bookings b
JOIN guests g ON g.id = b.guest_id
WHERE b.status = 'confirmed'
  AND b.checkin  <= sqlc.arg(on_date)::date
  AND b.checkout >= sqlc.arg(on_date)::date
ORDER BY b.checkin, g.name;

-- The reservations list, with paid against total on every row.
--
-- Every filter is optional and expressed as "the parameter is absent OR it
-- matches", so one query serves the whole screen rather than a builder in Go
-- assembling SQL from strings. At this size the planner does not care and the
-- alternative is the shape injection bugs come from.
--
-- The date filter is an overlap, not a containment: an owner asking about next
-- week means every stay that touches next week, including the one that started
-- the Friday before.
-- name: SearchBookings :many
SELECT
  b.id,
  b.code,
  b.status,
  b.checkin,
  b.checkout,
  b.guests,
  b.with_pet,
  b.total_cents,
  b.amount_paid_cents,
  b.balance_due_cents,
  b.balance_charge_at,
  b.balance_charge_failed_at,
  b.balance_warned_at,
  b.created_at,
  g.id    AS guest_id,
  g.name  AS guest_name,
  g.email AS guest_email,
  g.phone AS guest_phone,
  coalesce((
    SELECT string_agg(r.name, ', ' ORDER BY r.sort_order)
    FROM booking_rooms br JOIN rooms r ON r.id = br.room_id
    WHERE br.booking_id = b.id
  ), '')::text AS room_names
FROM bookings b
JOIN guests g ON g.id = b.guest_id
WHERE (sqlc.narg(from_date)::date IS NULL OR b.checkout >  sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date   IS NULL OR b.checkin  <= sqlc.narg(to_date)::date)
  AND (sqlc.arg(status)::text = ''         OR b.status = sqlc.arg(status)::text)
  AND (sqlc.arg(room_id)::bigint = 0       OR EXISTS (
        SELECT 1 FROM booking_rooms br
        WHERE br.booking_id = b.id AND br.room_id = sqlc.arg(room_id)::bigint))
  -- A booking code is typed in uppercase and searched for whole; a name or an
  -- email is typed in fragments. Both go through the same box, so both are
  -- matched.
  AND (sqlc.arg(query)::text = ''
       OR b.code  = upper(sqlc.arg(query)::text)
       OR g.name  ILIKE '%' || sqlc.arg(query)::text || '%'
       OR g.email ILIKE '%' || sqlc.arg(query)::text || '%'
       OR g.phone ILIKE '%' || sqlc.arg(query)::text || '%')
  -- Failed charges first when asked for, because that is the one filter the
  -- owner reaches for to act rather than to look.
  AND (NOT sqlc.arg(only_flagged)::boolean OR b.balance_charge_failed_at IS NOT NULL)
ORDER BY b.checkin, b.code
LIMIT sqlc.arg(row_limit)::int;

-- The unmissable count for the console's frame: stays whose balance charge was
-- refused and which nobody has dealt with. Cheap enough to ask for on every
-- screen, which is the point — a flag nobody navigates to is a flag nobody
-- sees.
-- name: CountFlaggedBookings :one
SELECT count(*)::bigint
FROM bookings
WHERE status = 'confirmed' AND balance_charge_failed_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- The calendar, and blocking
-- ---------------------------------------------------------------------------

-- Every occupancy row touching a window, whatever put it there.
--
-- Bookings, holds and blocks come back together because that is how they are
-- stored and how the grid draws them: the owner needs to see that a room is
-- unsellable, and whether it is unsellable because somebody paid, because
-- somebody is halfway through a card form, or because they blocked it
-- themselves.
-- name: ListOccupancyBetween :many
SELECT
  o.id,
  o.room_id,
  lower(o.during)::date AS starts_on,
  upper(o.during)::date AS ends_on,
  o.kind,
  o.source,
  o.reason,
  o.expires_at,
  o.booking_id,
  coalesce(b.code, '')::text   AS booking_code,
  coalesce(b.status, '')::text AS booking_status,
  coalesce(g.name, '')::text   AS guest_name
FROM room_occupancy o
LEFT JOIN bookings b ON b.id = o.booking_id
LEFT JOIN guests   g ON g.id = b.guest_id
WHERE o.during && daterange(sqlc.arg(from_date)::date, sqlc.arg(to_date)::date, '[)')
ORDER BY o.room_id, o.during;

-- Removing a block, and only a block.
--
-- The kind is in the WHERE clause rather than checked in Go: this is the one
-- endpoint that deletes occupancy, and an id that happened to name a confirmed
-- booking's row would otherwise put a paid stay's room back on sale with the
-- guest still arriving. Zero rows affected is the refusal.
-- name: DeleteBlock :execrows
DELETE FROM room_occupancy WHERE id = sqlc.arg(id) AND kind = 'block';

-- ---------------------------------------------------------------------------
-- Rates (decision #21)
-- ---------------------------------------------------------------------------

-- name: ListRateSeasons :many
SELECT id, name, starts_on, ends_on, min_stay, priority
FROM rate_seasons
ORDER BY starts_on, priority DESC, id;

-- The whole grid in one read. Seven rooms times a handful of seasons is small
-- enough that filtering per season would be more round trips than rows.
-- name: ListSeasonPrices :many
SELECT season_id, room_id, price_cents
FROM rate_season_prices
ORDER BY season_id, room_id;

-- name: UpdateRateSeason :execrows
UPDATE rate_seasons SET
  name       = sqlc.arg(name),
  starts_on  = sqlc.arg(starts_on),
  ends_on    = sqlc.arg(ends_on),
  min_stay   = sqlc.narg(min_stay),
  priority   = sqlc.arg(priority),
  updated_at = now()
WHERE id = sqlc.arg(id);

-- name: DeleteRateSeason :execrows
DELETE FROM rate_seasons WHERE id = sqlc.arg(id);

-- Prices are replaced wholesale on save rather than merged, so a room the owner
-- removed from a season actually leaves it. The delete and the inserts run in
-- one transaction with the season's own update.
-- name: ClearSeasonPrices :exec
DELETE FROM rate_season_prices WHERE season_id = sqlc.arg(season_id);

-- What saving would change, summarised (decision #21's "diff before saving").
--
-- Called inside the transaction that has already applied the edit and is about
-- to roll back, so it compares the live calendar against what the edited
-- seasons would generate. Aggregated rather than listed: the owner needs to
-- know it is 142 nights and not 14,000, and a per-night list is a screen nobody
-- reads.
-- name: SummariseRateChanges :one
SELECT
  count(*)::bigint                                        AS nights,
  count(DISTINCT room_id)::bigint                         AS rooms,
  count(*) FILTER (WHERE old_price IS NULL)::bigint       AS nights_gaining_a_price,
  count(*) FILTER (WHERE new_price IS NULL)::bigint       AS nights_losing_their_price,
  min(night)::date                                        AS first_night,
  max(night)::date                                        AS last_night
FROM rate_calendar_changes(sqlc.arg(from_date), sqlc.arg(to_date));

-- How far forward the calendar is currently priced.
--
-- Shown in the rate editor because the failure mode of the monthly rebuild
-- stopping is silent: nothing breaks on the day it stops, the horizon just
-- creeps closer until a guest planning next autumn finds no price and the room
-- drops out of the search with no error anywhere. NULL means no calendar at
-- all, which is every room unsellable.
-- name: GetRateHorizon :one
SELECT max(date)::date AS horizon FROM rate_calendar;

-- Confirmed stays inside a window. Shown beside the diff to say plainly that
-- they are *not* affected: their nightly prices and tax rate were snapshotted
-- when they booked, and no rebuild can reach them.
-- name: CountConfirmedBookingsBetween :one
SELECT count(*)::bigint
FROM bookings
WHERE status = 'confirmed'
  AND checkin  <= sqlc.arg(to_date)::date
  AND checkout >  sqlc.arg(from_date)::date;

-- ---------------------------------------------------------------------------
-- Guests
-- ---------------------------------------------------------------------------

-- Guests, searchable the way the owner remembers people: by name, by email, by
-- phone, by the code on the paperwork, or by which room they had.
--
-- Counts are FILTERed to confirmed stays. A guest who booked and cancelled has
-- stayed zero times, and a "3 stays" that includes two abandoned holds is worse
-- than no number at all.
-- name: SearchGuests :many
SELECT
  g.id,
  g.name,
  g.email,
  g.phone,
  g.created_at,
  count(b.id) FILTER (WHERE b.status = 'confirmed')::bigint AS stays,
  coalesce(sum(b.amount_paid_cents) FILTER (WHERE b.status = 'confirmed'), 0)::bigint AS lifetime_cents,
  max(b.checkout) FILTER (WHERE b.status = 'confirmed')::date AS last_checkout,
  count(n.id)::bigint AS notes
FROM guests g
LEFT JOIN bookings    b ON b.guest_id = g.id
LEFT JOIN guest_notes n ON n.guest_id = g.id
WHERE (sqlc.arg(query)::text = ''
       OR g.name  ILIKE '%' || sqlc.arg(query)::text || '%'
       OR g.email ILIKE '%' || sqlc.arg(query)::text || '%'
       OR g.phone ILIKE '%' || sqlc.arg(query)::text || '%'
       OR EXISTS (SELECT 1 FROM bookings c
                  WHERE c.guest_id = g.id AND c.code = upper(sqlc.arg(query)::text)))
  AND (sqlc.arg(room_id)::bigint = 0
       OR EXISTS (SELECT 1 FROM bookings c
                  JOIN booking_rooms br ON br.booking_id = c.id
                  WHERE c.guest_id = g.id AND br.room_id = sqlc.arg(room_id)::bigint))
  AND (sqlc.narg(from_date)::date IS NULL
       OR EXISTS (SELECT 1 FROM bookings c
                  WHERE c.guest_id = g.id AND c.checkout > sqlc.narg(from_date)::date))
  AND (sqlc.narg(to_date)::date IS NULL
       OR EXISTS (SELECT 1 FROM bookings c
                  WHERE c.guest_id = g.id AND c.checkin <= sqlc.narg(to_date)::date))
GROUP BY g.id
ORDER BY max(b.checkout) DESC NULLS LAST, g.name
LIMIT sqlc.arg(row_limit)::int;

-- name: GetGuest :one
SELECT id, name, email, phone, created_at FROM guests WHERE id = sqlc.arg(id);

-- name: ListGuestBookings :many
SELECT
  b.id,
  b.code,
  b.status,
  b.checkin,
  b.checkout,
  b.guests,
  b.total_cents,
  b.amount_paid_cents,
  coalesce((
    SELECT string_agg(r.name, ', ' ORDER BY r.sort_order)
    FROM booking_rooms br JOIN rooms r ON r.id = br.room_id
    WHERE br.booking_id = b.id
  ), '')::text AS room_names
FROM bookings b
WHERE b.guest_id = sqlc.arg(guest_id)
ORDER BY b.checkin DESC;

-- The author is joined rather than stored as a name, so a user renaming
-- themselves does not leave old notes attributed to who they used to be. It is
-- a LEFT JOIN because the column is ON DELETE SET NULL: removing a user must
-- not delete what they wrote about a guest who is still coming back.
-- name: ListGuestNotes :many
SELECT
  n.id,
  n.body,
  n.created_at,
  coalesce(u.name, '')::text AS author
FROM guest_notes n
LEFT JOIN users u ON u.id = n.author_user_id
WHERE n.guest_id = sqlc.arg(guest_id)
ORDER BY n.created_at DESC, n.id DESC;

-- name: CreateGuestNote :one
INSERT INTO guest_notes (guest_id, author_user_id, body)
VALUES (sqlc.arg(guest_id), sqlc.narg(author_user_id), sqlc.arg(body))
RETURNING id, created_at;

-- The guest id is in the WHERE clause as well as the note id, so a mistyped
-- path cannot delete a note belonging to somebody else's record.
-- name: DeleteGuestNote :execrows
DELETE FROM guest_notes WHERE id = sqlc.arg(id) AND guest_id = sqlc.arg(guest_id);

-- ---------------------------------------------------------------------------
-- Rooms and their content
-- ---------------------------------------------------------------------------

-- name: ListRooms :many
SELECT
  id,
  slug,
  name,
  description,
  view,
  max_occupancy,
  amenities,
  is_accessible,
  accessibility_features,
  is_pet_friendly,
  pet_fee_cents,
  sort_order
FROM rooms
ORDER BY sort_order, id;

-- The cheapest night on sale per room, for the "from $x" on a rooms index.
--
-- Future nights only, and NULL for a room the calendar does not cover — which
-- is a room that cannot currently be sold at all, and the page says so rather
-- than showing a price it cannot honour.
-- name: ListLowestRates :many
SELECT room_id, min(price_cents)::int AS from_cents
FROM rate_calendar
WHERE date >= sqlc.arg(from_date)::date
GROUP BY room_id;

-- The accessibility honesty rule (decision #22) is a CHECK constraint on the
-- table, so this cannot set the flag without features and does not re-check it
-- here: enforcing it in whichever statement happens to write the row is exactly
-- the arrangement that constraint exists to replace.
-- name: UpdateRoom :execrows
UPDATE rooms SET
  name                   = sqlc.arg(name),
  description            = sqlc.arg(description),
  view                   = sqlc.narg(view),
  max_occupancy          = sqlc.arg(max_occupancy),
  amenities              = sqlc.arg(amenities),
  is_accessible          = sqlc.arg(is_accessible),
  accessibility_features = sqlc.arg(accessibility_features),
  is_pet_friendly        = sqlc.arg(is_pet_friendly),
  pet_fee_cents          = sqlc.arg(pet_fee_cents),
  sort_order             = sqlc.arg(sort_order),
  updated_at             = now()
WHERE id = sqlc.arg(id);

-- Photos are saved as a whole ordered list rather than one at a time, so the
-- order on screen and the order in the table cannot come apart: sort_order is
-- the array index at save time and never a number anybody types.
-- name: DeleteRoomPhotos :exec
DELETE FROM room_photos WHERE room_id = sqlc.arg(room_id);

-- name: CreateRoomPhoto :exec
INSERT INTO room_photos (room_id, path, alt_text, sort_order)
VALUES (sqlc.arg(room_id), sqlc.arg(path), sqlc.arg(alt_text), sqlc.arg(sort_order));

-- name: DeleteRoomBeds :exec
DELETE FROM room_beds WHERE room_id = sqlc.arg(room_id);

-- name: CreateRoomBed :exec
INSERT INTO room_beds (room_id, bed_type, count, location)
VALUES (sqlc.arg(room_id), sqlc.arg(bed_type), sqlc.arg(count), sqlc.arg(location));

-- ---------------------------------------------------------------------------
-- Settings
-- ---------------------------------------------------------------------------

-- Both rates arrive pre-scaled to hundred-thousandths and are divided back in
-- SQL, mirroring GetSettings exactly. The CHECK constraints on the table are
-- what refuse a nonsense value, so a bad save is a database error rather than a
-- silently accepted 850% tax rate.
-- name: UpdateSettings :exec
UPDATE settings SET
  default_min_stay       = sqlc.arg(default_min_stay),
  max_stay_nights        = sqlc.arg(max_stay_nights),
  tax_rate               = sqlc.arg(tax_rate_scaled)::bigint::numeric / 100000,
  refund_processing_rate = sqlc.arg(refund_processing_rate_scaled)::bigint::numeric / 100000,
  hold_ttl_minutes       = sqlc.arg(hold_ttl_minutes),
  payment_grace_minutes  = sqlc.arg(payment_grace_minutes),
  checkin_time           = sqlc.arg(checkin_time),
  checkout_time          = sqlc.arg(checkout_time),
  accessibility_notice   = sqlc.arg(accessibility_notice),
  updated_at             = now()
WHERE id;

-- ---------------------------------------------------------------------------
-- The menu (decision #12)
-- ---------------------------------------------------------------------------

-- name: ListMenuSections :many
SELECT id, name, description, sort_order FROM menu_sections ORDER BY sort_order, id;

-- Every item in one read, ordered so a caller can group them by walking the
-- list. Seven or eight sections of a dozen dishes is not worth a query each.
-- name: ListMenuItems :many
SELECT i.id, i.section_id, i.name, i.description, i.price_cents, i.is_available,
       i.is_gluten_free, i.is_vegan, i.is_vegetarian, i.sort_order
FROM menu_items i
JOIN menu_sections s ON s.id = i.section_id
ORDER BY s.sort_order, s.id, i.sort_order, i.id;

-- The menu saves as one document. Sections and items are reordered, renamed and
-- moved between courses in a single editing session, and reconciling that as a
-- stream of per-row edits would be a diff algorithm on the client with a
-- half-applied menu as its failure mode. Delete-and-rewrite inside one
-- transaction has neither.
-- name: DeleteAllMenuSections :exec
DELETE FROM menu_sections;

-- name: CreateMenuSection :one
INSERT INTO menu_sections (name, description, sort_order)
VALUES (sqlc.arg(name), sqlc.arg(description), sqlc.arg(sort_order))
RETURNING id;

-- name: CreateMenuItem :exec
INSERT INTO menu_items (
  section_id, name, description, price_cents, is_available,
  is_gluten_free, is_vegan, is_vegetarian, sort_order
)
VALUES (
  sqlc.arg(section_id),
  sqlc.arg(name),
  sqlc.arg(description),
  sqlc.arg(price_cents),
  sqlc.arg(is_available),
  sqlc.arg(is_gluten_free),
  sqlc.arg(is_vegan),
  sqlc.arg(is_vegetarian),
  sqlc.arg(sort_order)
);

-- ---------------------------------------------------------------------------
-- Events, their gallery, and the inquiries they produce
-- ---------------------------------------------------------------------------

-- name: ListEvents :many
SELECT id, title, happens_on, description, is_published, sort_order
FROM events
ORDER BY sort_order, happens_on NULLS LAST, id;

-- Published only, and nothing that has already happened. An events page listing
-- last spring's wedding is a page that looks abandoned.
-- name: ListPublishedEvents :many
SELECT id, title, happens_on, description, sort_order
FROM events
WHERE is_published
  AND (happens_on IS NULL OR happens_on >= sqlc.arg(on_date)::date)
ORDER BY happens_on NULLS LAST, sort_order, id;

-- name: ListEventPhotos :many
SELECT id, event_id, path, alt_text, sort_order
FROM event_photos
ORDER BY event_id, sort_order, id;

-- name: DeleteAllEvents :exec
DELETE FROM events;

-- name: CreateEvent :one
INSERT INTO events (title, happens_on, description, is_published, sort_order)
VALUES (
  sqlc.arg(title),
  sqlc.narg(happens_on),
  sqlc.arg(description),
  sqlc.arg(is_published),
  sqlc.arg(sort_order)
)
RETURNING id;

-- name: CreateEventPhoto :exec
INSERT INTO event_photos (event_id, path, alt_text, sort_order)
VALUES (sqlc.arg(event_id), sqlc.arg(path), sqlc.arg(alt_text), sqlc.arg(sort_order));

-- The one write on this whole page that an anonymous visitor performs. It
-- inserts and nothing else — no email, no job, no side effect — because
-- decision #11 puts event booking out of scope and this is a message, not a
-- transaction.
-- name: CreateEventInquiry :one
INSERT INTO event_inquiries (name, email, phone, event_date, party_size, message, kind)
VALUES (
  sqlc.arg(name),
  sqlc.arg(email),
  sqlc.arg(phone),
  sqlc.narg(event_date),
  sqlc.narg(party_size),
  sqlc.arg(message),
  sqlc.arg(kind)
)
RETURNING id, created_at;

-- Both filters are optional and an empty string means "all", which is how the
-- console shows one inbox with the events and the contact messages together.
-- name: ListEventInquiries :many
SELECT id, name, email, phone, event_date, party_size, message, status, kind, created_at
FROM event_inquiries
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: CountNewInquiries :one
SELECT count(*)::bigint FROM event_inquiries WHERE status = 'new';

-- name: SetInquiryStatus :execrows
UPDATE event_inquiries
SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id);

-- ---------------------------------------------------------------------------
-- The prose on the public pages
-- ---------------------------------------------------------------------------

-- name: GetPageCopy :one
SELECT slug, heading, body, updated_at FROM page_copy WHERE slug = sqlc.arg(slug);

-- name: ListPageCopy :many
SELECT slug, heading, body, updated_at FROM page_copy ORDER BY slug;

-- name: UpsertPageCopy :exec
INSERT INTO page_copy (slug, heading, body)
VALUES (sqlc.arg(slug), sqlc.arg(heading), sqlc.arg(body))
ON CONFLICT (slug) DO UPDATE
SET heading    = excluded.heading,
    body       = excluded.body,
    updated_at = now();

-- Emptying a page is a delete, the same way resetting an email template is: no
-- row is the absence of copy, and a row holding two empty strings would be a
-- second way to say the same thing.
-- name: DeletePageCopy :execrows
DELETE FROM page_copy WHERE slug = sqlc.arg(slug);

-- ---------------------------------------------------------------------------
-- The photographs on the public pages
-- ---------------------------------------------------------------------------
--
-- Saved as a whole document, the same way a room's photos and the menu are: the
-- console sends the list it wants the page to have and the transaction replaces
-- what is there. Reconciling per-row would need a diff on the client whose
-- failure mode is half a gallery on the public site.

-- name: ListPagePhotos :many
SELECT slug, path, alt_text, sort_order FROM page_photos
ORDER BY slug, sort_order, id;

-- name: ListPagePhotosFor :many
SELECT path, alt_text FROM page_photos
WHERE slug = sqlc.arg(slug)
ORDER BY sort_order, id;

-- name: DeletePagePhotos :exec
DELETE FROM page_photos WHERE slug = sqlc.arg(slug);

-- name: CreatePagePhoto :exec
INSERT INTO page_photos (slug, path, alt_text, sort_order)
VALUES (sqlc.arg(slug), sqlc.arg(path), sqlc.arg(alt_text), sqlc.arg(sort_order));

-- ---------------------------------------------------------------------------
-- What is near the inn
-- ---------------------------------------------------------------------------
--
-- Saved as a whole document, like the menu and the galleries.

-- name: ListLocalAttractions :many
SELECT name, distance, description, url FROM local_attractions ORDER BY sort_order, id;

-- name: DeleteLocalAttractions :exec
DELETE FROM local_attractions;

-- name: CreateLocalAttraction :exec
INSERT INTO local_attractions (name, distance, description, url, sort_order)
VALUES (sqlc.arg(name), sqlc.arg(distance), sqlc.arg(description), sqlc.arg(url), sqlc.arg(sort_order));
