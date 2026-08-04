package availability

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
)

// These tests work in a stretch of calendar no other package writes to, so a
// booking committed elsewhere in the suite cannot change what they see.
const (
	windowStart = 120
	windowEnd   = 150
)

func window() CalendarRequest {
	return CalendarRequest{From: day(windowStart), To: day(windowEnd), Guests: 1}
}

func spans(t *testing.T, ctx context.Context, q *db.Queries, req CalendarRequest) Calendar {
	t.Helper()
	cal, err := Spans(ctx, q, req)
	if err != nil {
		t.Fatalf("building the calendar: %v", err)
	}
	return cal
}

func roomSpans(t *testing.T, cal Calendar, slug string) []Span {
	t.Helper()
	for _, room := range cal.Rooms {
		if room.Slug == slug {
			return room.Spans
		}
	}
	t.Fatalf("%q is not in the calendar", slug)
	return nil
}

// occupy takes a room off sale, the way a booking, hold or owner's block does.
func occupy(t *testing.T, ctx context.Context, q *db.Queries, slug string, from, to int) {
	t.Helper()
	id, err := q.GetRoomIDBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("looking up %q: %v", slug, err)
	}
	if _, err := occupancy.Create(ctx, q, db.CreateOccupancyParams{
		RoomID:   id,
		Checkin:  pgtype.Date{Time: day(from), Valid: true},
		Checkout: pgtype.Date{Time: day(to), Valid: true},
		Kind:     "booking",
		Source:   "direct",
	}); err != nil {
		t.Fatalf("occupying %s: %v", slug, err)
	}
}

// selectable is the rule the date picker applies, and the reason the API sends
// spans rather than a set of free nights: a stay is offerable only if one room
// covers all of it and the minimum stay at the arrival night is met.
func selectable(cal Calendar, checkin, checkout time.Time) bool {
	nights := civil.Nights(checkin, checkout)

	for _, room := range cal.Rooms {
		for _, span := range room.Spans {
			start, err := time.Parse(time.DateOnly, span.Start)
			if err != nil {
				continue
			}
			offset := civil.Nights(start, checkin)
			if offset < 0 || offset >= len(span.MinStays) {
				continue
			}
			if nights < span.MinStays[offset] {
				continue
			}
			if offset+nights <= len(span.MinStays) {
				return true
			}
		}
	}
	return false
}

func TestAnEmptyCalendarIsOneSpanPerRoom(t *testing.T) {
	ctx, q := setup(t)

	cal := spans(t, ctx, q, window())

	if len(cal.Rooms) != 7 {
		t.Fatalf("got %d rooms in the calendar, want 7", len(cal.Rooms))
	}
	for _, room := range cal.Rooms {
		if len(room.Spans) != 1 {
			t.Errorf("%s has %d spans, want 1 unbroken run", room.Slug, len(room.Spans))
			continue
		}
		if got, want := len(room.Spans[0].MinStays), windowEnd-windowStart; got != want {
			t.Errorf("%s covers %d nights, want %d", room.Slug, got, want)
		}
		if room.Spans[0].Start != cal.From {
			t.Errorf("%s starts at %s, want %s", room.Slug, room.Spans[0].Start, cal.From)
		}
	}
}

// The seeded default is two nights, carried per night so a season that raises
// it only affects the nights it covers.
func TestSpansCarryTheMinimumStayPerNight(t *testing.T) {
	ctx, q := setup(t)

	cal := spans(t, ctx, q, window())

	for _, min := range roomSpans(t, cal, "flume")[0].MinStays {
		if min != 2 {
			t.Fatalf("minimum stay %d, want the seeded default of 2", min)
			return
		}
	}
}

// A stay in the middle of the window splits that room's run in two, and the
// checkout day starts the second run rather than being lost to it.
func TestOccupancySplitsARun(t *testing.T) {
	ctx, q := setup(t)
	occupy(t, ctx, q, "flume", windowStart+10, windowStart+13)

	got := roomSpans(t, spans(t, ctx, q, window()), "flume")

	if len(got) != 2 {
		t.Fatalf("got %d spans, want the run split in two: %+v", len(got), got)
	}
	if n := len(got[0].MinStays); n != 10 {
		t.Errorf("the first run holds %d nights, want 10", n)
	}
	if want := dayString(windowStart + 13); got[1].Start != want {
		t.Errorf("the second run starts %s, want %s — the checkout day is sellable",
			got[1].Start, want)
	}
}

// A fully booked room has nothing to offer and says so, rather than appearing
// with an empty span the picker would have to interpret.
func TestAFullyOccupiedRoomDropsOut(t *testing.T) {
	ctx, q := setup(t)
	occupy(t, ctx, q, "flume", windowStart, windowEnd)

	cal := spans(t, ctx, q, window())

	for _, room := range cal.Rooms {
		if room.Slug == "flume" {
			t.Errorf("a fully booked room is still in the calendar: %+v", room.Spans)
		}
	}
	if len(cal.Rooms) != 6 {
		t.Errorf("got %d rooms, want the other 6", len(cal.Rooms))
	}
}

