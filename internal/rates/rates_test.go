package rates

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// Every test runs inside a rolled-back transaction, so replacing the whole
// season table is free and leaves the developer's database untouched.
func setup(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()

	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	q := db.New(tx)

	ctx := context.Background()
	if _, err := q.DeleteAllRateSeasons(ctx); err != nil {
		t.Fatalf("clearing seasons: %v", err)
	}
	return ctx, q
}

func date(t *testing.T, s string) pgtype.Date {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return pgtype.Date{Time: d, Valid: true}
}

// season creates a season priced identically for every room.
func season(t *testing.T, ctx context.Context, q *db.Queries, name, from, to string, minStay *int32, priority int32, price int32) int64 {
	t.Helper()

	id, err := q.CreateRateSeason(ctx, db.CreateRateSeasonParams{
		Name:     name,
		StartsOn: date(t, from),
		EndsOn:   date(t, to),
		MinStay:  minStay,
		Priority: priority,
	})
	if err != nil {
		t.Fatalf("creating season %q: %v", name, err)
	}

	for _, slug := range allSlugs {
		roomID, err := q.GetRoomIDBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("looking up %q (is the room seed loaded?): %v", slug, err)
		}
		if err := q.SetSeasonPrice(ctx, db.SetSeasonPriceParams{
			SeasonID:   id,
			RoomID:     roomID,
			PriceCents: price,
		}); err != nil {
			t.Fatalf("pricing %q for %q: %v", slug, name, err)
		}
	}
	return id
}

var allSlugs = []string{
	"mrs-beals-suite", "garden-suite", "flume", "rose-chamber",
	"washington-room", "blue-room", "back-lavender",
}

func rateOn(t *testing.T, ctx context.Context, q *db.Queries, slug, day string) (price int32, minStay int32, found bool) {
	t.Helper()

	roomID, err := q.GetRoomIDBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("looking up %q: %v", slug, err)
	}
	row, err := q.GetRateCalendarEntry(ctx, db.GetRateCalendarEntryParams{
		RoomID: roomID,
		Date:   date(t, day),
	})
	if err != nil {
		return 0, 0, false
	}
	return row.PriceCents, row.MinStay, true
}

func TestSeasonExpandsToEveryNight(t *testing.T) {
	ctx, q := setup(t)
	season(t, ctx, q, "Test", "2027-01-01", "2027-01-10", nil, 0, 15000)

	n, err := Rebuild(ctx, q, civil.Date(2027, time.January, 1), civil.Date(2027, time.January, 10))
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// Seven rooms across ten inclusive nights.
	if want := int64(70); n != want {
		t.Errorf("generated %d nights, want %d", n, want)
	}

	// Both ends of the range are included: ends_on is a night, not a checkout.
	for _, day := range []string{"2027-01-01", "2027-01-10"} {
		if _, _, found := rateOn(t, ctx, q, "flume", day); !found {
			t.Errorf("no rate generated for %s", day)
		}
	}
}

