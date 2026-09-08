package availability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
	"bealhouse/internal/testdb"
)

// Tests run inside a rolled-back transaction and use dates a month out, which
// keeps them inside the seeded rate horizon without depending on a fixed
// calendar year.
//
// They also take testdb.Exclusive, and that is about locks rather than rows.
// These fixtures claim **several rooms inside one transaction**, and
// occupancy.Create takes a per-room advisory lock held to the end of it. Two
// packages doing that in different room orders is an AB-BA deadlock, which
// Postgres breaks after deadlock_timeout — so the suite failed roughly one full
// run in four, in whichever package lost, with an error about an aborted
// transaction rather than about a deadlock. Nothing in the application does
// this: a booking claims exactly one room, which is what makes that advisory
// lock sufficient there. See CLAUDE.md.
func setup(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()
	pool := testdb.Connect(t)
	testdb.Exclusive(t, pool)
	tx := testdb.Tx(t, pool)
	return context.Background(), db.New(tx)
}

func day(offset int) time.Time { return civil.AddDays(civil.Today(), offset) }

func search(t *testing.T, ctx context.Context, q *db.Queries, req Request) Result {
	t.Helper()
	res, err := Search(ctx, q, req)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return res
}

func slugs(res Result) []string {
	out := make([]string, len(res.Rooms))
	for i, r := range res.Rooms {
		out[i] = r.Slug
	}
	return out
}

func roomBySlug(t *testing.T, res Result, slug string) Room {
	t.Helper()
	for _, r := range res.Rooms {
		if r.Slug == slug {
			return r
		}
	}
	t.Fatalf("%q not in results %v", slug, slugs(res))
	return Room{}
}

func TestEmptyCalendarOffersEveryRoom(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 1})

	if len(res.Rooms) != 7 {
		t.Errorf("got %d rooms, want all 7: %v", len(res.Rooms), slugs(res))
	}
	if res.Nights != 2 {
		t.Errorf("nights = %d, want 2", res.Nights)
	}
	if res.AccessibilityNotice == "" {
		t.Error("the stairs notice is missing; a guest with mobility needs would not be told")
	}
}

// Guest count filters capacity. It is never a price input.
func TestCapacityFilter(t *testing.T) {
	ctx, q := setup(t)

	tests := []struct {
		guests int
		want   []string
	}{
		{1, []string{"mrs-beals-suite", "garden-suite", "flume", "rose-chamber", "washington-room", "blue-room", "back-lavender"}},
		{2, []string{"mrs-beals-suite", "garden-suite", "flume", "rose-chamber", "washington-room", "blue-room", "back-lavender"}},
		{3, []string{"mrs-beals-suite", "garden-suite", "back-lavender"}},
		{4, []string{"garden-suite"}},
		{5, nil},
	}

	for _, tt := range tests {
		res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: tt.guests})
		got := slugs(res)

		if len(got) != len(tt.want) {
			t.Errorf("%d guests: got %v, want %v", tt.guests, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%d guests: got %v, want %v", tt.guests, got, tt.want)
				break
			}
		}
	}
}

// Back Lavender is an ordinary room now that the pet fee is withdrawn: it
// sleeps three, it is offered to everyone, and nothing is added to its quote.
func TestBackLavenderIsAnOrdinaryRoom(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2})
	room := roomBySlug(t, res, "back-lavender")

	if room.Quote.TaxableCents != room.Quote.RoomSubtotalCents {
		t.Errorf("taxable %d != room subtotal %d: something besides the room is being charged",
			room.Quote.TaxableCents, room.Quote.RoomSubtotalCents)
	}
}

