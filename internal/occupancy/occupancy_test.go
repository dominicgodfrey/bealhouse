package occupancy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

func setup(t *testing.T) (context.Context, *db.Queries, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.Connect(t)
	// These tests commit real rows and empty the table to start clean, so they
	// have to take turns with the other packages that do the same.
	testdb.Exclusive(t, pool)
	testdb.ResetOccupancy(t, pool)
	return context.Background(), db.New(pool), pool
}

func mustDate(t *testing.T, s string) pgtype.Date {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return pgtype.Date{Time: d, Valid: true}
}

func roomID(t *testing.T, ctx context.Context, q *db.Queries, slug string) int64 {
	t.Helper()
	id, err := q.GetRoomIDBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("looking up room %q (is the seed loaded?): %v", slug, err)
	}
	return id
}

// book writes a confirmed booking through the same entry point the API uses,
// so the tests exercise the deadlock retry rather than bypassing it.
func book(ctx context.Context, q *db.Queries, room int64, checkin, checkout pgtype.Date) error {
	_, err := Create(ctx, q, db.CreateOccupancyParams{
		RoomID:   room,
		Checkin:  checkin,
		Checkout: checkout,
		Kind:     "booking",
		Source:   "direct",
	})
	return err
}

// The central claim of the architecture: when several guests check out
// simultaneously for the last available room, the database — not application
// logic — decides, and exactly one wins.
func TestConcurrentBookingsRaceForTheSameRoom(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "flume")

	checkin, checkout := mustDate(t, "2027-06-10"), mustDate(t, "2027-06-13")

	const racers = 16
	errs := make([]error, racers)

	// Every goroutine blocks on the same channel so they are released together
	// and genuinely contend, rather than trickling in one at a time.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = book(ctx, q, room, checkin, checkout)
		}(i)
	}
	close(start)
	wg.Wait()

	var won, lost int
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrRoomTaken):
			lost++
		default:
			t.Fatalf("racer %d got an unexpected error: %v", i, err)
		}
	}

	if won != 1 {
		t.Errorf("%d of %d racers booked the same room; exactly 1 must win", won, racers)
	}
	if lost != racers-1 {
		t.Errorf("%d racers were cleanly rejected, want %d", lost, racers-1)
	}
}

// Regression guard for the deadlock this suite originally exposed.
//
// Identical spans are the obvious race, but staggered overlapping ones are
// worse: each insert waits on a different uncommitted neighbour, which is
// exactly the cycle Postgres resolves by aborting somebody with 40P01. Before
// Create retried, that surfaced as a raw driver error roughly once every
// twenty-five runs — an error page mid-checkout that a guest could not tell
// apart from the room being genuinely gone.
//
// The invariant is not "one winner" here, since some spans do not overlap each
// other. It is that a caller never sees anything except success or a clean
// ErrRoomTaken.
func TestStaggeredOverlapsNeverLeakDeadlocks(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "flume")

	const racers = 24
	errs := make([]error, racers)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Spans of 2 to 4 nights starting across a 6-day window, so they
			// overlap each other in varying combinations.
			checkin := mustDate(t, fmt.Sprintf("2028-03-%02d", 10+i%6))
			checkout := mustDate(t, fmt.Sprintf("2028-03-%02d", 12+i%3+i%6))
			<-start
			errs[i] = book(ctx, q, room, checkin, checkout)
		}(i)
	}
	close(start)
	wg.Wait()

	var won int
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrRoomTaken):
		default:
			t.Errorf("racer %d leaked a raw database error: %v", i, err)
		}
	}
	if won == 0 {
		t.Error("every racer lost; at least one span should have been claimed")
	}
}

// A guest leaving on the 13th and another arriving on the 13th share a date but
// not a night. Treating that as a conflict costs a sellable night at every
// changeover.
func TestTurnoverDayIsNotAConflict(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "rose-chamber")

	if err := book(ctx, q, room, mustDate(t, "2027-06-10"), mustDate(t, "2027-06-13")); err != nil {
		t.Fatalf("first stay: %v", err)
	}
	if err := book(ctx, q, room, mustDate(t, "2027-06-13"), mustDate(t, "2027-06-15")); err != nil {
		t.Errorf("checkout day should be bookable by the next guest, got: %v", err)
	}
}

