// Package payments records money against a booking.
//
// It knows nothing about Stripe. Everything here takes amounts and opaque
// object ids and writes the consequences down, which is what lets the whole
// payment state machine — including the two cases that are genuinely hard —
// be tested against a real Postgres without an API key or a network.
//
// The hard cases, both of which happen to real guests:
//
//   - The same event arrives twice. Stripe's delivery is at-least-once and it
//     redelivers on any non-2xx, so "record this payment" has to be safe to run
//     again. The unique index on payments (stripe_id, status) is what decides,
//     not a flag read earlier in the handler. The status half of that key
//     matters: a declined card is retried on the same PaymentIntent, so one id
//     carries a failed attempt and then a successful one, and keying on the id
//     alone silently dropped the second (decision #28).
//
//   - The money arrives after the hold has gone. A guest who takes longer than
//     the hold TTL — a slow 3-D Secure challenge, a wandering attention span —
//     can pay for a room the sweeper has already put back on sale. The room may
//     still be free, in which case it is simply taken back; or it may have been
//     sold to somebody else, in which case the only honest outcome is a refund
//     and an owner who is told. What must never happen is a guest charged for a
//     room another guest is also holding, and the exclusion constraint is what
//     guarantees that rather than the ordering of this code.
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
	"bealhouse/internal/pricing"
)

// The kinds of movement the ledger records, matching the CHECK on payments.kind.
const (
	// KindDeposit is the 50% taken at booking (decision #8).
	KindDeposit = "deposit"

	// KindFull is a short-notice stay charged in one go (decision #7).
	KindFull = "full"

	// KindBalance is the off-session T-7 charge (decision #6).
	KindBalance = "balance"

	// KindRefund is money returned (decision #9).
	KindRefund = "refund"
)

const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
)

var (
	// ErrBookingNotFound means the charge names a booking that does not exist.
	// Worth failing loudly: it means Stripe metadata and the database disagree,
	// which is a bug rather than a guest's problem.
	ErrBookingNotFound = errors.New("payments: no such booking")

	ErrAmountNotPositive = errors.New("payments: amount must be positive")
	ErrKindUnknown       = errors.New("payments: unknown payment kind")
	ErrStripeIDRequired  = errors.New("payments: a stripe id is required")
)

// warningLeadDays is how far ahead of the balance charge the guest is warned:
// the T-8 email about the T-7 charge (decision #6).
const warningLeadDays = 1

// Outcome is what recording a charge did.
type Outcome string

const (
	// Confirmed is the ordinary path: the money landed, the room is the
	// guest's, and the booking says so.
	Confirmed Outcome = "confirmed"

	// AlreadyApplied means this exact Stripe object had been recorded before
	// and nothing changed. A redelivered webhook should still answer 200: the
	// work is done, and asking Stripe to try again would only produce this
	// answer again.
	AlreadyApplied Outcome = "already_applied"

	// RefundDue means the charge succeeded but the room did not survive — it
	// was resold while the guest was paying. The booking is cancelled, the
	// money is recorded as collected, and RefundCents says what has to go back.
	// Nobody is left holding a charge for a room they cannot have.
	RefundDue Outcome = "refund_due"

	// Underpaid means real money arrived but less of it than the booking's own
	// snapshot says was due at booking. The payment is recorded — it happened,
	// and the ledger does not get to be selective — but the stay is not
	// confirmed and the hold is left to lapse on its own.
	//
	// This should be unreachable: the PaymentIntent's amount is set by the
	// server from the booking, and the webhook that reports it is signature
	// verified. It exists because the cost of being wrong about that is a
	// confirmed room the inn was paid a dollar for, and one comparison against
	// a number already in the row is a cheap thing to be sure about.
	Underpaid Outcome = "underpaid"
)