func TestPricingMatchesTheRateCalendar(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2})
	rose := roomBySlug(t, res, "rose-chamber")

	if len(rose.NightlyCents) != 2 {
		t.Fatalf("got %d nightly prices, want 2", len(rose.NightlyCents))
	}

	// Night by night against the calendar itself rather than a constant: the
	// seed carries the inn's real seasons, and which one a test window lands in
	// depends on the day the test runs.
	roomID, err := q.GetRoomIDBySlug(ctx, "rose-chamber")
	if err != nil {
		t.Fatalf("looking up rose-chamber: %v", err)
	}
	var subtotal int64
	for i, cents := range rose.NightlyCents {
		night := day(30 + i)
		entry, err := q.GetRateCalendarEntry(ctx, db.GetRateCalendarEntryParams{
			RoomID: roomID,
			Date:   pgtype.Date{Time: night, Valid: true},
		})
		if err != nil {
			t.Fatalf("calendar has no entry for %s: %v", night.Format(time.DateOnly), err)
		}
		if cents != int64(entry.PriceCents) {
			t.Errorf("night %d is %d cents, calendar says %d", i, cents, entry.PriceCents)
		}
		subtotal += cents
	}
	if rose.Quote.RoomSubtotalCents != subtotal {
		t.Errorf("room subtotal %d, want the nights' sum %d", rose.Quote.RoomSubtotalCents, subtotal)
	}
	if rose.Quote.DepositCents+rose.Quote.BalanceCents != rose.Quote.TotalCents {
		t.Errorf("deposit %d + balance %d != total %d",
			rose.Quote.DepositCents, rose.Quote.BalanceCents, rose.Quote.TotalCents)
	}
}

// Occupancy of any kind takes a room out of results.
func TestOccupiedRoomsAreExcluded(t *testing.T) {
	ctx, q := setup(t)

	flume, err := q.GetRoomIDBySlug(ctx, "flume")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := occupancy.Create(ctx, q, db.CreateOccupancyParams{
		RoomID:   flume,
		Checkin:  pgtype.Date{Time: day(31), Valid: true},
		Checkout: pgtype.Date{Time: day(33), Valid: true},
		Kind:     "booking",
		Source:   "direct",
	}); err != nil {
		t.Fatalf("seeding a booking: %v", err)
	}

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 1})
	if len(res.Rooms) != 6 {
		t.Errorf("got %d rooms, want 6: %v", len(res.Rooms), slugs(res))
	}
	for _, r := range res.Rooms {
		if r.Slug == "flume" {
			t.Error("an occupied room was offered for sale")
		}
	}

	// The same room is still sellable either side of the stay.
	res = search(t, ctx, q, Request{Checkin: day(33), Checkout: day(35), Guests: 1})
	roomBySlug(t, res, "flume")
}

// The global two-night minimum has to hold on the server, not just in the date
// picker.
func TestSingleNightStaysAreRejected(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(31), Guests: 1})
	if len(res.Rooms) != 0 {
		t.Errorf("a one-night stay returned %v; the minimum is two", slugs(res))
	}
}

// Decision #27: a stay longer than a month is a conversation with the owner,
// not something the engine sells. The boundary is settings-driven, so this
// reads it rather than hardcoding 31.
func TestStaysLongerThanTheMaximumAreRefused(t *testing.T) {
	ctx, q := setup(t)

	settings, err := q.GetSettings(ctx)
	if err != nil {
		t.Fatalf("loading settings: %v", err)
	}
	max := int(settings.MaxStayNights)

	// Exactly the maximum is still on sale.
	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(30 + max), Guests: 1})
	if len(res.Rooms) == 0 {
		t.Errorf("a %d-night stay returned nothing; the maximum itself must still be bookable", max)
	}
	if res.Nights != max {
		t.Errorf("nights = %d, want %d", res.Nights, max)
	}

	// One night more is not, and says so specifically rather than coming back
	// as an empty result the guest cannot interpret.
	_, err = Search(ctx, q, Request{Checkin: day(30), Checkout: day(30 + max + 1), Guests: 1})
	if !errors.Is(err, ErrStayTooLong) {
		t.Errorf("a %d-night stay got %v, want ErrStayTooLong", max+1, err)
	}
}

// Beyond the generated horizon there are no rates, so nothing is sellable —
// rather than being sold at a guessed price.
func TestDatesBeyondTheRateHorizonReturnNothing(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(365 * 3), Checkout: day(365*3 + 2), Guests: 1})
	if len(res.Rooms) != 0 {
		t.Errorf("got %v for unpriced dates, want nothing", slugs(res))
	}
}

