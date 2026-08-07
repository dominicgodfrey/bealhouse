package console_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/gateway"
	"bealhouse/internal/testdb"
)

// The console's tests run inside a rolled-back transaction, so its nested Begin
// is a savepoint and nothing here survives the test — which is what lets them
// rewrite reference data (seasons, page copy, settings) freely.
//
// This package books in its own stretch of calendar: **today + 500 onwards**.
// Every package that commits bookings has its own window, because a booking
// inside somebody else's silently breaks their assertions. See CLAUDE.md.
const window = 500

func day(offset int) time.Time { return civil.AddDays(civil.Today(), window+offset) }

func date(offset int) string { return day(offset).Format(time.DateOnly) }

func ops(t *testing.T) (*console.Ops, pgx.Tx) {
	t.Helper()
	tx := testdb.Tx(t, testdb.Connect(t))
	// No processor: the only operation that needs one is keying in a card, and
	// gateway.Disabled is what a deployment without keys has anyway. Everything
	// else here works with no Stripe account, which is the property the payments
	// package was built around and this one inherits.
	return console.New(tx, nil, letterhead, console.Processor{Gateway: gateway.Disabled{}}), tx
}

// The owner's address and a stand-in signer, so the tests below see the same
// two messages a real deploy queues. The signer returns something recognisable
// rather than a real HMAC: what is being tested here is that the link reaches
// the confirmation, not how it is signed — booking.Links has its own tests for
// that.
var letterhead = console.Letterhead{
	OwnerEmail: "owner@example.test",
	SiteURL:    "https://example.test",
	ManageURL: func(code string, expires time.Time) string {
		return "https://example.test/booking/" + code + "?t=signed"
	},
}

// queuedMail reads back the messages queued for one booking, by template.
//
// Filtered on the booking's own code, so it needs neither the exclusive lock nor
// an emptied queue: no other package's committed rows can be mistaken for these.
func queuedMail(t *testing.T, tx pgx.Tx, template, code string) []map[string]any {
	t.Helper()

	rows, err := tx.Query(context.Background(), `
		SELECT payload
		FROM jobs
		WHERE kind = $1
		  AND payload->>'template' = $2
		  AND payload->'data'->>'Code' = $3
		ORDER BY id`, email.JobKind, template, code)
	if err != nil {
		t.Fatalf("reading queued mail: %v", err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scanning queued mail: %v", err)
		}
		var env map[string]any
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decoding queued mail: %v", err)
		}
		out = append(out, env)
	}
	return out
}

func field(t *testing.T, env map[string]any, key string) string {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("the message carries no data")
	}
	value, _ := data[key].(string)
	return value
}

// A guest for a manual booking. The email is unique per test so the guest
// upsert cannot make two tests share a row.
func guest(t *testing.T) (string, string) {
	t.Helper()
	return t.Name(), t.Name() + "@example.test"
}

func firstRoomSlug(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	rooms, err := db.New(tx).ListRooms(context.Background())
	if err != nil || len(rooms) == 0 {
		t.Fatalf("listing rooms: %v (load internal/db/seed/rooms.sql)", err)
	}
	return rooms[0].Slug
}

func firstRoomID(t *testing.T, tx pgx.Tx) int64 {
	t.Helper()
	rooms, err := db.New(tx).ListRooms(context.Background())
	if err != nil || len(rooms) == 0 {
		t.Fatalf("listing rooms: %v", err)
	}
	return rooms[0].ID
}

// A manual booking is the owner writing down a reservation taken on the phone.
//
// The three things that make it different from a guest's are all things that
// would otherwise misfire later: a pending booking would go on the arrivals
// board as somebody who never paid, a hold would let the sweeper resell the room
// at minute fifteen, and a balance_charge_at would have the T-7 job try to
// charge a card that was never saved — flagging a failure and mailing the guest
// about a payment they were always going to make by cheque.
func TestAManualBookingIsConfirmedAndHasNeitherHoldNorScheduledCharge(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(0),
		Checkout: date(3),
		Guests:   2,
		Name:     name,
		Email:    email,
	})
	if err != nil {
		t.Fatalf("taking a manual booking: %v", err)
	}

	if made.Status != booking.StatusConfirmed {
		t.Errorf("status = %q, want confirmed", made.Status)
	}
	if made.HoldExpiresAt != nil {
		t.Error("a manual booking has no checkout to hold a room for, but it has a hold")
	}
	if made.BalanceChargeOn != "" {
		t.Errorf("balance charge scheduled for %q, but no card was saved", made.BalanceChargeOn)
	}

	// And the room really is claimed: the occupancy row is a booking, not a
	// hold, so nothing expires it.
	var kind string
	var expires *time.Time
	err = tx.QueryRow(ctx, `
		SELECT o.kind, o.expires_at FROM room_occupancy o
		JOIN bookings b ON b.id = o.booking_id WHERE b.code = $1`, made.Code).
		Scan(&kind, &expires)
	if err != nil {
		t.Fatalf("reading the occupancy row: %v", err)
	}
	if kind != "booking" || expires != nil {
		t.Errorf("occupancy is kind=%q expires=%v, want a booking that never expires", kind, expires)
	}
}

