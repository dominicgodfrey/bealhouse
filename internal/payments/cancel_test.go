package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/email"
	"bealhouse/internal/pricing"
)

// confirmed is a paid-for stay at today+300, ready to be cancelled.
//
// The day the guest cancels is passed to Cancel rather than baked into the
// booking, so both sides of the seven-day boundary can be tested without moving
// a stay out of this package's stretch of calendar (CLAUDE.md).
func confirmed(t *testing.T, ctx context.Context, tx pgx.Tx) booking.Booking {
	t.Helper()

	made := held(t, ctx, tx)
	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit_"+made.Code)); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}
	return made
}

// The ordinary case: cancelled in good time, everything back except what the
// card processor kept (decisions #9 and #26).
func TestCancellingInTimeRefundsAllButTheProcessorsCut(t *testing.T) {
	ctx, q, tx := setup(t)
	made := confirmed(t, ctx, tx)

	got, err := Cancel(ctx, tx, made.Code, day(stayStart-30))
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}

	if got.Late {
		t.Error("a cancellation a month out was treated as late")
	}

	// Three percent of what was collected, rounded up, and nothing more: the
	// inn is never short and the guest is never charged a penalty they did not
	// incur.
	paid := made.Quote.DepositCents
	fee := pricing.ProcessingFee(paid, pricing.Rate(3000))
	if got.RetainedCents != fee {
		t.Errorf("retained %d, want the %d processing fee", got.RetainedCents, fee)
	}
	if got.RefundCents != paid-fee {
		t.Errorf("refunded %d, want %d", got.RefundCents, paid-fee)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}

	// The gross only ever grows. Reducing it would make a second cancellation
	// compute a smaller refund off an already-reduced figure (decision #25).
	if after.AmountPaidCents != paid {
		t.Errorf("amount_paid_cents = %d, want the %d gross unchanged", after.AmountPaidCents, paid)
	}

	// And the room is back on sale, which is the part somebody else is waiting
	// for.
	if kinds := occupancyKinds(t, ctx, tx, after.ID); len(kinds) != 0 {
		t.Errorf("the room is still occupied by %v", kinds)
	}
}

// Inside seven days the deposit is forfeit, and a stay that has only paid its
// deposit therefore gets nothing back — zero rather than a negative the inn
// would try to collect.
func TestCancellingLateForfeitsTheDeposit(t *testing.T) {
	ctx, q, tx := setup(t)
	made := confirmed(t, ctx, tx)

	got, err := Cancel(ctx, tx, made.Code, day(stayStart-3))
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}

	if !got.Late {
		t.Error("a cancellation three days out was not treated as late")
	}
	if got.RefundCents != 0 {
		t.Errorf("refunded %d, want nothing", got.RefundCents)
	}

	// Nothing to send, so nothing queued. A job to move zero dollars is one the
	// processor refuses for no reason.
	if n := queuedRefunds(t, ctx, tx, made.Code); n != 0 {
		t.Errorf("%d refunds queued for a refund of nothing", n)
	}

	// The guest is still told. A cancellation they made is one they are owed
	// confirmation of, whether or not money moves.
	if got := queuedMail(t, ctx, tx, email.CancellationRefund, made.Code); len(got) != 1 {
		t.Errorf("%d cancellation emails queued, want 1", len(got))
	}

	if after := state(t, ctx, q, made.Code); after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}
}

// Exactly seven days out is in time. The boundary is on the guest's side, and
// it is the same T-7 the balance charge uses.
func TestSevenDaysOutIsStillInTime(t *testing.T) {
	ctx, _, tx := setup(t)
	made := confirmed(t, ctx, tx)

	got, err := Cancel(ctx, tx, made.Code, day(stayStart-pricing.BalanceLeadDays))
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	if got.Late {
		t.Error("cancelling exactly seven days out was treated as late")
	}
}

