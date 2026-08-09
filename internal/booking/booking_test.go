package booking

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/availability"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// Most tests run inside a rolled-back transaction: Create's own transaction
// becomes a savepoint inside it, so a booking is genuinely committed as far as
// the code under test can tell, and vanishes when the test ends.
func setup(t *testing.T) (context.Context, *db.Queries, Beginner) {
	t.Helper()
	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	return context.Background(), db.New(tx), tx
}

func day(offset int) time.Time { return civil.AddDays(civil.Today(), offset) }

func dayString(offset int) string { return day(offset).Format(time.DateOnly) }

// request is a booking that should succeed, which each test then breaks in
// exactly one way.
//
// The dates sit far out on purpose. Most of these tests roll back, but the
// concurrency one commits, and `go test ./...` runs packages in parallel
// against a shared database: a committed booking in a stretch of calendar
// another package asserts about would quietly break that package instead.
func request() Request {
	return Request{
		RoomSlug: "rose-chamber",
		Checkin:  day(200),
		Checkout: day(202),
		Guests:   2,
		Guest:    Guest{Name: "Ada Lovelace", Email: "ada@example.com", Phone: "603-555-0100"},
		// Every test here is about something else, and a bookable request is
		// one that accepted the policies. TestPoliciesMustBeAccepted is where
		// the refusal itself is asserted.
		AcceptedPolicies: true,
	}
}

func create(t *testing.T, ctx context.Context, b Beginner, req Request) Booking {
	t.Helper()
	out, err := Create(ctx, b, req)
	if err != nil {
		t.Fatalf("creating the booking: %v", err)
	}
	return out
}

func TestCreateHoldsTheRoom(t *testing.T) {
	ctx, q, b := setup(t)

	made := create(t, ctx, b, request())

	if !strings.HasPrefix(made.Code, "BH-") || len(made.Code) != 9 {
		t.Errorf("code %q does not look like BH- plus six characters", made.Code)
	}
	if made.Status != StatusPending {
		t.Errorf("status %q, want %q — nothing is paid yet", made.Status, StatusPending)
	}
	if made.HoldExpiresAt == nil {
		t.Fatal("no hold expiry; the room is not actually reserved")
	}
	if until := time.Until(*made.HoldExpiresAt); until < 14*time.Minute || until > 15*time.Minute {
		t.Errorf("hold expires in %v, want the seeded 15-minute TTL", until)
	}

	// Rose Chamber at the seeded $150: two nights, taxed at 8.5%.
	if made.Quote.TotalCents != 32550 || made.Quote.DepositCents != 16275 {
		t.Errorf("quote total/deposit %d/%d, want 32550/16275",
			made.Quote.TotalCents, made.Quote.DepositCents)
	}

	// The point of the hold: the room is gone from the search that produced it.
	res, err := availability.Search(ctx, q, availability.Request{
		Checkin: day(200), Checkout: day(202), Guests: 2,
	})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	for _, room := range res.Rooms {
		if room.Slug == "rose-chamber" {
			t.Error("a held room was still offered for sale")
		}
	}
}

// The date picker greys out one-night stays. That is a convenience, and this is
// the boundary: a hand-crafted payload must be refused by the server.
func TestMinimumStayCannotBeBypassed(t *testing.T) {
	ctx, _, b := setup(t)

	req := request()
	req.Checkout = day(201) // one night, against a global minimum of two

	if _, err := Create(ctx, b, req); !errors.Is(err, ErrRoomUnavailable) {
		t.Errorf("a one-night payload got %v, want ErrRoomUnavailable", err)
	}
}

// The maximum stay is the picker's rule and the server's rule, for the same
// reason the minimum is: a hand-crafted payload must not get a two-month stay
// past the booking endpoint.
func TestMaximumStayCannotBeBypassed(t *testing.T) {
	ctx, q, b := setup(t)

	settings, err := q.GetSettings(ctx)
	if err != nil {
		t.Fatalf("loading settings: %v", err)
	}

	req := request()
	req.Checkout = day(200 + int(settings.MaxStayNights) + 1)

	if _, err := Create(ctx, b, req); !errors.Is(err, availability.ErrStayTooLong) {
		t.Errorf("an over-long payload got %v, want ErrStayTooLong", err)
	}
}