// A booking taken on the phone earns the guest the same confirmation as one
// taken on the website — and the owner the same copy of it.
//
// Queued inside the transaction that created the booking, so a stay that exists
// without the message telling the guest about it is not a state this system can
// reach.
func TestAManualBookingConfirmsTheGuestAndTellsTheOwner(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, address := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(100),
		Checkout: date(103),
		Guests:   2,
		Name:     name,
		Email:    address,
	})
	if err != nil {
		t.Fatalf("taking a manual booking: %v", err)
	}

	confirmation := queuedMail(t, tx, email.BookingConfirmation, made.Code)
	if len(confirmation) != 1 {
		t.Fatalf("%d confirmations queued, want exactly 1", len(confirmation))
	}
	if to, _ := confirmation[0]["to"].(string); to != address {
		t.Errorf("confirmation addressed to %q, want the guest", to)
	}

	// The one thing this message must not imply. Nothing has been collected, so
	// it reports nothing paid and the whole total outstanding — where a
	// confirmation for a stay paid in full leaves both empty.
	if got := field(t, confirmation[0], "PaidNow"); got != email.Money(0) {
		t.Errorf("PaidNow is %q, want nothing collected", got)
	}
	if got := field(t, confirmation[0], "BalanceDue"); got != email.Money(made.Quote.TotalCents) {
		t.Errorf("BalanceDue is %q, want the whole total outstanding", got)
	}

	// ...and no date, because there is no saved card for anything to be taken
	// from. A guest told they will be charged on a day nothing will happen is
	// worse than one told to settle up with the inn.
	if got := field(t, confirmation[0], "BalanceChargeOn"); got != "" {
		t.Errorf("BalanceChargeOn is %q, but no card was saved", got)
	}

	// The guest's way back to their own booking, which is the only one there is.
	if got := field(t, confirmation[0], "ManageURL"); got == "" {
		t.Error("the confirmation carries no manage link")
	}

	owner := queuedMail(t, tx, email.OwnerNotification, made.Code)
	if len(owner) != 1 {
		t.Fatalf("%d owner notifications queued, want exactly 1", len(owner))
	}
	if to, _ := owner[0]["to"].(string); to != letterhead.OwnerEmail {
		t.Errorf("owner copy addressed to %q", to)
	}
	if got := field(t, owner[0], "GuestEmail"); got != address {
		t.Errorf("the owner's copy names %q as the guest", got)
	}
}

// Asking the guest to pay sends them the amount and somewhere to pay it.
//
// Queued alongside the confirmation and in the same transaction, so a guest who
// is told they are booked and a guest who is told what they owe are never
// separated by a crash.
func TestChoosingAPaymentLinkQueuesOne(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, address := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(120),
		Checkout: date(123),
		Guests:   2,
		Name:     name,
		Email:    address,
		Payment:  console.SettleByLink,
	})
	if err != nil {
		t.Fatalf("taking a manual booking: %v", err)
	}

	queued := queuedMail(t, tx, email.PaymentRequest, made.Code)
	if len(queued) != 1 {
		t.Fatalf("%d payment requests queued, want exactly 1", len(queued))
	}
	if got := field(t, queued[0], "Amount"); got != email.Money(made.Quote.TotalCents) {
		t.Errorf("Amount is %q, want the whole total outstanding", got)
	}
	if got := field(t, queued[0], "PayURL"); got == "" {
		t.Error("the payment request carries no link to pay at")
	}

	// The default sends nothing. An owner who is taking cash must not have the
	// guest emailed an invoice behind their back.
	other, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(130),
		Checkout: date(133),
		Guests:   2,
		Name:     name,
		Email:    address,
	})
	if err != nil {
		t.Fatalf("taking the second booking: %v", err)
	}
	if queued := queuedMail(t, tx, email.PaymentRequest, other.Code); len(queued) != 0 {
		t.Errorf("%d payment requests queued for a booking settled offline, want none", len(queued))
	}
}