// Charge is a successful payment as the webhook describes it.
type Charge struct {
	// BookingCode comes from the PaymentIntent's metadata rather than from the
	// browser, so a guest cannot attach their payment to somebody else's stay.
	BookingCode string

	// StripeID is the PaymentIntent id. Together with the outcome it is the
	// idempotency key for the whole operation, so it has to be the id of the
	// object that actually moved the money, not one we generate.
	//
	// Deliberately not unique on its own: a declined card is retried on the
	// same intent, so one id can carry both a failure and a success.
	StripeID string

	// EventID is the Stripe event that reported this, and is recorded inside
	// the same transaction as the payment. Optional — a caller that is not a
	// webhook has no event to name.
	//
	// It is not the primary idempotency guard; the payments index is. It is
	// here so that "this event has been handled" and "this payment has been
	// recorded" become one fact that commits or rolls back together, rather
	// than two that can disagree after a crash.
	EventID string

	// EventType is Stripe's name for what happened —
	// "payment_intent.succeeded" and the like — and is what stripe_events
	// records alongside the id. Not the same thing as Kind: that is the inn's
	// word for which part of the money this is. Keeping Stripe's own vocabulary
	// here is the difference between an audit table that can be reconciled
	// against the dashboard and one that cannot.
	EventType string

	Kind        string
	AmountCents int64

	// CustomerID and PaymentMethodID are the card the processor kept, and are
	// recorded on the booking in the same transaction as the payment.
	//
	// They arrive here rather than being stored when the payment was opened
	// because there is no card at that point — the guest has not typed one yet.
	// The webhook reporting a success is the first moment this system knows what
	// it will charge at T-7, and a stay that reached confirmed without them is
	// one whose balance could never be collected.
	//
	// Empty on a short-notice stay, which pays in full and is never charged
	// again (decision #7), and on the balance charge itself.
	CustomerID      string
	PaymentMethodID string

	// OwnerEmail gets the inn's own copy of a confirmed booking. Empty sends
	// none, which is what the tests and the T-7 job want.
	//
	// It rides on the charge rather than being configuration this package
	// reads, because the queueing has to happen inside RecordCharge's
	// transaction and a package-level setting would be one more thing that can
	// be wrong in one process and right in another.
	OwnerEmail string

	// ManageURL builds the guest's signed manage-booking link (decision #19),
	// given the code and when the link should stop working.
	//
	// A function rather than a signer, for the same reason OwnerEmail is a
	// string: what a link looks like, what it is signed with and which origin it
	// points at are all the HTTP layer's business, and this package's job is
	// only to put the result in the message at the moment the stay is confirmed.
	// Nil leaves the link out, which is what an unconfigured deploy gets.
	ManageURL func(code string, expires time.Time) string
}

// Result is what happened, and what the caller still has to do about it.
type Result struct {
	Outcome   Outcome
	BookingID int64
	Code      string

	// RefundCents is set only on RefundDue: the whole gross collected against
	// this booking, because a guest who ends up with no room owes nothing. The
	// cancellation penalty in decision #9 is for a guest who changes their
	// mind, not for a stay the inn could not honour.
	RefundCents int64

	// ShortfallCents is set only on Underpaid: how much more had to arrive
	// before the stay could be confirmed.
	ShortfallCents int64
}

// Beginner is anything that can start a transaction: a *pgxpool.Pool in
// production, and in tests an already-open transaction whose nested Begin is a
// savepoint.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Seen reports whether a Stripe event has already been handled, and marks it
// handled if not.
//
// **Only for event types that write no payment row.** Those have no other
// record that they happened, and would otherwise be reprocessed on every
// redelivery for as long as Stripe keeps trying.
//
// It must not be used to gate RecordCharge, RecordFailure or RecordRefund. Each
// of those opens its own transaction, so marking the event handled out here
// commits separately from the work: if the work then fails, Stripe redelivers,
// this answers "already seen", and the handler skips a payment that was never
// recorded. Those three take an EventID and write it down inside their own
// transaction precisely so the two facts cannot come apart.
func Seen(ctx context.Context, q *db.Queries, eventID, eventType string) (bool, error) {
	inserted, err := q.RecordStripeEvent(ctx, db.RecordStripeEventParams{
		ID:   eventID,
		Type: eventType,
	})
	if err != nil {
		return false, fmt.Errorf("payments: recording event %q: %w", eventID, err)
	}
	return inserted == 0, nil
}

// noteEvent records the Stripe event inside the caller's transaction, reporting
// whether it was already there.
//
// Same commit as the payment it describes, which is the whole point: a
// transaction that rolls back leaves the event unrecorded too, so a redelivery
// does the work rather than skipping it.
func noteEvent(ctx context.Context, q *db.Queries, in Charge) (bool, error) {
	if strings.TrimSpace(in.EventID) == "" {
		return false, nil
	}

	// A caller that named no event type still gets a usable row: the payment
	// kind is a poorer label than Stripe's own, but an empty string in an audit
	// table is worse than either.
	eventType := strings.TrimSpace(in.EventType)
	if eventType == "" {
		eventType = in.Kind
	}

	inserted, err := q.RecordStripeEvent(ctx, db.RecordStripeEventParams{
		ID:   in.EventID,
		Type: eventType,
	})
	if err != nil {
		return false, fmt.Errorf("payments: recording event %q: %w", in.EventID, err)
	}
	return inserted == 0, nil
}

