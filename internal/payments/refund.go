package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/jobs"
)

// RefundJobKind returns money for a stay the inn could not honour.
const RefundJobKind = "payment.refund"

// refundPayload is what the job carries: the booking, and how much of it.
//
// AmountCents is zero on decision #24's path, which returns everything and can
// therefore work out the figure from the ledger when the job runs — the version
// that stays right if a second payment landed in between.
//
// A guest's own cancellation cannot do that. What the inn keeps depends on which
// side of T-7 the guest cancelled (decision #9), and a job that recomputed that
// when it ran would give a different answer to a retry that happened to cross the
// boundary. So that amount is decided once, in the transaction that cancelled the
// stay, and carried.
type refundPayload struct {
	Code        string `json:"code"`
	AmountCents int64  `json:"amountCents,omitempty"`
}

// QueueRefund schedules the money going back. Zero means everything collected.
//
// Called inside the transaction that cancelled the booking, so a stay that was
// cancelled without a refund queued behind it is not a state this system can
// reach. The unique key is the booking, so queueing twice for the same stay
// leaves one job.
func QueueRefund(ctx context.Context, q *db.Queries, code string, amountCents int64) error {
	if _, err := jobs.Enqueue(ctx, q, jobs.Job{
		Kind:      RefundJobKind,
		Payload:   refundPayload{Code: code, AmountCents: amountCents},
		UniqueKey: RefundJobKind + ":" + code,
	}); err != nil {
		return fmt.Errorf("payments: queueing the refund for %s: %w", code, err)
	}
	return nil
}

// RefundJob sends back everything collected against a cancelled stay.
//
// Decision #24: a guest whose room was resold while they were paying gets the
// whole amount back, penalty-free. They did not change their mind, so decision
// #9's forfeit does not apply.
//
// Each payment is refunded against the intent that took it, so a stay that paid
// a deposit and then a balance produces two refunds rather than one that Stripe
// would reject. The gateway keys each on the booking, the intent and the amount,
// so a job that failed after refunding at Stripe but before recording it here
// gets the same refund back on its retry instead of sending the money twice.
func RefundJob(beginner Beginner, gw Gateway) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var in refundPayload
		if err := decodePayload(payload, &in); err != nil {
			return err
		}
		return Refund(ctx, beginner, gw, in.Code, in.AmountCents)
	}
}

// Refund sends money back against a booking. Zero means everything collected.
//
// Each payment is refunded against the intent that took it, so a stay that paid
// a deposit and then a balance produces two refunds rather than one that Stripe
// would reject. A partial refund is spread over those intents in ledger order,
// filling each before moving on.
//
// That order is a function of the target and the payments already recorded, both
// of which are fixed by the time this runs — so a retry computes the same
// division and asks the gateway for the same amounts against the same intents.
// The gateway keys each call on the booking, the intent and the amount, so a job
// that failed after refunding at Stripe but before recording it gets the same
// refund object back instead of sending the money twice.
func Refund(ctx context.Context, beginner Beginner, gw Gateway, code string, targetCents int64) error {
	ledger, err := collectedPayments(ctx, beginner, code)
	if err != nil {
		return err
	}

	for _, part := range ledger.allocate(targetCents) {
		refundID, err := gw.Refund(ctx, RefundRequest{
			BookingCode: code,
			IntentID:    part.StripeID,
			AmountCents: part.AmountCents,
		})
		if err != nil {
			return fmt.Errorf("payments: refunding %s: %w", code, err)
		}

		// RecordRefund writes the row, cancels the stay and releases the room.
		// The last two are already true here and are idempotent; the row is what
		// this call is for, and its unique index is what makes running the whole
		// job again cost nothing.
		if _, err := RecordRefund(ctx, beginner, Charge{
			BookingCode: code,
			StripeID:    refundID,
			Kind:        KindRefund,
			AmountCents: part.AmountCents,
		}); err != nil {
			return err
		}
		slog.Info("refunded", "booking", code,
			"amount_cents", part.AmountCents, "refund", refundID)
	}
	return nil
}