// A booking whose balance is already coming off a card on file must not also be
// sent an invoice. That is how a guest pays twice.
func TestAPaymentLinkIsRefusedWhenACardIsAlreadyScheduled(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, address := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(140),
		Checkout: date(143),
		Guests:   2,
		Name:     name,
		Email:    address,
	})
	if err != nil {
		t.Fatalf("taking a manual booking: %v", err)
	}

	// Made to look like a website booking whose deposit landed: confirmed, part
	// paid, and with the T-7 charge scheduled against a saved card.
	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET balance_charge_at = checkin - 7 WHERE code = $1`, made.Code); err != nil {
		t.Fatalf("scheduling a balance charge: %v", err)
	}

	var bad console.BadRequest
	if err := o.RequestPayment(ctx, made.Code); !errors.As(err, &bad) {
		t.Fatalf("err = %v, want a BadRequest refusing to invoice a scheduled charge", err)
	}
}

// And neither is a booking that has already been paid for.
func TestAPaymentLinkIsRefusedWhenNothingIsOutstanding(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, address := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(150),
		Checkout: date(153),
		Guests:   2,
		Name:     name,
		Email:    address,
	})
	if err != nil {
		t.Fatalf("taking a manual booking: %v", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET amount_paid_cents = total_cents WHERE code = $1`, made.Code); err != nil {
		t.Fatalf("marking it paid: %v", err)
	}

	var bad console.BadRequest
	if err := o.RequestPayment(ctx, made.Code); !errors.As(err, &bad) {
		t.Fatalf("err = %v, want a BadRequest", err)
	}
}