// RecordCharge writes a successful payment down and works out what it means for
// the room.
//
// Everything happens in one transaction, so a booking is never left confirmed
// with no occupancy behind it, and money is never recorded without the booking
// moving with it.
func RecordCharge(ctx context.Context, beginner Beginner, in Charge) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("payments: beginning transaction: %w", err)
	}
	// WithoutCancel so a webhook whose client hung up still releases its locks
	// rather than leaving the transaction to time out.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(in.BookingCode)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrBookingNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("payments: loading booking %q: %w", in.BookingCode, err)
	}

	seen, err := noteEvent(ctx, q, in)
	if err != nil {
		return Result{}, err
	}
	if seen {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	}

	// The idempotency gate. A duplicate delivery inserts nothing and stops
	// here, before anything is added to the gross collected.
	if _, err := q.RecordPayment(ctx, db.RecordPaymentParams{
		BookingID:   found.ID,
		StripeID:    in.StripeID,
		Kind:        in.Kind,
		AmountCents: in.AmountCents,
		Status:      statusSucceeded,
	}); errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	} else if err != nil {
		return Result{}, fmt.Errorf("payments: recording payment: %w", err)
	}

	// The money moved, so it goes on the booking whatever happens to the room.
	if err := q.AddPaymentToBooking(ctx, db.AddPaymentToBookingParams{
		ID:          found.ID,
		AmountCents: in.AmountCents,
	}); err != nil {
		return Result{}, fmt.Errorf("payments: adding to the gross collected: %w", err)
	}

	// And so does the card, in the same transaction. A stay confirmed without
	// the means to take its balance is one the guest finds out about at the
	// door; committing them separately is a crash away from exactly that.
	if in.CustomerID != "" && in.PaymentMethodID != "" {
		if err := q.SaveBookingCard(ctx, db.SaveBookingCardParams{
			ID:                    found.ID,
			StripeCustomerID:      in.CustomerID,
			StripePaymentMethodID: in.PaymentMethodID,
		}); err != nil {
			return Result{}, fmt.Errorf("payments: saving the card for the balance charge: %w", err)
		}
	}

	// Nothing is confirmed until the money due at booking has actually arrived.
	// Checked against the booking's own snapshot rather than against anything
	// in the request, so it holds even if whatever created the PaymentIntent
	// was talked into naming its own amount.
	if short := shortfall(found, in.AmountCents); short > 0 {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{
			Outcome:        Underpaid,
			BookingID:      found.ID,
			Code:           found.Code,
			ShortfallCents: short,
		}, nil
	}

	secured, err := secureRoom(ctx, tx, q, found)
	if err != nil {
		return Result{}, err
	}

	// A cancelled booking cannot be revived by a late payment either: the room
	// has already gone back on sale and somebody may be standing in it.
	if !secured || found.Status == booking.StatusCancelled {
		return refundDue(ctx, tx, q, found, in.AmountCents)
	}

	// Read before anything in this transaction changed it, which is what makes
	// it the honest answer to "did this payment confirm the stay, or was it
	// already confirmed?". ConfirmBooking below runs either way and is harmless
	// on a stay that is already confirmed — but the confirmation email is not,
	// and a guest whose T-7 balance lands reading it as a second booking is a
	// phone call the owner did not need.
	newlyConfirmed := found.Status != booking.StatusConfirmed

	if err := q.ConfirmBooking(ctx, found.ID); err != nil {
		return Result{}, fmt.Errorf("payments: confirming the booking: %w", err)
	}

	// The confirmation is queued in the same transaction that confirmed the
	// stay, for the reason MarkWarned is (CLAUDE.md): the queue is a table, so
	// the message and the fact that earned it commit together. Queueing after
	// the commit would lose a confirmation to any crash in between — for a
	// guest whose card has already been charged, and with nothing left to
	// retry it.
	switch {
	case newlyConfirmed:
		if err := confirmationMail(ctx, q, found, in); err != nil {
			return Result{}, err
		}

	// The stay was already confirmed and this is its balance landing. A receipt,
	// not a second confirmation — and the message that closes the loop the T-8
	// warning opened.
	case in.Kind == KindBalance:
		if err := balanceReceipt(ctx, q, found, in); err != nil {
			return Result{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("payments: committing: %w", err)
	}
	return Result{Outcome: Confirmed, BookingID: found.ID, Code: found.Code}, nil
}

// RecordFailure notes a declined charge.
//
// It deliberately does not touch the booking's status. A failed deposit leaves
// the stay pending and the hold running out on its own schedule, and a failed
// T-7 balance charge leaves the stay confirmed — the guest is still arriving,
// there is just money outstanding — with a flag the owner cannot miss.
func RecordFailure(ctx context.Context, beginner Beginner, in Charge) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("payments: beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(in.BookingCode)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrBookingNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("payments: loading booking %q: %w", in.BookingCode, err)
	}

	seen, err := noteEvent(ctx, q, in)
	if err != nil {
		return Result{}, err
	}
	if seen {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	}

	if _, err := q.RecordPayment(ctx, db.RecordPaymentParams{
		BookingID:   found.ID,
		StripeID:    in.StripeID,
		Kind:        in.Kind,
		AmountCents: in.AmountCents,
		Status:      statusFailed,
	}); errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	} else if err != nil {
		return Result{}, fmt.Errorf("payments: recording the failure: %w", err)
	}

	if in.Kind == KindBalance {
		if err := q.MarkBalanceChargeFailed(ctx, found.ID); err != nil {
			return Result{}, fmt.Errorf("payments: flagging the failed balance charge: %w", err)
		}
		// The guest hears about it in the same transaction as the flag the owner
		// sees, so the two can never disagree about whether the money is
		// outstanding.
		if err := balanceFailed(ctx, q, found); err != nil {
			return Result{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("payments: committing: %w", err)
	}
	return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
}

// RecordRefund writes a refund down, cancels the stay and puts the room back on
// sale.
//
// The amount is decided by the caller from pricing.Refund, which derives it from
// what was actually collected. It is not subtracted from amount_paid_cents:
// that column is the gross, and reducing it would make a second cancellation
// compute a smaller refund off an already-reduced figure.
func RecordRefund(ctx context.Context, beginner Beginner, in Charge) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("payments: beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(in.BookingCode)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrBookingNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("payments: loading booking %q: %w", in.BookingCode, err)
	}

	seen, err := noteEvent(ctx, q, in)
	if err != nil {
		return Result{}, err
	}
	if seen {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	}

	if _, err := q.RecordPayment(ctx, db.RecordPaymentParams{
		BookingID:   found.ID,
		StripeID:    in.StripeID,
		Kind:        KindRefund,
		AmountCents: in.AmountCents,
		Status:      statusSucceeded,
	}); errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("payments: committing: %w", err)
		}
		return Result{Outcome: AlreadyApplied, BookingID: found.ID, Code: found.Code}, nil
	} else if err != nil {
		return Result{}, fmt.Errorf("payments: recording the refund: %w", err)
	}

	if _, err := q.ReleaseBookingOccupancy(ctx, &found.ID); err != nil {
		return Result{}, fmt.Errorf("payments: releasing the room: %w", err)
	}
	if err := q.CancelBooking(ctx, found.ID); err != nil {
		return Result{}, fmt.Errorf("payments: cancelling the booking: %w", err)
	}

	// No mail here, deliberately. This runs once per intent being refunded, so a
	// stay that paid a deposit and then a balance would get two "your stay is
	// cancelled" messages, each naming a fraction of the money. The guest is told
	// once, by whichever transaction decided to cancel — refundDue below, or
	// Cancel — and both know the whole figure.

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("payments: committing: %w", err)
	}
	return Result{
		Outcome:     RefundDue,
		BookingID:   found.ID,
		Code:        found.Code,
		RefundCents: in.AmountCents,
	}, nil
}