func TestOverlapDetection(t *testing.T) {
	// An existing stay of 2027-06-10 to 2027-06-13, i.e. nights 10, 11, 12.
	tests := []struct {
		name          string
		checkin       string
		checkout      string
		wantRoomTaken bool
	}{
		{"identical span", "2027-06-10", "2027-06-13", true},
		{"starts inside", "2027-06-12", "2027-06-14", true},
		{"ends inside", "2027-06-08", "2027-06-11", true},
		{"entirely inside", "2027-06-11", "2027-06-12", true},
		{"entirely surrounding", "2027-06-09", "2027-06-14", true},
		{"immediately before, ends on arrival day", "2027-06-08", "2027-06-10", false},
		{"immediately after, starts on departure day", "2027-06-13", "2027-06-15", false},
		{"well clear", "2027-07-01", "2027-07-03", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, q, _ := setup(t)
			room := roomID(t, ctx, q, "blue-room")

			if err := book(ctx, q, room, mustDate(t, "2027-06-10"), mustDate(t, "2027-06-13")); err != nil {
				t.Fatalf("seeding the existing stay: %v", err)
			}

			err := book(ctx, q, room, mustDate(t, tt.checkin), mustDate(t, tt.checkout))
			gotTaken := errors.Is(err, ErrRoomTaken)

			if gotTaken != tt.wantRoomTaken {
				t.Errorf("booking %s to %s: room taken = %v, want %v (err: %v)",
					tt.checkin, tt.checkout, gotTaken, tt.wantRoomTaken, err)
			}
			if !tt.wantRoomTaken && err != nil {
				t.Errorf("expected the booking to succeed, got: %v", err)
			}
		})
	}
}

// The constraint is scoped per room, so a full house is still seven independent
// calendars.
func TestDifferentRoomsDoNotConflict(t *testing.T) {
	ctx, q, _ := setup(t)

	checkin, checkout := mustDate(t, "2027-08-01"), mustDate(t, "2027-08-04")
	for _, slug := range []string{"flume", "rose-chamber", "blue-room", "back-lavender",
		"washington-room", "garden-suite", "mrs-beals-suite"} {
		if err := book(ctx, q, roomID(t, ctx, q, slug), checkin, checkout); err != nil {
			t.Errorf("booking %s for the same nights as the others failed: %v", slug, err)
		}
	}
}

// An empty range overlaps nothing, so a zero-night stay would slip past the
// exclusion constraint entirely and occupy a room without blocking anyone.
func TestZeroNightStayIsRejected(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "flume")

	same := mustDate(t, "2027-06-10")
	err := book(ctx, q, room, same, same)
	if err == nil {
		t.Fatal("a check-in equal to check-out was accepted; it must be rejected")
	}
	if errors.Is(err, ErrRoomTaken) {
		t.Errorf("wrong rejection reason: got ErrRoomTaken, want a check-constraint violation")
	}
}

// A guest partway through Stripe genuinely owns the room.
func TestHoldBlocksABooking(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "washington-room")

	_, err := Create(ctx, q, db.CreateOccupancyParams{
		RoomID:    room,
		Checkin:   mustDate(t, "2027-09-01"),
		Checkout:  mustDate(t, "2027-09-03"),
		Kind:      "hold",
		Source:    "direct",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(15 * time.Minute), Valid: true},
	})
	if err != nil {
		t.Fatalf("creating the hold: %v", err)
	}

	err = book(ctx, q, room, mustDate(t, "2027-09-02"), mustDate(t, "2027-09-04"))
	if !errors.Is(err, ErrRoomTaken) {
		t.Errorf("a live hold must block an overlapping booking, got: %v", err)
	}
}

// ...but an abandoned checkout must not hold the room forever.
func TestSweepReclaimsExpiredHolds(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "washington-room")

	_, err := Create(ctx, q, db.CreateOccupancyParams{
		RoomID:    room,
		Checkin:   mustDate(t, "2027-09-01"),
		Checkout:  mustDate(t, "2027-09-03"),
		Kind:      "hold",
		Source:    "direct",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})
	if err != nil {
		t.Fatalf("creating the expired hold: %v", err)
	}

	swept, err := q.SweepExpiredHolds(ctx)
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if swept != 1 {
		t.Errorf("swept %d holds, want 1", swept)
	}

	if err := book(ctx, q, room, mustDate(t, "2027-09-01"), mustDate(t, "2027-09-03")); err != nil {
		t.Errorf("the room should be free once the hold expired, got: %v", err)
	}
}

// A hold with no expiry would never be swept and would remove a room from sale
// permanently; a booking with one would evaporate mid-stay.
func TestHoldAndExpiryMustAgree(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "garden-suite")

	t.Run("hold without an expiry is rejected", func(t *testing.T) {
		_, err := Create(ctx, q, db.CreateOccupancyParams{
			RoomID:   room,
			Checkin:  mustDate(t, "2027-10-01"),
			Checkout: mustDate(t, "2027-10-03"),
			Kind:     "hold",
			Source:   "direct",
		})
		if err == nil {
			t.Error("a hold with no expires_at was accepted")
		}
	})

	t.Run("booking with an expiry is rejected", func(t *testing.T) {
		_, err := Create(ctx, q, db.CreateOccupancyParams{
			RoomID:    room,
			Checkin:   mustDate(t, "2027-10-05"),
			Checkout:  mustDate(t, "2027-10-07"),
			Kind:      "booking",
			Source:    "direct",
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		if err == nil {
			t.Error("a booking carrying expires_at was accepted")
		}
	})
}

// An owner's block is occupancy like any other: it takes the room off sale.
func TestOwnerBlockPreventsBooking(t *testing.T) {
	ctx, q, _ := setup(t)
	room := roomID(t, ctx, q, "mrs-beals-suite")

	_, err := Create(ctx, q, db.CreateOccupancyParams{
		RoomID:   room,
		Checkin:  mustDate(t, "2027-11-20"),
		Checkout: mustDate(t, "2027-11-25"),
		Kind:     "block",
		Source:   "direct",
		Reason:   "family staying",
	})
	if err != nil {
		t.Fatalf("creating the block: %v", err)
	}

	if err := book(ctx, q, room, mustDate(t, "2027-11-22"), mustDate(t, "2027-11-24")); !errors.Is(err, ErrRoomTaken) {
		t.Errorf("a blocked room must not be bookable, got: %v", err)
	}
}

// The occupancy half of availability: a room with an overlapping stay drops out
// of the free list, and reappears once the dates clear it.
func TestListRoomsFreeBetween(t *testing.T) {
	ctx, q, _ := setup(t)
	taken := roomID(t, ctx, q, "flume")

	if err := book(ctx, q, taken, mustDate(t, "2027-06-10"), mustDate(t, "2027-06-13")); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	free, err := q.ListRoomsFreeBetween(ctx, db.ListRoomsFreeBetweenParams{
		Checkin:  mustDate(t, "2027-06-11"),
		Checkout: mustDate(t, "2027-06-12"),
	})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(free) != 6 {
		t.Errorf("got %d free rooms, want 6 of 7", len(free))
	}
	for _, id := range free {
		if id == taken {
			t.Error("the occupied room appeared in the free list")
		}
	}

	// The turnover day again, from the availability side.
	free, err = q.ListRoomsFreeBetween(ctx, db.ListRoomsFreeBetweenParams{
		Checkin:  mustDate(t, "2027-06-13"),
		Checkout: mustDate(t, "2027-06-15"),
	})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(free) != 7 {
		t.Errorf("got %d free rooms for the span starting on checkout day, want all 7", len(free))
	}
}