// A booking that loses the race for its room must not have told anybody it
// happened.
//
// The hook runs inside the transaction and after the room is claimed, so a
// failed booking takes its confirmation down with it rather than leaving a
// guest holding a message about a stay that does not exist.
func TestARefusedManualBookingQueuesNoMail(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, address := guest(t)

	first := console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(110),
		Checkout: date(113),
		Guests:   2,
		Name:     name,
		Email:    address,
	}
	if _, err := o.CreateBooking(ctx, first); err != nil {
		t.Fatalf("the first booking should succeed: %v", err)
	}

	second := first
	second.Checkin, second.Checkout = date(112), date(115)
	if _, err := o.CreateBooking(ctx, second); err == nil {
		t.Fatal("the overlapping booking should have been refused")
	}

	// One booking succeeded, so exactly one confirmation exists across both
	// attempts — counted by the guest's address rather than by code, since the
	// refused one never got one.
	var queued int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE kind = $1 AND payload->>'template' = $2 AND payload->>'to' = $3`,
		email.JobKind, email.BookingConfirmation, address).Scan(&queued); err != nil {
		t.Fatalf("counting queued mail: %v", err)
	}
	if queued != 1 {
		t.Errorf("%d confirmations queued for this guest, want 1 — the refused booking mailed them", queued)
	}
}

// The owner is allowed to take a booking the website would not offer. They are
// not allowed to double-book, and the exclusion constraint is what says so —
// not this form, and not the availability check that runs before it.
func TestAManualBookingCannotTakeARoomThatIsGone(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	first := console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(10),
		Checkout: date(13),
		Guests:   2,
		Name:     name,
		Email:    email,
	}
	if _, err := o.CreateBooking(ctx, first); err != nil {
		t.Fatalf("the first booking should succeed: %v", err)
	}

	// Overlapping, on the same room.
	second := first
	second.Checkin, second.Checkout = date(12), date(15)

	_, err := o.CreateBooking(ctx, second)
	var bad console.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("second booking error = %v, want a BadRequest the owner can read", err)
	}
}

// Today's board sorts one day's stays into the three questions an owner has
// every morning. A stay that touches the day in all three ways at once is not
// possible — checkin < checkout is a CHECK constraint — so the buckets cannot
// overlap.
func TestTodaySortsArrivalsDeparturesAndInHouse(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	// One stay arriving on the pivot day, one leaving on it, one straddling it.
	// Three rooms, because one room cannot hold all three at once.
	rooms, err := db.New(tx).ListRooms(ctx)
	if err != nil {
		t.Fatalf("listing rooms: %v", err)
	}
	if len(rooms) < 3 {
		t.Skip("this test needs three rooms in the seed")
	}

	pivot := day(20)
	stays := []struct {
		slug              string
		checkin, checkout string
	}{
		{rooms[0].Slug, date(20), date(23)}, // arrives on the pivot
		{rooms[1].Slug, date(17), date(20)}, // leaves on the pivot
		{rooms[2].Slug, date(18), date(23)}, // straddles it
	}

	for _, s := range stays {
		if _, err := o.CreateBooking(ctx, console.ManualBooking{
			RoomSlug: s.slug,
			Checkin:  s.checkin,
			Checkout: s.checkout,
			Guests:   2,
			Name:     name,
			Email:    email,
		}); err != nil {
			t.Fatalf("booking %s: %v", s.slug, err)
		}
	}

	board, err := o.Today(ctx, pivot)
	if err != nil {
		t.Fatalf("loading the board: %v", err)
	}

	if len(board.Arrivals) != 1 || board.Arrivals[0].Checkin != date(20) {
		t.Errorf("arrivals = %+v, want the one stay checking in on the pivot", board.Arrivals)
	}
	if len(board.Departures) != 1 || board.Departures[0].Checkout != date(20) {
		t.Errorf("departures = %+v, want the one stay checking out on the pivot", board.Departures)
	}
	if len(board.InHouse) != 1 {
		t.Errorf("in house = %+v, want the one stay straddling the pivot", board.InHouse)
	}
}

// The preview is the whole reason the rate editor is safe to touch, and its one
// requirement is that it changes nothing. If it ever committed, an owner asking
// "what would this do" would have already done it.
func TestPreviewingASeasonLeavesTheCalendarAlone(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()

	before := seasonCount(t, tx)

	change, err := o.PreviewSeason(ctx, console.SaveSeason{
		Name:     "Preview season",
		StartsOn: date(0),
		EndsOn:   date(30),
		Priority: 500, // above the seed, so it genuinely wins those nights
		Prices:   priceEveryRoom(t, tx, 44400),
	})
	if err != nil {
		t.Fatalf("previewing: %v", err)
	}

	if change.Nights == 0 {
		t.Error("the preview reported no change, but this season reprices a month at a price nothing else uses")
	}
	if after := seasonCount(t, tx); after != before {
		t.Errorf("seasons went from %d to %d — the preview committed", before, after)
	}
	if repriced := nightsAt(t, tx, 44400); repriced != 0 {
		t.Errorf("%d nights are priced at the previewed rate — the preview reached the calendar", repriced)
	}
}

// Saving does what the preview described: the season lands and the nightly
// calendar behind it is regenerated in the same transaction, so it cannot be
// left priced from a season that was never saved.
func TestSavingASeasonRepricesTheCalendar(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()

	if _, err := o.SaveSeasonAndRebuild(ctx, console.SaveSeason{
		Name:     "Saved season",
		StartsOn: date(0),
		EndsOn:   date(30),
		Priority: 500,
		Prices:   priceEveryRoom(t, tx, 55500),
	}); err != nil {
		t.Fatalf("saving: %v", err)
	}

	if repriced := nightsAt(t, tx, 55500); repriced == 0 {
		t.Error("no night is priced at the saved rate; the rebuild did not run")
	}
}

// A season that ends before it starts is refused with a sentence rather than a
// constraint violation, because the person reading it is holding a phone.
func TestASeasonCannotEndBeforeItStarts(t *testing.T) {
	o, _ := ops(t)

	_, err := o.PreviewSeason(context.Background(), console.SaveSeason{
		Name:     "Backwards",
		StartsOn: date(30),
		EndsOn:   date(0),
	})

	var bad console.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("error = %v, want a BadRequest", err)
	}
}

// Blocking goes through occupancy.Create, so it collides with a paid stay the
// same way a second guest would — and the owner is told the room is spoken for
// rather than shown a database error.
func TestBlockingARoomSomebodyHasIsRefused(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	if _, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(40),
		Checkout: date(43),
		Guests:   2,
		Name:     name,
		Email:    email,
	}); err != nil {
		t.Fatalf("booking: %v", err)
	}

	_, err := o.Block(ctx, console.NewBlock{
		RoomID: firstRoomID(t, tx),
		From:   date(41),
		To:     date(44),
		Reason: "family",
	})

	var bad console.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("blocking error = %v, want a BadRequest saying the room is taken", err)
	}
}

// Unblocking must not be a way to release a paid stay's room.
//
// The kind is in the DELETE's WHERE clause rather than checked in Go, so an id
// naming a booking's occupancy row matches nothing. Getting this wrong would put
// a room back on sale with the guest still arriving.
func TestUnblockingWillNotReleaseABooking(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(60),
		Checkout: date(63),
		Guests:   2,
		Name:     name,
		Email:    email,
	})
	if err != nil {
		t.Fatalf("booking: %v", err)
	}

	var occupancyID int64
	if err := tx.QueryRow(ctx, `
		SELECT o.id FROM room_occupancy o
		JOIN bookings b ON b.id = o.booking_id WHERE b.code = $1`, made.Code).
		Scan(&occupancyID); err != nil {
		t.Fatalf("reading the occupancy row: %v", err)
	}

	if err := o.Unblock(ctx, occupancyID); !errors.Is(err, console.ErrNotFound) {
		t.Fatalf("unblocking a booking's row: err = %v, want ErrNotFound", err)
	}

	var still int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM room_occupancy WHERE id = $1`, occupancyID).Scan(&still); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if still != 1 {
		t.Error("the booking's room was released by an unblock")
	}
}