// decodePayload reads a job's JSON payload.
func decodePayload(payload []byte, into any) error {
	if err := json.Unmarshal(payload, into); err != nil {
		return fmt.Errorf("payments: decoding the job payload: %w", err)
	}
	return nil
}

// StartPayment notes that a PaymentIntent now exists for a booking, which is
// what keeps the sweeper from expiring it while the guest is still paying.
func StartPayment(ctx context.Context, q *db.Queries, bookingID int64, intentID string) error {
	if strings.TrimSpace(intentID) == "" {
		return ErrStripeIDRequired
	}
	if err := q.StartPayment(ctx, db.StartPaymentParams{
		ID:              bookingID,
		PaymentIntentID: intentID,
	}); err != nil {
		return fmt.Errorf("payments: marking payment started: %w", err)
	}
	return nil
}

// secureRoom makes sure the booking still occupies its rooms, and reports
// whether it managed to.
//
// Three ways in, and the difference between them is the whole of the late
// payment problem:
//
//   - The hold is still there. Promote it in place — kind and expires_at move
//     in the same statement, so occupancy_holds_expire is never violated and
//     the row is never dropped, meaning the room is not free for an instant in
//     between.
//   - The hold is gone but occupancy remains. This is an already-confirmed stay
//     taking its T-7 balance charge; there is nothing to do.
//   - Nothing remains. The sweeper reclaimed the hold while the guest was
//     paying, so take the room back the same way any other booker would — and
//     let the exclusion constraint say whether it is still going.
func secureRoom(ctx context.Context, tx pgx.Tx, q *db.Queries, b db.GetBookingForPaymentRow) (bool, error) {
	promoted, err := q.PromoteHoldToBooking(ctx, &b.ID)
	if err != nil {
		return false, fmt.Errorf("payments: promoting the hold: %w", err)
	}
	if promoted > 0 {
		return true, nil
	}

	held, err := q.CountBookingOccupancy(ctx, &b.ID)
	if err != nil {
		return false, fmt.Errorf("payments: checking occupancy: %w", err)
	}
	if held > 0 {
		return true, nil
	}

	rooms, err := q.ListBookingRooms(ctx, b.ID)
	if err != nil {
		return false, fmt.Errorf("payments: loading the booked rooms: %w", err)
	}
	if len(rooms) == 0 {
		// A booking with no rooms cannot be honoured, and pretending otherwise
		// would confirm a stay that occupies nothing.
		return false, nil
	}

	for _, room := range rooms {
		// A savepoint per room. Losing this race raises 23P01, which aborts the
		// subtransaction — without the savepoint it would poison the outer one
		// and take the payment record down with it, which is the one thing that
		// must survive.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return false, fmt.Errorf("payments: opening a savepoint: %w", err)
		}

		_, err = occupancy.Create(ctx, db.New(sp), db.CreateOccupancyParams{
			RoomID:    room.RoomID,
			Checkin:   room.Checkin,
			Checkout:  room.Checkout,
			Kind:      "booking",
			Source:    "direct",
			BookingID: &b.ID,
		})
		if err != nil {
			_ = sp.Rollback(ctx)
			if errors.Is(err, occupancy.ErrRoomTaken) {
				return false, nil
			}
			return false, fmt.Errorf("payments: re-claiming the room: %w", err)
		}
		if err := sp.Commit(ctx); err != nil {
			return false, fmt.Errorf("payments: committing the re-claim: %w", err)
		}
	}
	return true, nil
}