// A season leaving min_stay NULL inherits the global default rather than
// hardcoding 2 in the generator.
func TestMinStayFallsBackToSettings(t *testing.T) {
	ctx, q := setup(t)
	season(t, ctx, q, "Test", "2027-01-01", "2027-01-10", nil, 0, 15000)

	if _, err := Rebuild(ctx, q, civil.Date(2027, time.January, 1), civil.Date(2027, time.January, 10)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	_, minStay, found := rateOn(t, ctx, q, "flume", "2027-01-05")
	if !found {
		t.Fatal("no rate generated")
	}
	if minStay != 2 {
		t.Errorf("min stay %d, want the global default of 2", minStay)
	}
}

// A Thanksgiving season sitting inside a Leaf Season must win on its own dates
// and leave the surrounding range alone. Without priority this is undefined
// behaviour and the owner gets silently wrong prices.
func TestHigherPriorityOverlappingSeasonWins(t *testing.T) {
	ctx, q := setup(t)

	threeNights := int32(3)
	season(t, ctx, q, "Leaf Season", "2027-09-01", "2027-11-30", nil, 0, 20000)
	season(t, ctx, q, "Thanksgiving", "2027-11-25", "2027-11-28", &threeNights, 10, 35000)

	if _, err := Rebuild(ctx, q, civil.Date(2027, time.September, 1), civil.Date(2027, time.November, 30)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	tests := []struct {
		day         string
		wantPrice   int32
		wantMinStay int32
		note        string
	}{
		{"2027-10-15", 20000, 2, "well inside the outer season"},
		{"2027-11-24", 20000, 2, "the night before the override starts"},
		{"2027-11-25", 35000, 3, "first night of the override"},
		{"2027-11-28", 35000, 3, "last night of the override"},
		{"2027-11-29", 20000, 2, "the night after the override ends"},
	}

	for _, tt := range tests {
		t.Run(tt.note, func(t *testing.T) {
			price, minStay, found := rateOn(t, ctx, q, "flume", tt.day)
			if !found {
				t.Fatalf("no rate for %s", tt.day)
			}
			if price != tt.wantPrice {
				t.Errorf("%s price %d, want %d", tt.day, price, tt.wantPrice)
			}
			if minStay != tt.wantMinStay {
				t.Errorf("%s min stay %d, want %d", tt.day, minStay, tt.wantMinStay)
			}
		})
	}
}

// A night no season covers must produce no rate at all. An unbookable night is
// the right outcome; a night sold at a guessed price is not.
func TestUncoveredNightsGetNoRate(t *testing.T) {
	ctx, q := setup(t)

	season(t, ctx, q, "Spring", "2027-04-01", "2027-04-10", nil, 0, 15000)
	season(t, ctx, q, "Summer", "2027-06-01", "2027-06-10", nil, 0, 20000)

	if _, err := Rebuild(ctx, q, civil.Date(2027, time.April, 1), civil.Date(2027, time.June, 10)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if _, _, found := rateOn(t, ctx, q, "flume", "2027-05-15"); found {
		t.Error("a night between two seasons was given a rate")
	}
	if _, _, found := rateOn(t, ctx, q, "flume", "2027-04-05"); !found {
		t.Error("a covered night was missing a rate")
	}
}

// Editing a season and rebuilding must not disturb nights already in the past,
// which is what keeps historical reporting stable.
func TestRebuildIsFutureOnly(t *testing.T) {
	ctx, q := setup(t)
	season(t, ctx, q, "Old", "2026-01-01", "2026-01-31", nil, 0, 11100)

	// Lay down January at the old price.
	if _, err := Rebuild(ctx, q, civil.Date(2026, time.January, 1), civil.Date(2026, time.January, 31)); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	if price, _, _ := rateOn(t, ctx, q, "flume", "2026-01-10"); price != 11100 {
		t.Fatalf("setup price %d, want 11100", price)
	}

	// Overlay a higher-priority season at a new price, then rebuild only from
	// the 20th onward.
	season(t, ctx, q, "New", "2026-01-01", "2026-01-31", nil, 5, 22200)

	if _, err := Rebuild(ctx, q, civil.Date(2026, time.January, 20), civil.Date(2026, time.January, 31)); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}

	if price, _, _ := rateOn(t, ctx, q, "flume", "2026-01-10"); price != 11100 {
		t.Errorf("a night before the rebuild window changed to %d; it must stay 11100", price)
	}
	if price, _, _ := rateOn(t, ctx, q, "flume", "2026-01-25"); price != 22200 {
		t.Errorf("a night inside the rebuild window is %d, want 22200", price)
	}
}

// Rebuilding twice must not duplicate rows or change any price. The monthly job
// depends on this.
func TestRebuildIsIdempotent(t *testing.T) {
	ctx, q := setup(t)
	season(t, ctx, q, "Test", "2027-01-01", "2027-01-10", nil, 0, 15000)

	from, to := civil.Date(2027, time.January, 1), civil.Date(2027, time.January, 10)

	first, err := Rebuild(ctx, q, from, to)
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	second, err := Rebuild(ctx, q, from, to)
	if err != nil {
		t.Fatalf("second rebuild: %v", err)
	}

	if first != second {
		t.Errorf("rebuild generated %d rows then %d; it must be idempotent", first, second)
	}
}

// The nightly breakdown a quote is built from excludes the checkout date,
// because a guest does not pay for the night they leave.
func TestListNightlyRatesExcludesCheckoutDate(t *testing.T) {
	ctx, q := setup(t)
	season(t, ctx, q, "Test", "2027-01-01", "2027-01-31", nil, 0, 15000)

	if _, err := Rebuild(ctx, q, civil.Date(2027, time.January, 1), civil.Date(2027, time.January, 31)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	roomID, err := q.GetRoomIDBySlug(ctx, "flume")
	if err != nil {
		t.Fatal(err)
	}

	// 10th to 13th is three nights: the 10th, 11th and 12th.
	nights, err := q.ListNightlyRates(ctx, db.ListNightlyRatesParams{
		RoomID:   roomID,
		Checkin:  date(t, "2027-01-10"),
		Checkout: date(t, "2027-01-13"),
	})
	if err != nil {
		t.Fatalf("listing nightly rates: %v", err)
	}
	if len(nights) != 3 {
		t.Fatalf("got %d nights for a 10th-to-13th stay, want 3", len(nights))
	}
	if last := nights[len(nights)-1].Date.Time.Format("2006-01-02"); last != "2027-01-12" {
		t.Errorf("last billed night is %s, want 2027-01-12", last)
	}
}
