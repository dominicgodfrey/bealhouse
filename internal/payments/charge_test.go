package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
)

// offSession is a Gateway that answers off-session charges and records them.
type offSession struct {
	seen       []OffSessionRequest
	decline    bool            // decline everything
	declineFor map[string]bool // or just these bookings
	declined   string          // the intent id a decline names, if any
	err        error           // anything other than a decline: a timeout, a bad key
}

func (o *offSession) CreateIntent(context.Context, IntentRequest) (Intent, error) {
	return Intent{}, errors.New("not used here")
}

func (o *offSession) ChargeOffSession(_ context.Context, in OffSessionRequest) (Intent, error) {
	o.seen = append(o.seen, in)
	if o.err != nil {
		return Intent{}, o.err
	}
	if o.decline || o.declineFor[in.BookingCode] {
		return Intent{}, &Declined{IntentID: o.declined, Reason: "your card was declined"}
	}
	return Intent{ID: "pi_balance_" + in.BookingCode}, nil
}

func (o *offSession) Refund(context.Context, RefundRequest) (string, error) {
	return "", errors.New("not used here")
}

// dueForCharge is a confirmed stay with a saved card, sitting at its T-7.
func dueForCharge(t *testing.T, ctx context.Context, tx pgx.Tx) (code string, balance int64, total int64) {
	t.Helper()

	made := held(t, ctx, tx)

	charge := deposit(made, "pi_deposit_"+made.Code)
	charge.CustomerID = "cus_saved"
	charge.PaymentMethodID = "pm_saved"
	if _, err := RecordCharge(ctx, tx, charge); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	// The stay is at today+300, so its own T-7 is months out. Pulling the charge
	// date to today is what the booking looks like on the day the money is due,
	// without moving the stay into another package's calendar.
	//
	// Today at the inn, passed in, and deliberately not Postgres' current_date:
	// the container runs in UTC, so after 8pm Eastern that is tomorrow, and the
	// scan these tests then run for today found nothing. The whole system dates
	// in America/New_York (internal/civil) and a test reaching in with raw SQL
	// has to as well.
	dueToday(t, ctx, tx, made.Code)
	return made.Code, made.Quote.BalanceCents, made.Quote.TotalCents
}

// bookAnotherRoom puts a second stay in the same window, on a different room,
// also due its balance today.
func bookAnotherRoom(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	made, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "blue-room",
		Checkin:          day(stayStart),
		Checkout:         day(stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("holding a second room: %v", err)
	}

	charge := deposit(made, "pi_deposit_"+made.Code)
	charge.CustomerID = "cus_second"
	charge.PaymentMethodID = "pm_second"
	if _, err := RecordCharge(ctx, tx, charge); err != nil {
		t.Fatalf("recording the second deposit: %v", err)
	}
	dueToday(t, ctx, tx, made.Code)
	return made.Code
}

// dueToday brings a booking's balance charge forward to today at the inn.
func dueToday(t *testing.T, ctx context.Context, tx pgx.Tx, code string) {
	t.Helper()

	if _, err := tx.Exec(ctx,
		"UPDATE bookings SET balance_charge_at = $2 WHERE code = $1", code, day(0),
	); err != nil {
		t.Fatalf("bringing the charge date forward: %v", err)
	}
}