// refundDue closes out the case where the money landed but the room did not.
//
// Any rooms re-claimed before the failing one are released, so a multi-room
// booking does not leave half a stay occupying rooms nobody is coming to.
//
// **The refund is queued as a job, inside this transaction.** Returning the
// amount for the caller to act on was not enough: RecordCharge is idempotent, so
// a caller that failed to issue the refund would get AlreadyApplied on the
// redelivery and the money would never go back — a guest charged for a room
// somebody else is standing in, with nothing left anywhere to notice it. In the
// queue it survives a crash, a restart and a Stripe outage, and retries itself.
func refundDue(
	ctx context.Context,
	tx pgx.Tx,
	q *db.Queries,
	b db.GetBookingForPaymentRow,
	justPaid int64,
) (Result, error) {
	if _, err := q.ReleaseBookingOccupancy(ctx, &b.ID); err != nil {
		return Result{}, fmt.Errorf("payments: releasing the rooms: %w", err)
	}
	if err := q.CancelBooking(ctx, b.ID); err != nil {
		return Result{}, fmt.Errorf("payments: cancelling the booking: %w", err)
	}

	// Everything collected, this charge included. AmountPaidCents was read before
	// the row was updated, so the addition here is deliberate rather than a stale
	// read. Penalty-free, and deliberately not through pricing.Refund: decision
	// #9's forfeit is for a guest who changed their mind, and this guest did not
	// — the inn could not honour the stay (decision #24).
	owed := b.AmountPaidCents + justPaid

	// Zero: this path returns everything, so the job works the figure out from
	// the ledger when it runs and cannot be wrong about a payment that landed in
	// between.
	if err := QueueRefund(ctx, q, b.Code, 0); err != nil {
		return Result{}, err
	}

	// The one message for this cancellation, naming the whole amount. Queued in
	// the transaction that cancelled the stay, so a guest is never told money is
	// coming back without a record of it, or left with a refund nobody mentioned.
	if err := cancellationMail(ctx, q, b, owed); err != nil {
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("payments: committing: %w", err)
	}
	return Result{
		Outcome:     RefundDue,
		BookingID:   b.ID,
		Code:        b.Code,
		RefundCents: owed,
	}, nil
}

