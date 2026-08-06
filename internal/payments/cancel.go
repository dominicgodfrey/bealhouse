package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/pricing"
)

var (
	// ErrNotCancellable means the stay is not in a state a guest can cancel: it
	// was never confirmed, or it has been cancelled already.
	//
	// Not an error worth spelling out to the caller in detail. A guest holding a
	// link to a cancelled booking has already got what they asked for.
	ErrNotCancellable = errors.New("payments: this booking cannot be cancelled")

	// ErrStayUnderway means check-in has arrived.
	//
	// Refused rather than run, because decision #9's arithmetic has no branch for
	// it: IsLateCancellation would call it late and return half the money for a
	// stay that is partly or wholly consumed. What a no-show or a cut-short visit
	// is worth is a conversation with the owner, and the admin console's manual
	// refund is where that belongs.
	ErrStayUnderway = errors.New("payments: the stay has already begun")
)

// Cancellation is what cancelling did.
type Cancellation struct {
	BookingID int64
	Code      string

	// Late is whether it fell inside the deposit-forfeiting window (decision #9),
	// which is the difference between getting everything back less the
	// processor's cut and getting half.
	Late bool

	// RetainedCents is what the inn keeps; RefundCents is what goes back. They do
	// not necessarily add up to what was paid — nothing is retained beyond
	// max(penalty, processing fee) (decision #26).
	RetainedCents int64
	RefundCents   int64
}

// Cancel cancels a confirmed stay at the guest's request and starts the refund.
//
// Everything happens in one transaction: the booking is cancelled, the room goes
// back on sale, the refund is queued and so is the message telling the guest. The
// queue is a table, so a crash anywhere in here leaves all four undone rather
// than a cancelled stay whose money never moved.
//
// `on` is a civil date resolved by the caller in America/New_York, the same way
// every other date boundary in this system is. It decides the refund, so it is
// an argument rather than a clock read in here — a test that could not choose
// which side of T-7 it was on could not test decision #9 at all.
func Cancel(ctx context.Context, beginner Beginner, code string, on time.Time) (Cancellation, error) {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return Cancellation{}, fmt.Errorf("payments: beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Cancellation{}, ErrBookingNotFound
	}
	if err != nil {
		return Cancellation{}, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	if found.Status != booking.StatusConfirmed {
		return Cancellation{}, ErrNotCancellable
	}
	if !on.Before(found.Checkin.Time) {
		return Cancellation{}, ErrStayUnderway
	}

	settings, err := q.GetSettings(ctx)
	if err != nil {
		return Cancellation{}, fmt.Errorf("payments: loading settings: %w", err)
	}

	// The split the guest agreed to, read back rather than recomputed, so a rate
	// edit since they booked cannot change what they get back.
	quote := pricing.Quote{
		TotalCents:   found.TotalCents,
		DepositCents: found.DepositCents,
		BalanceCents: found.BalanceDueCents,
	}

	late := pricing.IsLateCancellation(on, found.Checkin.Time)
	rate := pricing.Rate(settings.RefundProcessingRateScaled)

	// Derived from the gross collected, not from the total. That is what makes it
	// right when the T-7 charge failed: the guest has paid only the deposit, the
	// penalty is the deposit, and the answer is zero rather than a negative the
	// inn would try to collect (decision #25).
	refund := quote.Refund(found.AmountPaidCents, late, rate)

	if _, err := q.ReleaseBookingOccupancy(ctx, &found.ID); err != nil {
		return Cancellation{}, fmt.Errorf("payments: releasing the room: %w", err)
	}
	if err := q.CancelBooking(ctx, found.ID); err != nil {
		return Cancellation{}, fmt.Errorf("payments: cancelling the booking: %w", err)
	}

	// A late cancellation of a stay that had only paid its deposit returns
	// nothing, and queueing a job to move zero dollars would be a job that fails
	// at the processor for no reason.
	if refund > 0 {
		if err := QueueRefund(ctx, q, found.Code, refund); err != nil {
			return Cancellation{}, err
		}
	}

	// Sent whether or not money is going back: a guest who cancels is owed the
	// confirmation that it happened, and "$0.00" is the honest answer to what
	// they are getting when they cancelled two days before arriving.
	if err := cancellationMail(ctx, q, found, refund); err != nil {
		return Cancellation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Cancellation{}, fmt.Errorf("payments: committing: %w", err)
	}

	return Cancellation{
		BookingID:     found.ID,
		Code:          found.Code,
		Late:          late,
		RetainedCents: quote.Retained(found.AmountPaidCents, late, rate),
		RefundCents:   refund,
	}, nil
}