// Copy that will not compile has to be refused at the save, while the owner is
// looking at it. The alternative is that it fails at send time — which is after
// a guest's card has been charged, with nothing in front of anybody to connect
// the failure to the sentence they typed.
func TestEmailCopyThatWillNotRenderIsRefused(t *testing.T) {
	o, _ := ops(t)

	err := o.SaveEmailCopy(context.Background(), "booking_confirmation",
		"Your booking", "Hello {{.Data.GuestName")

	var bad console.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("error = %v, want a BadRequest naming the problem", err)
	}
}

func TestEmailCopyForAMessageThatDoesNotExistIsRefused(t *testing.T) {
	o, _ := ops(t)

	err := o.SaveEmailCopy(context.Background(), "not_a_message", "Subject", "Body")

	var bad console.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("error = %v, want a BadRequest", err)
	}
}

// Emptying a page is a delete, not a row holding two empty strings — the same
// way resetting an email template is. No row is the absence of copy, and a
// second way to express that is a second thing every reader has to handle.
func TestEmptyingAPageDeletesItsRow(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()

	if err := o.SaveCopy(ctx, console.PageCopy{
		Slug: "about", Heading: "Our house", Body: "Since 1833.",
	}); err != nil {
		t.Fatalf("saving: %v", err)
	}

	page, err := o.PageFor(ctx, "about")
	if err != nil || !page.Written {
		t.Fatalf("after saving: page = %+v, err = %v", page, err)
	}

	if err := o.SaveCopy(ctx, console.PageCopy{Slug: "about"}); err != nil {
		t.Fatalf("emptying: %v", err)
	}

	var rows int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM page_copy WHERE slug = 'about'`).Scan(&rows); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rows != 0 {
		t.Error("emptying a page left a row behind")
	}

	// And reading it back is not an error: the page exists, it just has nothing
	// in that slot.
	page, err = o.PageFor(ctx, "about")
	if err != nil {
		t.Fatalf("reading an unwritten page: %v", err)
	}
	if page.Written {
		t.Error("an unwritten page reports itself as written")
	}
}

// A refund of nothing is refused rather than treated as "everything".
//
// payments.QueueRefund does read zero as the whole ledger, deliberately — that
// is decision #24's penalty-free path. An owner who leaves the amount box empty
// means nothing at all, and the difference between the two readings is a whole
// stay's money.
func TestARefundOfNothingIsRefused(t *testing.T) {
	o, tx := ops(t)
	ctx := context.Background()
	name, email := guest(t)

	made, err := o.CreateBooking(ctx, console.ManualBooking{
		RoomSlug: firstRoomSlug(t, tx),
		Checkin:  date(80),
		Checkout: date(83),
		Guests:   2,
		Name:     name,
		Email:    email,
	})
	if err != nil {
		t.Fatalf("booking: %v", err)
	}

	var bad console.BadRequest
	if err := o.Refund(ctx, made.Code, 0); !errors.As(err, &bad) {
		t.Fatalf("refunding zero: err = %v, want a BadRequest", err)
	}

	// And more than was collected is refused too — nothing has been paid on a
	// manual booking, so any amount is too much.
	if err := o.Refund(ctx, made.Code, 5000); !errors.As(err, &bad) {
		t.Fatalf("refunding more than was collected: err = %v, want a BadRequest", err)
	}
}

// A menu saves as one document, so the previous menu is the failure mode rather
// than a half-applied one.
func TestSavingTheMenuReplacesIt(t *testing.T) {
	o, _ := ops(t)
	ctx := context.Background()

	if err := o.SaveMenu(ctx, []console.MenuSection{{
		Name: "Starters",
		Items: []console.MenuItem{
			{Name: "Soup", PriceCents: 900, Available: true},
			{Name: "Sold out thing", PriceCents: 1200, Available: false},
		},
	}}); err != nil {
		t.Fatalf("saving the menu: %v", err)
	}

	// The console sees everything, so an item turned off can be turned back on.
	full, err := o.Menu(ctx)
	if err != nil || len(full) != 1 || len(full[0].Items) != 2 {
		t.Fatalf("console menu = %+v, err = %v; want both items", full, err)
	}

	// The public page sees only what the kitchen is actually serving.
	public, err := o.PublicMenu(ctx)
	if err != nil || len(public) != 1 || len(public[0].Items) != 1 {
		t.Fatalf("public menu = %+v, err = %v; want only the available item", public, err)
	}
	if public[0].Items[0].Name != "Soup" {
		t.Errorf("public menu shows %q, want the available item", public[0].Items[0].Name)
	}

	// Saving again replaces rather than appends.
	if err := o.SaveMenu(ctx, []console.MenuSection{{Name: "Mains"}}); err != nil {
		t.Fatalf("re-saving: %v", err)
	}
	again, _ := o.Menu(ctx)
	if len(again) != 1 || again[0].Name != "Mains" {
		t.Errorf("menu after re-saving = %+v, want only the new course", again)
	}
}

// A room claiming to be accessible without saying what makes it accessible is a
// promise a guest plans a trip around. The database refuses it too; this refuses
// it in a sentence first.
func TestARoomCannotClaimAccessibilityWithoutNamingAFeature(t *testing.T) {
	o, _ := ops(t)
	ctx := context.Background()

	rooms, err := o.Rooms(ctx)
	if err != nil || len(rooms) == 0 {
		t.Fatalf("loading rooms: %v", err)
	}

	room := rooms[0]
	room.IsAccessible = true
	room.AccessibilityFeatures = nil

	var bad console.BadRequest
	if err := o.SaveRoom(ctx, room); !errors.As(err, &bad) {
		t.Fatalf("error = %v, want a BadRequest explaining the promise", err)
	}
}

// Photos without alt text are refused for the same reason the column is NOT
// NULL: a picture nobody can see described is a picture that is not there.
func TestAPhotoNeedsAltText(t *testing.T) {
	o, _ := ops(t)
	ctx := context.Background()

	rooms, err := o.Rooms(ctx)
	if err != nil || len(rooms) == 0 {
		t.Fatalf("loading rooms: %v", err)
	}

	room := rooms[0]
	room.Photos = []console.Photo{{Path: "/photos/a.jpg", Alt: "  "}}

	var bad console.BadRequest
	if err := o.SaveRoom(ctx, room); !errors.As(err, &bad) {
		t.Fatalf("error = %v, want a BadRequest about alt text", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func seasonCount(t *testing.T, tx pgx.Tx) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(context.Background(),
		`SELECT count(*) FROM rate_seasons`).Scan(&n); err != nil {
		t.Fatalf("counting seasons: %v", err)
	}
	return n
}

func nightsAt(t *testing.T, tx pgx.Tx, cents int) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(context.Background(),
		`SELECT count(*) FROM rate_calendar WHERE price_cents = $1`, cents).Scan(&n); err != nil {
		t.Fatalf("counting nights: %v", err)
	}
	return n
}

func priceEveryRoom(t *testing.T, tx pgx.Tx, cents int64) map[string]int64 {
	t.Helper()
	rooms, err := db.New(tx).ListRooms(context.Background())
	if err != nil {
		t.Fatalf("listing rooms: %v", err)
	}
	// Keyed by room id as a string, because that is how the grid arrives: JSON
	// object keys are strings whether anybody meant them to be or not.
	out := map[string]int64{}
	for _, r := range rooms {
		out[strconv.FormatInt(r.ID, 10)] = cents
	}
	return out
}