// shortfall reports how much less than the amount due at booking has been
// collected once this charge is counted, or zero when the booking is covered.
//
// The split comes from the booking's own row — the numbers the guest agreed to
// — and a NULL balance_charge_at is decision #7's short-notice flag, meaning
// the whole total was due up front rather than half of it. Read the same way
// booking.Get reads it, so the two cannot drift.
//
// A balance charge on a stay that already paid its deposit is over this line
// long before it arrives, which is why this does not need a case for it.
func shortfall(b db.GetBookingForPaymentRow, justPaid int64) int64 {
	quote := pricing.Quote{
		TotalCents:   b.TotalCents,
		DepositCents: b.DepositCents,
		BalanceCents: b.BalanceDueCents,
	}
	due := quote.ChargeAtBooking(!b.BalanceChargeAt.Valid)

	if collected := b.AmountPaidCents + justPaid; collected < due {
		return due - collected
	}
	return 0
}

func (c Charge) validate() error {
	if strings.TrimSpace(c.StripeID) == "" {
		return ErrStripeIDRequired
	}
	if c.AmountCents <= 0 {
		return ErrAmountNotPositive
	}
	switch c.Kind {
	case KindDeposit, KindFull, KindBalance, KindRefund:
	default:
		return ErrKindUnknown
	}
	return nil
}

// RefundQuote is what cancelling a stay on a given day would return.
type RefundQuote struct {
	BookingID int64
	Code      string

	// PaidCents is the gross collected, which is what the refund derives from
	// rather than the total (decision #25).
	PaidCents int64

	// Late is whether the cancellation falls inside the deposit-forfeiting
	// window (decision #9).
	Late bool

	// RetainedCents is what the inn keeps: the cancellation penalty, or the
	// card processor's cut when that is larger (decision #26).
	RetainedCents int64

	RefundCents int64
}

// RefundFor works out what cancelling a booking on a given day would return.
//
// Everything comes from the booking's own snapshot — the deposit split settled
// when the guest booked — so a rate edit since then cannot change what they get
// back. `on` is a civil date resolved by the caller in America/New_York.
func RefundFor(ctx context.Context, q *db.Queries, code string, on time.Time) (RefundQuote, error) {
	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundQuote{}, ErrBookingNotFound
	}
	if err != nil {
		return RefundQuote{}, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	settings, err := q.GetSettings(ctx)
	if err != nil {
		return RefundQuote{}, fmt.Errorf("payments: loading settings: %w", err)
	}

	// Only the split matters to the refund arithmetic, and it is read back
	// rather than recomputed: these are the numbers the guest agreed to.
	quote := pricing.Quote{
		TotalCents:   found.TotalCents,
		DepositCents: found.DepositCents,
		BalanceCents: found.BalanceDueCents,
	}

	late := pricing.IsLateCancellation(on, found.Checkin.Time)
	rate := pricing.Rate(settings.RefundProcessingRateScaled)

	return RefundQuote{
		BookingID:     found.ID,
		Code:          found.Code,
		PaidCents:     found.AmountPaidCents,
		Late:          late,
		RetainedCents: quote.Retained(found.AmountPaidCents, late, rate),
		RefundCents:   quote.Refund(found.AmountPaidCents, late, rate),
	}, nil
}