// The ordinary path, and the one decision #6 is built around.
func TestBalanceIsChargedToTheCardSavedAtBooking(t *testing.T) {
	ctx, q, tx := setup(t)
	code, balance, total := dueForCharge(t, ctx, tx)

	gw := &offSession{}
	collected, err := ChargeBalances(ctx, q, tx, gw, day(0))
	if err != nil {
		t.Fatalf("charging: %v", err)
	}
	if collected < 1 {
		t.Fatal("nothing was collected for a stay due its balance")
	}

	// The card the webhook wrote down when the deposit landed. Without it there
	// is nothing to charge, and the guest finds out at the door.
	var charged *OffSessionRequest
	for i := range gw.seen {
		if gw.seen[i].BookingCode == code {
			charged = &gw.seen[i]
		}
	}
	if charged == nil {
		t.Fatal("the processor was never asked to charge this stay")
	}
	if charged.CustomerID != "cus_saved" || charged.PaymentMethodID != "pm_saved" {
		t.Errorf("charged %q/%q, want the card saved at booking", charged.CustomerID, charged.PaymentMethodID)
	}
	if charged.AmountCents != balance {
		t.Errorf("charged %d, want the %d balance", charged.AmountCents, balance)
	}

	after := state(t, ctx, q, code)
	if after.AmountPaidCents != total {
		t.Errorf("collected %d in total, want the %d the stay costs", after.AmountPaidCents, total)
	}
	if after.Status != "confirmed" {
		t.Errorf("status %q, want the stay left confirmed", after.Status)
	}

	// A receipt, not a second confirmation.
	if got := queuedMail(t, ctx, tx, email.BalanceReceipt, code); len(got) != 1 {
		t.Errorf("%d receipts queued, want 1", len(got))
	}
	if got := queuedMail(t, ctx, tx, email.BookingConfirmation, code); len(got) != 1 {
		t.Errorf("%d confirmations queued, want only the one from the deposit", len(got))
	}
}

// Running twice must not charge twice. The scan drops a stay the moment its
// balance lands, and the ledger keys on the intent id underneath that.
func TestChargingTwiceCollectsOnce(t *testing.T) {
	ctx, q, tx := setup(t)
	code, _, total := dueForCharge(t, ctx, tx)

	gw := &offSession{}
	for i := range 3 {
		if _, err := ChargeBalances(ctx, q, tx, gw, day(0)); err != nil {
			t.Fatalf("charging run %d: %v", i+1, err)
		}
	}

	if after := state(t, ctx, q, code); after.AmountPaidCents != total {
		t.Errorf("collected %d, want %d — the balance was taken more than once", after.AmountPaidCents, total)
	}
	if got := queuedMail(t, ctx, tx, email.BalanceReceipt, code); len(got) != 1 {
		t.Errorf("%d receipts queued after three runs, want 1", len(got))
	}
}