func TestValidation(t *testing.T) {
	ctx, q := setup(t)

	tests := []struct {
		name string
		req  Request
		want error
	}{
		{"checkout before checkin", Request{Checkin: day(30), Checkout: day(28), Guests: 1}, ErrCheckoutNotAfterCheckin},
		{"same day", Request{Checkin: day(30), Checkout: day(30), Guests: 1}, ErrCheckoutNotAfterCheckin},
		{"checkin in the past", Request{Checkin: day(-1), Checkout: day(2), Guests: 1}, ErrCheckinInPast},
		{"no guests", Request{Checkin: day(30), Checkout: day(32), Guests: 0}, ErrGuestsOutOfRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Search(ctx, q, tt.req); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// Until the owner uploads real photos, every room still has something to
// render, so the layout can be judged before any content exists.
func TestRoomsWithoutPhotosFallBackToAPlaceholder(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 1})

	for _, room := range res.Rooms {
		if len(room.Photos) > 0 {
			continue // a real upload wins; nothing to check
		}
		want := "/placeholders/" + room.Slug + ".svg"
		if room.PlaceholderPhotoURL != want {
			t.Errorf("%s placeholder is %q, want %q", room.Slug, room.PlaceholderPhotoURL, want)
		}
	}
}

// A stored path is already the URL, and this used to prepend the prefix again.
//
// `media.Save` returns "/media/<name>" and the console writes exactly that into
// the column, so the "/media/" this once added made every URL "/media//media/…"
// — a broken image on the search results and the room page, which are the two
// pages a guest books from. It went unnoticed because no photographs have been
// uploaded yet and nothing asserted the shape. Both halves are checked here:
// the URL, and that the srcset the page will use is derived from it.
func TestAPhotoURLIsTheStoredPathUntouched(t *testing.T) {
	ctx, q := setup(t)

	const stored = "/media/0123456789abcdef0123456789abcdef-w2400.jpg"
	id, err := q.GetRoomIDBySlug(ctx, "blue-room")
	if err != nil {
		t.Fatalf("looking up the room: %v", err)
	}
	// Whatever the developer's database holds, this room has exactly the one
	// photograph below. The seed carries real photographs now, and a test that
	// counted on a room having none was really asserting that nobody had
	// uploaded any yet. Inside the rolled-back transaction, so the dev database
	// keeps them.
	if err := q.DeleteRoomPhotos(ctx, id); err != nil {
		t.Fatalf("clearing the room's photos: %v", err)
	}
	if err := q.CreateRoomPhoto(ctx, db.CreateRoomPhotoParams{
		RoomID: id, Path: stored, AltText: "The bay window", SortOrder: 0,
	}); err != nil {
		t.Fatalf("adding a photo: %v", err)
	}

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 1})
	room := roomBySlug(t, res, "blue-room")

	if len(room.Photos) != 1 {
		t.Fatalf("got %d photos, want 1", len(room.Photos))
	}
	if got := room.Photos[0].URL; got != stored {
		t.Errorf("photo URL is %q, want the stored path %q unchanged", got, stored)
	}
	// The ladder rides along, and every entry in it addresses the same
	// directory rather than a doubled one.
	if !strings.Contains(room.Photos[0].JPEG, "-w960.jpg 960w") {
		t.Errorf("srcset is %q, want the 960 rung in it", room.Photos[0].JPEG)
	}
	if strings.Contains(room.Photos[0].JPEG, "/media//") {
		t.Errorf("srcset carries a doubled prefix: %q", room.Photos[0].JPEG)
	}
}

// Beds come back with the result so a result card can describe the room.
func TestResultsCarryBeds(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 3})
	mrsBeals := roomBySlug(t, res, "mrs-beals-suite")

	if len(mrsBeals.Beds) != 2 {
		t.Fatalf("got %d bed entries, want 2", len(mrsBeals.Beds))
	}
	var foundDaybed bool
	for _, b := range mrsBeals.Beds {
		if b.Type == "daybed" && b.Location == "sitting room" {
			foundDaybed = true
		}
	}
	if !foundDaybed {
		t.Errorf("the sitting-room daybed is missing from %+v", mrsBeals.Beds)
	}
}

// A room with nothing uploaded must come back as an empty list, not null.
//
// This is a contract, not a nicety: the results page reads photos[0] to pick a
// hero image, and null there is a crash rather than a fallback. Every room is
// in that state until the owner uploads anything.
func TestEmptyListsSerialiseAsLists(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 1})

	for _, room := range res.Rooms {
		if room.Photos == nil {
			t.Errorf("%s has nil photos; the UI reads photos[0]", room.Slug)
		}
		if room.Beds == nil {
			t.Errorf("%s has nil beds", room.Slug)
		}
		if room.Amenities == nil {
			t.Errorf("%s has nil amenities", room.Slug)
		}
	}

	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encoding the result: %v", err)
	}
	if bytes.Contains(encoded, []byte(`null`)) {
		t.Errorf("a null reached the wire: %s", encoded)
	}
}
