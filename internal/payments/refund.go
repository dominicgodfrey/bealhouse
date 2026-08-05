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

// refundPayload is what the job carries: the booking, and nothing else.
//
// Not the amount. Working out what to send back from the ledger at the moment
// the job runs is the only version that stays right if a second payment landed
// in between, and it means a stale queued job can never return the wrong figure.
type refundPayload struct {
	Code string `json:"code"`
}

// QueueRefund schedules the money going back.
//
// Called inside the transaction that cancelled the booking, so a stay that was
// cancelled without a refund queued behind it is not a state this system can
// reach. The unique key is the booking, so queueing twice for the same stay
// leaves one job.
func QueueRefund(ctx context.Context, q *db.Queries, code string) error {
	if _, err := jobs.Enqueue(ctx, q, jobs.Job{
		Kind:      RefundJobKind,
		Payload:   refundPayload{Code: code},
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
		return Refund(ctx, beginner, gw, in.Code)
	}
}

// Refund returns everything collected against a booking.
func Refund(ctx context.Context, beginner Beginner, gw Gateway, code string) error {
	collected, err := collectedPayments(ctx, beginner, code)
	if err != nil {
		return err
	}

	for _, p := range collected {
		refundID, err := gw.Refund(ctx, RefundRequest{
			BookingCode: code,
			IntentID:    p.StripeID,
			AmountCents: p.AmountCents,
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
			AmountCents: p.AmountCents,
		}); err != nil {
			return err
		}
		slog.Info("refunded a stay the inn could not honour",
			"booking", code, "amount_cents", p.AmountCents, "refund", refundID)
	}
	return nil
}

// collected is one payment that has to go back.
type collected struct {
	StripeID    string
	AmountCents int64
}

// collectedPayments lists the successful charges against a booking, excluding
// refunds already made.
//
// Read from the ledger rather than from amount_paid_cents, because a refund has
// to name the payment it reverses. The gross on the booking is a total; the
// processor needs the individual transactions.
func collectedPayments(ctx context.Context, beginner Beginner, code string) ([]collected, error) {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("payments: reading the ledger for %s: %w", code, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBookingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	rows, err := q.ListPaymentsForBooking(ctx, found.ID)
	if err != nil {
		return nil, fmt.Errorf("payments: listing payments for %s: %w", code, err)
	}

	// Which intents have already been sent back. A refund row's stripe_id is the
	// refund's own id, not the intent's, so this is keyed on the amount and kind
	// instead — enough for the one thing that matters, which is not refunding the
	// same money twice after a partial failure.
	var refunded int64
	var out []collected
	for _, r := range rows {
		if r.Status != statusSucceeded {
			continue
		}
		if r.Kind == KindRefund {
			refunded += r.AmountCents
			continue
		}
		out = append(out, collected{StripeID: r.StripeID, AmountCents: r.AmountCents})
	}

	// Everything has already gone back. Returning nothing here is what makes a
	// redelivered or retried job a no-op rather than a second refund.
	if refunded > 0 {
		var owed int64
		for _, c := range out {
			owed += c.AmountCents
		}
		if refunded >= owed {
			return nil, nil
		}
	}
	return out, nil
}

// cancellationMail tells a guest their stay could not be honoured and what is
// coming back. Queued inside RecordRefund's transaction, like every other
// message here.
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