// collected is one payment, or one slice of one that has to go back.
type collected struct {
	StripeID    string
	AmountCents int64
}

// ledger is what has been taken against a booking and what has already gone
// back, in the order the payments were made.
type ledger struct {
	charges []collected

	// alreadyRefunded holds the amount of each refund recorded so far. Amounts
	// rather than intents, because a refund row's stripe_id is the refund's own
	// id and Stripe does not carry the intent back on it.
	alreadyRefunded []int64
}

// allocate divides a refund over the payments that have to fund it, skipping the
// parts already sent.
//
// Payments are filled in ledger order rather than proportionally: a $600 refund
// of a $500 deposit and a $700 balance is $500 against the first intent and $100
// against the second, not $250 and $350. Whole intents refunded whole are easier
// to reconcile against a statement, and the division has to be reproducible
// rather than merely fair — a retry that allocated differently would ask Stripe
// for amounts it had not seen, and every one of them would be a fresh refund.
//
// A target of zero means everything, which is decision #24's penalty-free path.
func (l ledger) allocate(targetCents int64) []collected {
	if targetCents <= 0 {
		for _, c := range l.charges {
			targetCents += c.AmountCents
		}
	}

	var parts []collected
	remaining := targetCents
	for _, c := range l.charges {
		if remaining <= 0 {
			break
		}
		part := c.AmountCents
		if part > remaining {
			part = remaining
		}
		remaining -= part
		parts = append(parts, collected{StripeID: c.StripeID, AmountCents: part})
	}

	// Then drop the ones already recorded, matching each against one recorded
	// amount so two equal slices consume two rows rather than one twice. This is
	// what makes a re-run of a job that got halfway a no-op for the half it
	// finished, and it is the guard that still holds after Stripe's own
	// idempotency keys have aged out at twenty-four hours.
	done := append([]int64(nil), l.alreadyRefunded...)
	var out []collected
	for _, p := range parts {
		if i := indexOf(done, p.AmountCents); i >= 0 {
			done = append(done[:i], done[i+1:]...)
			continue
		}
		out = append(out, p)
	}
	return out
}

func indexOf(amounts []int64, want int64) int {
	for i, a := range amounts {
		if a == want {
			return i
		}
	}
	return -1
}

// collectedPayments reads the ledger for a booking.
//
// Read from payments rather than from amount_paid_cents, because a refund has to
// name the payment it reverses. The gross on the booking is a total; the
// processor needs the individual transactions.
func collectedPayments(ctx context.Context, beginner Beginner, code string) (ledger, error) {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return ledger{}, fmt.Errorf("payments: reading the ledger for %s: %w", code, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger{}, ErrBookingNotFound
	}
	if err != nil {
		return ledger{}, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	rows, err := q.ListPaymentsForBooking(ctx, found.ID)
	if err != nil {
		return ledger{}, fmt.Errorf("payments: listing payments for %s: %w", code, err)
	}

	var out ledger
	for _, r := range rows {
		if r.Status != statusSucceeded {
			continue
		}
		if r.Kind == KindRefund {
			out.alreadyRefunded = append(out.alreadyRefunded, r.AmountCents)
			continue
		}
		out.charges = append(out.charges, collected{StripeID: r.StripeID, AmountCents: r.AmountCents})
	}
	return out, nil
}

// cancellationMail tells a guest their stay is off and what is coming back.
//
// Queued inside the transaction that cancelled the booking — refundDue for a
// stay the inn could not honour, Cancel for a guest who changed their mind —
// rather than inside the refund job, which runs once per intent and would send
// one message per slice of the money.
func cancellationMail(ctx context.Context, q *db.Queries, b db.GetBookingForPaymentRow, refunded int64) error {
	return email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.CancellationRefund,
		Data: email.CancellationRefundData{
			Code:      b.Code,
			GuestName: b.GuestName,
			Refunded:  email.Money(refunded),
			Checkin:   email.Day(b.Checkin.Time),
			Checkout:  email.Day(b.Checkout.Time),
		},
	})
}