// A refused card leaves the stay confirmed — the guest is still arriving, there
// is just money outstanding — and raises the flag the owner acts on.
func TestDeclinedBalanceFlagsAndMailsWithoutCancelling(t *testing.T) {
	ctx, q, tx := setup(t)
	code, _, _ := dueForCharge(t, ctx, tx)

	gw := &offSession{decline: true, declined: "pi_declined"}
	collected, err := ChargeBalances(ctx, q, tx, gw, day(0))
	if err != nil {
		// A decline is an outcome, not an error. Returned as one, the runner
		// would retry the batch hourly and mail the same guest every time.
		t.Fatalf("a declined card came back as a job failure: %v", err)
	}
	if collected != 0 {
		t.Errorf("%d collected from a declined card", collected)
	}

	after := state(t, ctx, q, code)
	if after.Status != "confirmed" {
		t.Errorf("status %q — a declined balance must not cancel the stay", after.Status)
	}

	var failedAt *string
	if err := tx.QueryRow(ctx,
		"SELECT balance_charge_failed_at::text FROM bookings WHERE code = $1", code,
	).Scan(&failedAt); err != nil {
		t.Fatalf("reading the failure flag: %v", err)
	}
	if failedAt == nil {
		t.Error("the owner has no flag for a balance that was refused")
	}

	if got := queuedMail(t, ctx, tx, email.BalanceFailed, code); len(got) != 1 {
		t.Fatalf("%d failure notices queued, want 1", len(got))
	}

	// The attempt is in the ledger. It is what an owner argues from when the
	// guest says the card was fine (decision #28).
	var attempts int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN bookings b ON b.id = p.booking_id
		 WHERE b.code = $1 AND p.status = 'failed'`, code,
	).Scan(&attempts); err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if attempts != 1 {
		t.Errorf("%d failed attempts recorded, want 1", attempts)
	}
}

// A stay that already failed its charge drops out of the scan, so the guest is
// not mailed hourly about the same refused card.
func TestADeclinedStayIsNotChasedEveryHour(t *testing.T) {
	ctx, q, tx := setup(t)
	code, _, _ := dueForCharge(t, ctx, tx)

	gw := &offSession{decline: true, declined: "pi_declined"}
	for range 3 {
		if _, err := ChargeBalances(ctx, q, tx, gw, day(0)); err != nil {
			t.Fatalf("charging: %v", err)
		}
	}

	if got := queuedMail(t, ctx, tx, email.BalanceFailed, code); len(got) != 1 {
		t.Errorf("%d failure notices queued after three runs, want 1", len(got))
	}
}

// A timeout is not a decline. It has to fail the job so the runner retries it,
// because the money may well have moved and this server does not know.
func TestANetworkFailureFailsTheJobRatherThanTheCard(t *testing.T) {
	ctx, q, tx := setup(t)
	code, _, _ := dueForCharge(t, ctx, tx)

	gw := &offSession{err: errors.New("connection reset")}
	if _, err := ChargeBalances(ctx, q, tx, gw, day(0)); err == nil {
		t.Fatal("a network failure was swallowed; the job would report success")
	}

	// Nothing is flagged and nobody is told: as far as this server knows, the
	// charge may have gone through.
	if got := queuedMail(t, ctx, tx, email.BalanceFailed, code); len(got) != 0 {
		t.Errorf("%d guests told their card failed after a network error", len(got))
	}
	var failedAt *string
	if err := tx.QueryRow(ctx,
		"SELECT balance_charge_failed_at::text FROM bookings WHERE code = $1", code,
	).Scan(&failedAt); err != nil {
		t.Fatalf("reading the failure flag: %v", err)
	}
	if failedAt != nil {
		t.Error("a network error was recorded as a declined card")
	}
}

// One guest's refused card must not stop the inn collecting from anybody else
// that day.
func TestOneDeclineDoesNotStopTheRest(t *testing.T) {
	ctx, q, tx := setup(t)
	first, _, _ := dueForCharge(t, ctx, tx)

	// A second stay, in the same window but a different room.
	second := bookAnotherRoom(t, ctx, tx)

	gw := &offSession{
		declineFor: map[string]bool{first: true},
		declined:   "pi_declined",
	}
	collected, err := ChargeBalances(ctx, q, tx, gw, day(0))
	if err != nil {
		t.Fatalf("charging: %v", err)
	}
	if collected < 1 {
		t.Fatal("one refused card stopped the whole batch")
	}

	if got := queuedMail(t, ctx, tx, email.BalanceFailed, first); len(got) != 1 {
		t.Errorf("the refused card produced %d failure notices, want 1", len(got))
	}
	if got := queuedMail(t, ctx, tx, email.BalanceReceipt, second); len(got) != 1 {
		t.Errorf("the second stay got %d receipts, want 1 — it was charged behind a decline", len(got))
	}
}

// A stay due its balance with no card on file cannot be charged. It should be
// unreachable — the webhook writes the card down in the transaction that
// confirms the stay — but the alternative to handling it is a balance that
// quietly never gets collected.
func TestAStayWithNoCardOnFileIsFlaggedRatherThanSkipped(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	// Confirmed without a card: the deposit carried none.
	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit_nocard")); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}
	dueToday(t, ctx, tx, made.Code)

	gw := &offSession{}
	if _, err := ChargeBalances(ctx, q, tx, gw, day(0)); err != nil {
		t.Fatalf("charging: %v", err)
	}

	if len(gw.seen) != 0 {
		t.Error("the processor was asked to charge a card that does not exist")
	}
	if got := queuedMail(t, ctx, tx, email.BalanceFailed, made.Code); len(got) != 1 {
		t.Errorf("%d failure notices queued, want 1", len(got))
	}
}

var _ Gateway = (*offSession)(nil)

var _ = db.GetBookingForPaymentRow{}
