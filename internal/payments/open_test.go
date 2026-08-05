package payments

import (
	"context"
	"errors"
	"testing"

	db "bealhouse/internal/db/gen"
)

// spy is a Gateway that answers plausibly and remembers what it was asked.
//
// The whole point of Open is what it decides to charge, so the assertions are
// all about the request that reached here.
type spy struct {
	seen []IntentRequest
	err  error
}

func (s *spy) CreateIntent(_ context.Context, in IntentRequest) (Intent, error) {
	s.seen = append(s.seen, in)
	if s.err != nil {
		return Intent{}, s.err
	}
	id := "pi_spy_" + in.BookingCode
	return Intent{ID: id, ClientSecret: id + "_secret_spy"}, nil
}

func (s *spy) ChargeOffSession(context.Context, OffSessionRequest) (Intent, error) {
	return Intent{}, errors.New("not used here")
}

func (s *spy) Refund(context.Context, RefundRequest) (string, error) {
	return "", errors.New("not used here")
}

func (s *spy) last(t *testing.T) IntentRequest {
	t.Helper()
	if len(s.seen) == 0 {
		t.Fatal("the processor was never asked to open a payment")
	}
	return s.seen[len(s.seen)-1]
}

// The rule the whole endpoint exists to enforce: a guest never names their own
// price. The amount comes from the booking's own snapshot, which for an arrival
// more than a week out is the 50% deposit (decision #8).
func TestOpenChargesTheDepositFromTheBookingsOwnSnapshot(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	gw := &spy{}
	opened, err := Open(ctx, q, gw, made.Code)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	if opened.AmountCents != made.Quote.DepositCents {
		t.Errorf("opened for %d, want the %d deposit", opened.AmountCents, made.Quote.DepositCents)
	}
	if got := gw.last(t); got.AmountCents != made.Quote.DepositCents {
		t.Errorf("the processor was asked for %d, want %d", got.AmountCents, made.Quote.DepositCents)
	}

	// A T-7 charge is coming, so the card has to survive the browser closing.
	if !gw.last(t).SaveCard {
		t.Error("the card was not kept, so the balance could never be charged")
	}
	if got := gw.last(t).Kind; got != KindDeposit {
		t.Errorf("kind %q, want %q", got, KindDeposit)
	}

	// The booking code reaches the processor as metadata. It is what a webhook
	// uses to find the stay, and it is never read back from the browser.
	if got := gw.last(t).BookingCode; got != made.Code {
		t.Errorf("the processor was told %q, want %q", got, made.Code)
	}

	// Recorded on the booking, which is what stops the sweeper expiring a stay
	// whose guest is mid-payment.
	if after := state(t, ctx, q, made.Code); after.PaymentIntentID != opened.IntentID {
		t.Errorf("payment_intent_id is %q, want %q", after.PaymentIntentID, opened.IntentID)
	}
}

// Decision #7: an arrival inside the T-8 window pays the whole total up front.
// A NULL balance_charge_at is that flag, and reading it as a missing value would
// charge half and leave the rest uncollectable.
func TestOpenChargesAShortNoticeStayInFull(t *testing.T) {
	ctx, q, tx := setup(t)
	made := shortNotice(t, ctx, tx)

	gw := &spy{}
	opened, err := Open(ctx, q, gw, made.Code)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	if opened.AmountCents != made.Quote.TotalCents {
		t.Errorf("opened for %d, want the %d total", opened.AmountCents, made.Quote.TotalCents)
	}
	if got := gw.last(t).Kind; got != KindFull {
		t.Errorf("kind %q, want %q", got, KindFull)
	}

	// Nothing will ever be charged again, so nothing has to be kept. Holding a
	// reusable card for a guest the inn will never bill again is a liability
	// with no purpose.
	if gw.last(t).SaveCard {
		t.Error("a stay paid in full kept a card it can never charge")
	}
}

// A stay already confirmed takes its balance off-session through the T-7 job,
// never through a page. Opening one here would let anyone with a booking code
// authorise a second charge against a card that has already paid.
func TestOpenRefusesAConfirmedStay(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	gw := &spy{}
	if _, err := Open(ctx, q, gw, made.Code); !errors.Is(err, ErrNotPayable) {
		t.Errorf("opening a confirmed stay gave %v, want ErrNotPayable", err)
	}
	if len(gw.seen) != 0 {
		t.Error("the processor was called for a stay that had already paid")
	}
}

// The hold ran out and the room went back on sale. Taking money now would be
// charging a guest for a room somebody else can already book.
func TestOpenRefusesAnExpiredBooking(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)
	sweepHold(t, ctx, q, tx, made.Code)

	gw := &spy{}
	if _, err := Open(ctx, q, gw, made.Code); !errors.Is(err, ErrNotPayable) {
		t.Errorf("opening an expired booking gave %v, want ErrNotPayable", err)
	}
	if len(gw.seen) != 0 {
		t.Error("the processor was called for a room that is back on sale")
	}
}

func TestOpenRefusesAnUnknownBooking(t *testing.T) {
	ctx, q, _ := setup(t)

	if _, err := Open(ctx, q, &spy{}, "BH-NOPE"); !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("opening an unknown booking gave %v, want ErrBookingNotFound", err)
	}
}

// Whatever is still outstanding, not the original split. A guest who paid part
// of their deposit and came back owes the remainder, and asking for the whole
// deposit again would charge them twice for the part that landed.
func TestOpenAsksOnlyForWhatIsStillOutstanding(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	part := made.Quote.DepositCents / 3
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_partial",
		Kind:        KindDeposit,
		AmountCents: part,
	}); err != nil {
		t.Fatalf("recording a partial payment: %v", err)
	}

	gw := &spy{}
	opened, err := Open(ctx, q, gw, made.Code)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	if want := made.Quote.DepositCents - part; opened.AmountCents != want {
		t.Errorf("opened for %d, want the %d shortfall", opened.AmountCents, want)
	}
}

// Without a processor the honest answer is that money cannot move. Nothing is
// written to the booking on the way past.
func TestOpenWithoutAProcessorRecordsNothing(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	gw := &spy{err: ErrGatewayDisabled}
	if _, err := Open(ctx, q, gw, made.Code); !errors.Is(err, ErrGatewayDisabled) {
		t.Fatalf("opening gave %v, want ErrGatewayDisabled", err)
	}

	if after := state(t, ctx, q, made.Code); after.PaymentIntentID != "" {
		t.Errorf("a failed open left payment_intent_id set to %q", after.PaymentIntentID)
	}
}

var _ Gateway = (*spy)(nil)

var _ = db.GetBookingForPaymentRow{}