// The refund is a queued job carrying the amount, not a figure the caller was
// trusted to act on — and not one the job recomputes, because which side of T-7
// the guest cancelled on cannot be worked out again later.
func TestTheRefundIsQueuedForTheAmountDecidedAtCancellation(t *testing.T) {
	ctx, _, tx := setup(t)
	made := confirmed(t, ctx, tx)

	got, err := Cancel(ctx, tx, made.Code, day(stayStart-30))
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	if n := queuedRefunds(t, ctx, tx, made.Code); n != 1 {
		t.Fatalf("%d refunds queued, want 1", n)
	}

	gw := &refunder{}
	if err := Refund(ctx, tx, gw, made.Code, got.RefundCents); err != nil {
		t.Fatalf("refunding: %v", err)
	}

	if len(gw.seen) != 1 {
		t.Fatalf("%d refunds sent, want 1", len(gw.seen))
	}
	if gw.seen[0].AmountCents != got.RefundCents {
		t.Errorf("sent %d, want the %d the guest was quoted", gw.seen[0].AmountCents, got.RefundCents)
	}

	// The processor's cut stays with the inn: less went back than came in.
	if gw.seen[0].AmountCents >= made.Quote.DepositCents {
		t.Errorf("refunded %d of the %d collected — the processing fee was given away",
			gw.seen[0].AmountCents, made.Quote.DepositCents)
	}
}

// Running the job twice must not send the money twice. The second run finds the
// refund already in the ledger and does nothing.
func TestARetriedRefundJobDoesNotPayTwice(t *testing.T) {
	ctx, _, tx := setup(t)
	made := confirmed(t, ctx, tx)

	got, err := Cancel(ctx, tx, made.Code, day(stayStart-30))
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}

	gw := &refunder{}
	for range 3 {
		if err := Refund(ctx, tx, gw, made.Code, got.RefundCents); err != nil {
			t.Fatalf("refunding: %v", err)
		}
	}

	if len(gw.seen) != 1 {
		t.Errorf("%d refunds sent over three runs, want 1", len(gw.seen))
	}
}

// A stay that was never paid for has no refund to compute and no room to hand
// back — its hold lapses on its own.
func TestAnUnpaidHoldIsNotCancellableThisWay(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := Cancel(ctx, tx, made.Code, day(stayStart-30)); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling a pending hold gave %v, want ErrNotCancellable", err)
	}
}

func TestCancellingTwiceIsRefused(t *testing.T) {
	ctx, _, tx := setup(t)
	made := confirmed(t, ctx, tx)

	if _, err := Cancel(ctx, tx, made.Code, day(stayStart-30)); err != nil {
		t.Fatalf("cancelling: %v", err)
	}

	// Not merely idempotent: a second cancellation must not queue a second
	// refund off the gross, which has not been reduced (decision #25).
	if _, err := Cancel(ctx, tx, made.Code, day(stayStart-30)); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling twice gave %v, want ErrNotCancellable", err)
	}
	if n := queuedRefunds(t, ctx, tx, made.Code); n != 1 {
		t.Errorf("%d refunds queued after two cancellations, want 1", n)
	}
}

// Once the guest has arrived, decision #9's arithmetic no longer describes
// anything: it would call this late and hand back half the money for a stay
// that is being consumed. That is a conversation with the owner.
func TestAStayThatHasBegunIsRefused(t *testing.T) {
	ctx, _, tx := setup(t)
	made := confirmed(t, ctx, tx)

	if _, err := Cancel(ctx, tx, made.Code, day(stayStart)); !errors.Is(err, ErrStayUnderway) {
		t.Errorf("cancelling on the arrival day gave %v, want ErrStayUnderway", err)
	}
	if _, err := Cancel(ctx, tx, made.Code, day(stayStart+1)); !errors.Is(err, ErrStayUnderway) {
		t.Errorf("cancelling mid-stay gave %v, want ErrStayUnderway", err)
	}
}

func TestCancellingAnUnknownBookingIsNotFound(t *testing.T) {
	ctx, _, tx := setup(t)

	if _, err := Cancel(ctx, tx, "BH-NOSUCH", day(0)); !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("cancelling nothing gave %v, want ErrBookingNotFound", err)
	}
}
