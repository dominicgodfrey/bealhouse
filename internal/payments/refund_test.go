package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/email"
)

// refunder is a Gateway that returns money and remembers what it returned.
type refunder struct {
	seen []RefundRequest
	err  error
}

func (r *refunder) CreateIntent(context.Context, IntentRequest) (Intent, error) {
	return Intent{}, errors.New("not used here")
}

func (r *refunder) ChargeOffSession(context.Context, OffSessionRequest) (Intent, error) {
	return Intent{}, errors.New("not used here")
}

func (r *refunder) Refund(_ context.Context, in RefundRequest) (string, error) {
	r.seen = append(r.seen, in)
	if r.err != nil {
		return "", r.err
	}
	// Keyed the way the real adapter keys its idempotency, so a retry after a
	// partial failure produces the same id here too.
	return "re_" + in.IntentID, nil
}

// The case decision #24 exists for. The guest did not change their mind — the
// inn could not honour the stay — so everything goes back, penalty-free.
func TestMoneyForAResoldRoomIsQueuedForRefund(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	// The hold lapses and the room is sold to somebody else while this guest is
	// still on the card form.
	sweepHold(t, ctx, q, tx, made.Code)
	if _, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(stayStart),
		Checkout:         day(stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com", Phone: "603-555-0101"},
		AcceptedPolicies: true,
	}); err != nil {
		t.Fatalf("selling the room to somebody else: %v", err)
	}

	got, err := RecordCharge(ctx, tx, deposit(made, "pi_too_late"))
	if err != nil {
		t.Fatalf("recording the payment: %v", err)
	}
	if got.Outcome != RefundDue {
		t.Fatalf("outcome %q, want %q", got.Outcome, RefundDue)
	}
	if got.RefundCents != made.Quote.DepositCents {
		t.Errorf("owed %d back, want the %d paid", got.RefundCents, made.Quote.DepositCents)
	}

	// The refund is a queued job, not a value the caller was trusted to act on.
	// RecordCharge is idempotent, so a caller that failed to issue it would get
	// AlreadyApplied on the redelivery and the money would never go back.
	if n := queuedRefunds(t, ctx, tx, made.Code); n != 1 {
		t.Fatalf("%d refunds queued, want 1", n)
	}

	// Now run it.
	gw := &refunder{}
	if err := Refund(ctx, tx, gw, made.Code, 0); err != nil {
		t.Fatalf("refunding: %v", err)
	}

	if len(gw.seen) != 1 {
		t.Fatalf("%d refunds sent, want 1", len(gw.seen))
	}
	if gw.seen[0].AmountCents != made.Quote.DepositCents {
		t.Errorf("refunded %d, want the %d paid", gw.seen[0].AmountCents, made.Quote.DepositCents)
	}
	if gw.seen[0].IntentID != "pi_too_late" {
		t.Errorf("refunded against %q, want the intent that took the money", gw.seen[0].IntentID)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}

	// The gross only ever grows (decision #25). A refund is a row, never a
	// subtraction — reducing this would make a second cancellation compute a
	// smaller refund off an already-reduced figure.
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("amount_paid_cents is %d; a refund rewrote the gross", after.AmountPaidCents)
	}

	if got := queuedMail(t, ctx, tx, email.CancellationRefund, made.Code); len(got) != 1 {
		t.Errorf("%d cancellation notices queued, want 1", len(got))
	}
}

// The job is safe to run twice, which it has to be: the runner leases rather
// than deletes, so a process that dies mid-refund leaves work that comes back.
func TestRefundingTwiceReturnsTheMoneyOnce(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	sweepHold(t, ctx, q, tx, made.Code)
	if _, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(stayStart),
		Checkout:         day(stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com", Phone: "603-555-0101"},
		AcceptedPolicies: true,
	}); err != nil {
		t.Fatalf("selling the room to somebody else: %v", err)
	}
	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_too_late")); err != nil {
		t.Fatalf("recording the payment: %v", err)
	}

	gw := &refunder{}
	for i := range 3 {
		if err := Refund(ctx, tx, gw, made.Code, 0); err != nil {
			t.Fatalf("refund run %d: %v", i+1, err)
		}
	}

	var refunds int
	var total int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(p.amount_cents), 0)
		FROM payments p JOIN bookings b ON b.id = p.booking_id
		WHERE b.code = $1 AND p.kind = 'refund'`, made.Code,
	).Scan(&refunds, &total); err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if refunds != 1 {
		t.Errorf("%d refund rows, want 1", refunds)
	}
	if total != made.Quote.DepositCents {
		t.Errorf("refunded %d in total, want %d", total, made.Quote.DepositCents)
	}
	if got := queuedMail(t, ctx, tx, email.CancellationRefund, made.Code); len(got) != 1 {
		t.Errorf("%d cancellation notices queued after three runs, want 1", len(got))
	}
}

// A processor that cannot be reached must fail the job so the runner retries.
// Losing this silently would leave a guest charged for a room they cannot have.
func TestAFailedRefundStaysOwed(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	sweepHold(t, ctx, q, tx, made.Code)
	if _, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(stayStart),
		Checkout:         day(stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com", Phone: "603-555-0101"},
		AcceptedPolicies: true,
	}); err != nil {
		t.Fatalf("selling the room to somebody else: %v", err)
	}
	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_too_late")); err != nil {
		t.Fatalf("recording the payment: %v", err)
	}

	gw := &refunder{err: errors.New("stripe is unreachable")}
	if err := Refund(ctx, tx, gw, made.Code, 0); err == nil {
		t.Fatal("an unreachable processor was reported as a completed refund")
	}

	// Nothing recorded: the money has not moved, so the ledger must not say it
	// has. The job stays queued and retries.
	var refunds int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM payments p JOIN bookings b ON b.id = p.booking_id
		WHERE b.code = $1 AND p.kind = 'refund'`, made.Code,
	).Scan(&refunds); err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if refunds != 0 {
		t.Errorf("%d refunds recorded for money that never moved", refunds)
	}

	// The guest has been told once, by the transaction that cancelled their
	// stay, and is not told again by each attempt to send the money.
	//
	// The message belongs to the cancellation rather than to the transfer: the
	// booking really is cancelled at that point whatever Stripe is doing, and a
	// guest whose room was resold hearing nothing until the processor recovers
	// is the worse silence. It also has to be one message — this used to be
	// queued per refunded intent, so a stay that paid a deposit and then a
	// balance told the guest twice, each time naming part of the money.
	if got := queuedMail(t, ctx, tx, email.CancellationRefund, made.Code); len(got) != 1 {
		t.Errorf("%d cancellation emails queued, want exactly 1", len(got))
	}
}

// queuedRefunds counts the refund jobs waiting for one booking.
func queuedRefunds(t *testing.T, ctx context.Context, tx pgx.Tx, code string) int {
	t.Helper()

	var n int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM jobs WHERE kind = $1 AND payload->>'code' = $2",
		RefundJobKind, code,
	).Scan(&n); err != nil {
		t.Fatalf("counting queued refunds: %v", err)
	}
	return n
}

var _ Gateway = (*refunder)(nil)