// Capacity is a filter in search and a rule here. Rose Chamber sleeps two.
func TestCapacityCannotBeBypassed(t *testing.T) {
	ctx, _, b := setup(t)

	req := request()
	req.Guests = 4

	if _, err := Create(ctx, b, req); !errors.Is(err, ErrRoomUnavailable) {
		t.Errorf("overfilling a room got %v, want ErrRoomUnavailable", err)
	}
}

func TestUnknownRoomIsRejected(t *testing.T) {
	ctx, _, b := setup(t)

	req := request()
	req.RoomSlug = "the-tower"

	if _, err := Create(ctx, b, req); !errors.Is(err, ErrRoomUnavailable) {
		t.Errorf("booking a room that does not exist got %v, want ErrRoomUnavailable", err)
	}
}

func TestDateValidationIsSharedWithSearch(t *testing.T) {
	ctx, _, b := setup(t)

	tests := []struct {
		name string
		req  func(Request) Request
		want error
	}{
		{"check-in in the past", func(r Request) Request {
			r.Checkin, r.Checkout = day(-2), day(2)
			return r
		}, availability.ErrCheckinInPast},
		{"checkout before check-in", func(r Request) Request {
			r.Checkout = day(28)
			return r
		}, availability.ErrCheckoutNotAfterCheckin},
		{"no guests", func(r Request) Request {
			r.Guests = 0
			return r
		}, availability.ErrGuestsOutOfRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Create(ctx, b, tt.req(request())); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// Nobody is charged a price they were not shown.
func TestPriceDisagreementIsRejected(t *testing.T) {
	ctx, _, b := setup(t)

	req := request()
	req.ExpectedTotalCents = 30000 // the guest saw a stale, cheaper quote

	if _, err := Create(ctx, b, req); !errors.Is(err, ErrPriceChanged) {
		t.Errorf("got %v, want ErrPriceChanged", err)
	}

	req.ExpectedTotalCents = 32550
	if _, err := Create(ctx, b, req); err != nil {
		t.Errorf("the correct total was rejected: %v", err)
	}
}

func TestGuestDetailsAreRequired(t *testing.T) {
	ctx, _, b := setup(t)

	tests := []struct {
		name  string
		guest Guest
		want  error
	}{
		{"no name", Guest{Email: "ada@example.com"}, ErrGuestNameRequired},
		{"blank name", Guest{Name: "   ", Email: "ada@example.com"}, ErrGuestNameRequired},
		{"no email", Guest{Name: "Ada"}, ErrGuestEmailRequired},
		{"email without a local part", Guest{Name: "Ada", Email: "@example.com"}, ErrGuestEmailRequired},
		{"email without a domain", Guest{Name: "Ada", Email: "ada@"}, ErrGuestEmailRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request()
			req.Guest = tt.guest
			if _, err := Create(ctx, b, req); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// The fee is snapshotted as its own column, so a booking can always say what
// the extra $50 was for.
func TestPetFeeIsSnapshotted(t *testing.T) {
	ctx, q, b := setup(t)

	req := request()
	req.RoomSlug = "back-lavender"
	req.WithPet = true

	made := create(t, ctx, b, req)
	if made.Quote.PetFeeCents != 5000 {
		t.Errorf("pet fee %d, want 5000", made.Quote.PetFeeCents)
	}

	read, err := Get(ctx, q, made.Code)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if !read.WithPet || read.Quote.PetFeeCents != 5000 {
		t.Errorf("read back withPet=%v fee=%d, want true/5000", read.WithPet, read.Quote.PetFeeCents)
	}
	if read.Quote.TotalCents != made.Quote.TotalCents {
		t.Errorf("total drifted between writing and reading: %d then %d",
			made.Quote.TotalCents, read.Quote.TotalCents)
	}
}

// Decision #6: the balance is charged off-session seven days before arrival.
func TestBalanceIsScheduledForSevenDaysOut(t *testing.T) {
	ctx, q, b := setup(t)

	made := create(t, ctx, b, request())

	if want := dayString(193); made.BalanceChargeOn != want {
		t.Errorf("balance charge on %q, want %q (T-7 for a stay starting %s)",
			made.BalanceChargeOn, want, dayString(200))
	}
	if made.ChargeNowCents != made.Quote.DepositCents {
		t.Errorf("charging %d at booking, want the %d deposit",
			made.ChargeNowCents, made.Quote.DepositCents)
	}

	read, err := Get(ctx, q, made.Code)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if read.BalanceChargeOn != made.BalanceChargeOn || read.ChargeNowCents != made.ChargeNowCents {
		t.Errorf("read back %q/%d, want %q/%d", read.BalanceChargeOn, read.ChargeNowCents,
			made.BalanceChargeOn, made.ChargeNowCents)
	}
}

// Decision #7: inside the window there is no time left to run the T-7 job, so
// the stay is charged in full and no job is scheduled at all.
func TestShortNoticeArrivalIsChargedInFull(t *testing.T) {
	ctx, q, b := setup(t)

	req := request()
	req.Checkin, req.Checkout = day(2), day(4)

	made := create(t, ctx, b, req)

	if made.BalanceChargeOn != "" {
		t.Errorf("scheduled a balance charge for %q; a short-notice stay is paid up front",
			made.BalanceChargeOn)
	}
	if made.ChargeNowCents != made.Quote.TotalCents {
		t.Errorf("charging %d at booking, want the full %d", made.ChargeNowCents, made.Quote.TotalCents)
	}

	read, err := Get(ctx, q, made.Code)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if read.ChargeNowCents != read.Quote.TotalCents || read.BalanceChargeOn != "" {
		t.Errorf("read back %d due now and a charge date of %q, want the full total and no date",
			read.ChargeNowCents, read.BalanceChargeOn)
	}
}

// A hold is occupancy, so the turnover rule applies to it too: leaving on the
// 33rd and arriving on the 33rd are not a conflict.
func TestTurnoverBetweenTwoHoldsIsAllowed(t *testing.T) {
	ctx, _, b := setup(t)

	first := request()
	first.Checkout = day(203)
	create(t, ctx, b, first)

	second := request()
	second.Checkin, second.Checkout = day(203), day(205)
	second.Guest.Email = "grace@example.com"
	if _, err := Create(ctx, b, second); err != nil {
		t.Errorf("the checkout day should be bookable by the next guest, got: %v", err)
	}
}

// An abandoned checkout must not hold a room forever, and the booking behind it
// should say what happened rather than sit pending.
func TestSweptHoldFreesTheRoomAndExpiresTheBooking(t *testing.T) {
	ctx, q, b := setup(t)

	made := create(t, ctx, b, request())

	// Reach in and age the hold rather than waiting fifteen minutes.
	if err := expireHold(ctx, b, made.Code); err != nil {
		t.Fatalf("ageing the hold: %v", err)
	}

	holds, expired, err := Sweep(ctx, q)
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if holds == 0 {
		t.Fatal("the sweeper found no hold to reclaim")
	}
	if expired == 0 {
		t.Fatal("the hold went but the booking behind it stayed pending")
	}

	read, err := Get(ctx, q, made.Code)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if read.Status != StatusExpired {
		t.Errorf("status %q after the hold lapsed, want %q", read.Status, StatusExpired)
	}
	if read.HoldExpiresAt != nil {
		t.Error("a reclaimed booking still reports a live hold")
	}

	// ...and the room is sellable again.
	res, err := availability.Search(ctx, q, availability.Request{
		Checkin: day(200), Checkout: day(202), Guests: 2,
	})
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	var offered bool
	for _, room := range res.Rooms {
		offered = offered || room.Slug == "rose-chamber"
	}
	if !offered {
		t.Error("the room stayed off sale after its hold was swept")
	}
}

func TestGetRejectsAnUnknownCode(t *testing.T) {
	ctx, q, _ := setup(t)

	if _, err := Get(ctx, q, "BH-ZZZZZZ"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// The whole architecture in one test, at the layer a guest actually touches:
// several people confirming the last room at the same moment, and the database
// deciding. Exactly one hold exists afterwards.
func TestConcurrentConfirmationsForTheLastRoom(t *testing.T) {
	pool := testdb.Connect(t)
	testdb.Exclusive(t, pool)
	testdb.ResetBookings(t, pool)
	testdb.ResetOccupancy(t, pool)
	t.Cleanup(func() {
		testdb.ResetBookings(t, pool)
		testdb.ResetOccupancy(t, pool)
	})

	ctx := context.Background()

	const racers = 12
	errs := make([]error, racers)
	codes := make([]string, racers)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request()
			// A different guest every time, so the guest upsert is contended
			// too rather than quietly serialising the racers.
			req.Guest.Email = string(rune('a'+i)) + "@example.com"
			<-start
			made, err := Create(ctx, pool, req)
			errs[i], codes[i] = err, made.Code
		}(i)
	}
	close(start)
	wg.Wait()

	// A loser lands in one of two places depending on how close the race was:
	// gone before it searched, or taken from under it at the insert. What must
	// never happen is a raw database error, which a guest cannot act on.
	var won int
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case IsUnavailable(err):
		default:
			t.Errorf("racer %d saw something other than success or a clean rejection: %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d racers booked the same room; exactly 1 must win", won, racers)
	}

	// The losers must leave nothing behind — no orphan pending booking, no
	// guest charged for a room they did not get.
	q := db.New(pool)
	var bookings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM bookings").Scan(&bookings); err != nil {
		t.Fatalf("counting bookings: %v", err)
	}
	if bookings != 1 {
		t.Errorf("%d bookings survived, want 1: a losing racer left one behind", bookings)
	}

	for i, code := range codes {
		if errs[i] != nil {
			continue
		}
		read, err := Get(ctx, q, code)
		if err != nil {
			t.Fatalf("reading the winner back: %v", err)
		}
		if read.HoldExpiresAt == nil {
			t.Error("the winning booking has no hold on the room")
		}
	}
}

// expireHold ages a booking's hold past its TTL, standing in for fifteen
// minutes of a guest doing nothing.
func expireHold(ctx context.Context, b Beginner, code string) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE room_occupancy o
		SET expires_at = now() - interval '1 minute'
		FROM bookings b
		WHERE b.id = o.booking_id AND b.code = $1 AND o.kind = 'hold'`, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The tick-box on the confirm page disables a button. This is what actually
// stops the booking, because a disabled button is a suggestion to anything that
// is not a browser — curl, a stale bundle, a script.
//
// The second half is the point of recording it at all: a stay that was booked
// carries the moment its terms were accepted, stamped with the database's clock
// rather than the caller's, so it cannot be back-dated by a client that fancies
// it.
func TestABookingCannotSkipThePolicies(t *testing.T) {
	ctx, _, b := setup(t)

	// b is the test's own pgx.Tx, which is what can run raw SQL — db.Queries
	// only exposes the generated ones, and neither column below has a query of
	// its own outside this assertion.
	tx := b.(pgx.Tx)

	refused := request()
	refused.AcceptedPolicies = false
	if _, err := Create(ctx, b, refused); !errors.Is(err, ErrPoliciesNotAccepted) {
		t.Fatalf("a booking that accepted nothing got %v, want ErrPoliciesNotAccepted", err)
	}

	// ...and nothing was written. A refusal that still took the room off sale
	// would be worse than no check at all.
	var held int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE checkin = $1`, day(200)).Scan(&held); err != nil {
		t.Fatalf("counting bookings: %v", err)
	}
	if held != 0 {
		t.Fatalf("%d bookings exist after a refusal; want none", held)
	}

	made := create(t, ctx, b, request())

	var acceptedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT policies_accepted_at FROM bookings WHERE code = $1`, made.Code).Scan(&acceptedAt); err != nil {
		t.Fatalf("reading the acceptance: %v", err)
	}
	if acceptedAt == nil {
		t.Fatal("the booking records no acceptance; the tick-box is decoration")
	}
	if since := time.Since(*acceptedAt); since < 0 || since > time.Minute {
		t.Errorf("accepted at %v, which is %v ago — that is not this transaction's clock", *acceptedAt, since)
	}
}