// Due is a stay with money still to collect.
//
// It carries the guest and the saved card as well as the amount, because the
// only two callers are a job that emails the guest and a job that charges them.
// Both scans fetch it in one query rather than looking each booking up again:
// a per-row round trip is a batch that can fail halfway through, leaving some
// guests warned and some not.
type Due struct {
	BookingID   int64
	Code        string
	AmountCents int64

	GuestEmail string
	GuestName  string

	// ChargeOn is the day the balance comes off the card: T-7 (decision #6).
	// A civil date, like every other date the guest ever sees.
	ChargeOn time.Time
	Checkin  time.Time
	Checkout time.Time

	// CustomerID and PaymentMethodID are what an off-session charge needs. Empty
	// on a booking whose deposit never saved a card, which is a reason the T-7
	// charge fails rather than a reason to leave it out of the scan — the owner
	// still has to hear about it.
	CustomerID      string
	PaymentMethodID string
}

// DueForBalanceCharge lists confirmed stays whose balance is collectable on the
// given day (decision #6's T-7).
//
// A scan rather than a queue of pre-scheduled rows, and idempotent because of
// it: a booking stops matching the moment its charge lands, so running this
// twice in a minute costs nothing and a day the server spent switched off
// catches up by itself on the next tick. `on` is a civil date resolved by the
// caller in America/New_York, because "is it T-7 yet" is a question about the
// calendar at the inn.
//
// Short-notice stays never appear here at all: their balance_charge_at is NULL,
// which is decision #7's flag and not a missing value.
func DueForBalanceCharge(ctx context.Context, q *db.Queries, on time.Time) ([]Due, error) {
	rows, err := q.ListBookingsDueForBalanceCharge(ctx, date(on))
	if err != nil {
		return nil, fmt.Errorf("payments: listing balance charges due: %w", err)
	}

	out := make([]Due, 0, len(rows))
	for _, r := range rows {
		out = append(out, Due{
			BookingID: r.ID,
			Code:      r.Code,
			// What is left rather than the snapshotted balance: if the deposit
			// failed and was retried, or the guest was charged in part, the
			// remainder is the honest number to collect.
			AmountCents:     r.TotalCents - r.AmountPaidCents,
			GuestEmail:      r.GuestEmail,
			GuestName:       r.GuestName,
			ChargeOn:        r.BalanceChargeAt.Time,
			Checkin:         r.Checkin.Time,
			Checkout:        r.Checkout.Time,
			CustomerID:      r.StripeCustomerID,
			PaymentMethodID: r.StripePaymentMethodID,
		})
	}
	return out, nil
}

// DueForBalanceWarning lists stays to warn the day before the charge (T-8).
//
// `on` is today at the inn; the scan looks a day ahead of it. Like the charge
// scan this catches up rather than matching a single day: a warning is worth
// sending late, and sending none at all makes the T-7 charge exactly the
// surprise decision #6 set out to avoid. MarkWarned is what stops it repeating,
// so a caller that does not record the send will warn the same guest daily.
func DueForBalanceWarning(ctx context.Context, q *db.Queries, on time.Time) ([]Due, error) {
	rows, err := q.ListBookingsToWarnAboutBalance(ctx, date(on.AddDate(0, 0, warningLeadDays)))
	if err != nil {
		return nil, fmt.Errorf("payments: listing balance warnings due: %w", err)
	}

	out := make([]Due, 0, len(rows))
	for _, r := range rows {
		out = append(out, Due{
			BookingID:       r.ID,
			Code:            r.Code,
			AmountCents:     r.TotalCents - r.AmountPaidCents,
			GuestEmail:      r.GuestEmail,
			GuestName:       r.GuestName,
			ChargeOn:        r.BalanceChargeAt.Time,
			Checkin:         r.Checkin.Time,
			Checkout:        r.Checkout.Time,
			CustomerID:      r.StripeCustomerID,
			PaymentMethodID: r.StripePaymentMethodID,
		})
	}
	return out, nil
}

// MarkWarned records that the T-8 email has gone out, so the scan stops
// offering the booking.
//
// Call it with the same *db.Queries that queued the email, inside one
// transaction: the queue is a table, so the send and the note that it was sent
// commit together and a crash between them can neither lose the warning nor
// send it twice.
func MarkWarned(ctx context.Context, q *db.Queries, bookingID int64) error {
	if err := q.MarkBalanceWarned(ctx, bookingID); err != nil {
		return fmt.Errorf("payments: marking the balance warning sent: %w", err)
	}
	return nil
}

// date is the pgtype form of a civil date, for the scheduled-charge scans.
func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }
