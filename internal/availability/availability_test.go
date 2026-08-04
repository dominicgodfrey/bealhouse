package availability

import (
	"context"
	"errors"
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
func setup(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()
	pool := testdb.Connect(t)
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

// Checking the pet box narrows results to the only room that takes pets.
func TestPetSearchReturnsOnlyBackLavender(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2, WithPet: true})

	if len(res.Rooms) != 1 || res.Rooms[0].Slug != "back-lavender" {
		t.Fatalf("pet search returned %v, want just back-lavender", slugs(res))
	}
	if fee := res.Rooms[0].Quote.PetFeeCents; fee != 5000 {
		t.Errorf("pet fee %d cents, want 5000", fee)
	}
}

// ...but leaving the box unchecked must not hide it. It is an ordinary room
// that also happens to accept pets, and one of only two that sleep three.
func TestBackLavenderIsOfferedToGuestsWithoutPets(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2})
	room := roomBySlug(t, res, "back-lavender")

	if !room.IsPetFriendly {
		t.Error("the room should still be flagged pet friendly")
	}
	if room.Quote.PetFeeCents != 0 {
		t.Errorf("charged a %d cent pet fee to a guest with no pet", room.Quote.PetFeeCents)
	}
}

// The fee is its own line so the price preview can show the guest what the
// extra $50 is, rather than burying it in a larger subtotal.
func TestPetFeeIsSeparatedAndTaxed(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2, WithPet: true})
	q1 := res.Rooms[0].Quote

	// Back Lavender at the seeded $150 base: 2 nights = $300, plus $50 pet fee,
	// taxed together at 8.5%.
	if q1.RoomSubtotalCents != 30000 {
		t.Errorf("room subtotal %d, want 30000", q1.RoomSubtotalCents)
	}
	if q1.PetFeeCents != 5000 {
		t.Errorf("pet fee %d, want 5000", q1.PetFeeCents)
	}
	if q1.TaxableCents != 35000 {
		t.Errorf("taxable %d, want 35000 (the fee is taxed with the room)", q1.TaxableCents)
	}
	if q1.TaxCents != 2975 {
		t.Errorf("tax %d, want 2975", q1.TaxCents)
	}
	if q1.TotalCents != 37975 {
		t.Errorf("total %d, want 37975", q1.TotalCents)
	}
	// Odd total: the deposit takes the extra cent and the two still reconcile.
	if q1.DepositCents != 18988 || q1.BalanceCents != 18987 {
		t.Errorf("deposit/balance %d/%d, want 18988/18987", q1.DepositCents, q1.BalanceCents)
	}
}

func TestPricingMatchesTheRateCalendar(t *testing.T) {
	ctx, q := setup(t)

	res := search(t, ctx, q, Request{Checkin: day(30), Checkout: day(32), Guests: 2})
	rose := roomBySlug(t, res, "rose-chamber")

	if len(rose.NightlyCents) != 2 {
		t.Fatalf("got %d nightly prices, want 2", len(rose.NightlyCents))
	}
	for i, cents := range rose.NightlyCents {
		if cents != 15000 {
			t.Errorf("night %d is %d cents, want the seeded 15000", i, cents)
		}
	}
	if rose.Quote.TotalCents != 32550 {
		t.Errorf("all-in total %d, want 32550", rose.Quote.TotalCents)
	}
	if rose.Quote.DepositCents != 16275 {
		t.Errorf("deposit %d, want 16275", rose.Quote.DepositCents)
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