// Decision #14, stated as the thing that goes wrong without it.
//
// Every room is free somewhere in the middle of the window, so a picker built
// on the union of free nights would grey nothing out and happily let a guest
// select the whole span — for which there is nothing to sell, because no single
// room covers it.
func TestNoRoomCoveringTheWholeSpanIsNotSelectable(t *testing.T) {
	ctx, q := setup(t)

	const (
		split = windowStart + 10
		last  = windowStart + 20
	)

	// Six rooms are free only after the split; the seventh only before it.
	for _, slug := range []string{"garden-suite", "flume", "rose-chamber",
		"washington-room", "blue-room", "back-lavender"} {
		occupy(t, ctx, q, slug, windowStart, split)
	}
	occupy(t, ctx, q, "mrs-beals-suite", split, last)

	cal := spans(t, ctx, q, window())

	// The union of free nights covers the whole span with no gap...
	free := map[string]bool{}
	for _, room := range cal.Rooms {
		for _, span := range room.Spans {
			start, _ := time.Parse(time.DateOnly, span.Start)
			for i := range span.MinStays {
				free[civil.AddDays(start, i).Format(time.DateOnly)] = true
			}
		}
	}
	for i := windowStart; i < last; i++ {
		if !free[dayString(i)] {
			t.Fatalf("%s is occupied in every room; the test no longer sets up the trap", dayString(i))
		}
	}

	// ...but no room covers a stay that crosses the split, so the picker must
	// not offer one.
	crossing := selectable(cal, day(split-2), day(split+2))
	if crossing {
		t.Error("a span no single room covers was offered as selectable")
	}

	// And the search agrees, which is the only opinion that counts.
	res := search(t, ctx, q, Request{Checkin: day(split - 2), Checkout: day(split + 2), Guests: 1})
	if len(res.Rooms) != 0 {
		t.Errorf("the search sold %v for a span the calendar says is impossible", slugs(res))
	}
}

// The guarantee the picker rests on: what it greys out and what the search
// sells are the same set, over every span in the window.
func TestTheCalendarAndTheSearchAgree(t *testing.T) {
	ctx, q := setup(t)

	// A staggered mess, so the two are compared against something with real
	// structure rather than an empty calendar.
	occupy(t, ctx, q, "flume", windowStart+2, windowStart+6)
	occupy(t, ctx, q, "rose-chamber", windowStart+4, windowStart+9)
	occupy(t, ctx, q, "blue-room", windowStart+1, windowStart+3)
	occupy(t, ctx, q, "washington-room", windowStart+5, windowStart+12)
	occupy(t, ctx, q, "garden-suite", windowStart, windowStart+8)
	occupy(t, ctx, q, "back-lavender", windowStart+7, windowStart+10)
	occupy(t, ctx, q, "mrs-beals-suite", windowStart+3, windowStart+11)

	cal := spans(t, ctx, q, window())

	var yes, no int
	for checkin := windowStart; checkin < windowStart+14; checkin++ {
		for nights := 1; nights <= 6; nights++ {
			checkout := checkin + nights

			offered := selectable(cal, day(checkin), day(checkout))
			sellable := len(search(t, ctx, q, Request{
				Checkin: day(checkin), Checkout: day(checkout), Guests: 1,
			}).Rooms) > 0

			if offered != sellable {
				t.Errorf("%s to %s: picker offers it = %v, search sells it = %v",
					dayString(checkin), dayString(checkout), offered, sellable)
			}
			if offered {
				yes++
			} else {
				no++
			}
		}
	}

	// Agreement is cheap to achieve by saying no to everything, so the mix
	// matters as much as the match.
	if yes == 0 || no == 0 {
		t.Errorf("%d spans offered and %d refused; the fixture stopped discriminating", yes, no)
	}
}

// The filters are part of the calendar for the same reason they are part of the
// search: dates only two-person rooms are free on are not dates a party of four
// can pick.
func TestCalendarHonoursCapacityAndPets(t *testing.T) {
	ctx, q := setup(t)

	req := window()
	req.Guests = 4
	cal := spans(t, ctx, q, req)
	if len(cal.Rooms) != 1 || cal.Rooms[0].Slug != "garden-suite" {
		t.Errorf("a party of four sees %d rooms, want just the garden suite", len(cal.Rooms))
	}

	req = window()
	req.WithPet = true
	cal = spans(t, ctx, q, req)
	if len(cal.Rooms) != 1 || cal.Rooms[0].Slug != "back-lavender" {
		t.Errorf("a guest with a pet sees %d rooms, want just back lavender", len(cal.Rooms))
	}
}

// A picker showing the current month asks about days that have already gone,
// and about dates past the point the calendar has been generated to. Neither is
// an error; both are clamped to what can honestly be described.
func TestTheWindowIsClamped(t *testing.T) {
	ctx, q := setup(t)

	cal := spans(t, ctx, q, CalendarRequest{From: day(-30), To: day(10), Guests: 1})
	if want := civil.Today().Format(time.DateOnly); cal.From != want {
		t.Errorf("window starts %s, want today (%s)", cal.From, want)
	}

	cal = spans(t, ctx, q, CalendarRequest{From: day(10), To: day(365 * 5), Guests: 1})
	if cal.To >= dayString(365*5) {
		t.Errorf("window runs to %s, want it clamped to the rate horizon", cal.To)
	}
	for _, room := range cal.Rooms {
		for _, span := range room.Spans {
			if span.Start > cal.To {
				t.Errorf("%s has a span starting %s, past the window end %s",
					room.Slug, span.Start, cal.To)
			}
		}
	}

	// An inverted window is a bug in the caller, not a reason to fail: it comes
	// back as the smallest thing that can be described instead.
	cal = spans(t, ctx, q, CalendarRequest{From: day(20), To: day(10), Guests: 1})
	if cal.To <= cal.From {
		t.Errorf("window %s to %s is still inverted", cal.From, cal.To)
	}
}

func dayString(offset int) string { return day(offset).Format(time.DateOnly) }
