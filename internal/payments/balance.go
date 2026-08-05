package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
)

// WarnJobKind is the durable job that sends decision #6's T-8 warning.
const WarnJobKind = "balance.warn"

// WarnInterval is how often the scan runs.
//
// Hourly rather than daily, even though the thing it looks for only changes at
// midnight. A daily job fires at whatever time the server last restarted, so
// the warning would arrive at 4am for one deploy and 4pm for the next; hourly
// puts it out within an hour of the day turning over, every time. The scan is
// an indexed lookup over a seven-room inn's confirmed stays, so the twenty-three
// runs that find nothing cost nothing.
const WarnInterval = time.Hour

// WarnBalances sends the T-8 warning to every stay that is due one, and reports
// how many went out.
//
// `on` is today at the inn. The scan looks a day ahead of it and catches up
// rather than matching a single date, so a server that was switched off for T-8
// sends the warning late instead of never — which is the whole of what decision
// #6 asks this message to do.
//
// **One transaction per guest, not one for the batch.** The queue is a table,
// so the mail and the note that it was sent commit together and a crash between
// them can neither lose a warning nor send it twice. Doing the whole batch in
// one transaction would get that same guarantee and one worse property: a
// single booking that fails takes everyone else's warning down with it, and a
// warning is worth sending on the day it is due or not at all.
//
// Failures are collected rather than returned at the first one, so the guests
// after a bad row still hear from the inn, and the job still fails loudly
// enough for the runner to retry and record it.
func WarnBalances(ctx context.Context, q *db.Queries, beginner Beginner, on time.Time) (int, error) {
	due, err := DueForBalanceWarning(ctx, q, on)
	if err != nil {
		return 0, err
	}

	var sent int
	var failures []error
	for _, d := range due {
		if err := warnOne(ctx, beginner, d); err != nil {
			failures = append(failures, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(failures...)
}

// warnOne queues one warning and marks the booking warned, together.
func warnOne(ctx context.Context, beginner Beginner, d Due) error {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("payments: beginning the warning for %s: %w", d.Code, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	if err := email.Queue(ctx, q, email.Envelope{
		To:       d.GuestEmail,
		Template: email.BalanceWarning,
		Data:     d.warning(),
	}); err != nil {
		return err
	}

	// In the same transaction, deliberately. Marking outside it would warn the
	// same guest every hour until they arrived, or lose the warning entirely,
	// depending on which side of the crash the write landed.
	if err := MarkWarned(ctx, q, d.BookingID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("payments: committing the warning for %s: %w", d.Code, err)
	}
	return nil
}

// warning is what the T-8 template is rendered against.
func (d Due) warning() email.BalanceWarningData {
	return email.BalanceWarningData{
		Code:      d.Code,
		GuestName: d.GuestName,
		Amount:    email.Money(d.AmountCents),
		ChargeOn:  email.Day(d.ChargeOn),
		Checkin:   email.Day(d.Checkin),
		Checkout:  email.Day(d.Checkout),
	}
}

// ChargeJobKind is the durable job that takes decision #6's T-7 balance.
const ChargeJobKind = "balance.charge"

// ChargeInterval matches the warning's cadence, and for the same reason: the
// scan's answer only changes at midnight, but an hourly run puts the charge
// through within an hour of the day turning over rather than at whatever time
// the server last restarted.
const ChargeInterval = time.Hour

// ChargeBalances takes the outstanding balance from the card saved at booking,
// for every stay due one, and reports how many charges went through.
//
// **The processor is called outside any transaction**, and each booking's
// bookkeeping opens its own afterwards. Holding Postgres open across a network
// round trip would be bad enough with one guest; across a batch it is a lock
// pile-up waiting for somebody else's API to have a slow afternoon.
//
// Running twice is safe at both ends. The gateway keys the charge on the booking
// code, so a retried job gets the same payment rather than a second one against
// the guest's card; the ledger keys on the resulting intent id, so recording it
// again changes nothing; and a booking drops out of the scan the moment its
// balance lands.
//
// Failures are collected rather than returned at the first one. One guest's card
// being refused must not stop the inn collecting from anybody else that day.
func ChargeBalances(
	ctx context.Context,
	q *db.Queries,
	beginner Beginner,
	gw Gateway,
	on time.Time,
) (int, error) {
	due, err := DueForBalanceCharge(ctx, q, on)
	if err != nil {
		return 0, err
	}

	var collected int
	var failures []error
	for _, d := range due {
		charged, err := chargeOne(ctx, beginner, gw, d)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if charged {
			collected++
		}
	}
	return collected, errors.Join(failures...)
}

// chargeOne takes one stay's balance, reporting whether money actually moved.
//
// A declined card is not an error: it is an outcome with an email and an owner
// flag attached, and returning it as one would have the runner retry the whole
// batch hourly and mail the same guest every time. Anything else — a timeout, a
// refused API key — is returned, because that is a job worth running again.
func chargeOne(ctx context.Context, beginner Beginner, gw Gateway, d Due) (bool, error) {
	// No card on file. Nothing was ever attempted at the processor, so there is
	// no payment to record — but the guest still owes the money and the owner
	// still has to know, which is exactly what the failure path is for.
	//
	// It should be unreachable: a booking with a balance to come is opened with
	// SaveCard, and the webhook writes the card down in the same transaction
	// that confirms the stay. Reachable or not, the alternative is a stay that
	// quietly never gets charged.
	if d.CustomerID == "" || d.PaymentMethodID == "" {
		slog.Error("a stay due its balance has no card on file",
			"booking", d.Code, "amount_cents", d.AmountCents)
		return false, flagUncollectable(ctx, beginner, d)
	}

	intent, err := gw.ChargeOffSession(ctx, OffSessionRequest{
		BookingCode:     d.Code,
		AmountCents:     d.AmountCents,
		CustomerID:      d.CustomerID,
		PaymentMethodID: d.PaymentMethodID,
	})

	if declined, ok := IsDeclined(err); ok {
		slog.Warn("balance charge declined",
			"booking", d.Code, "amount_cents", d.AmountCents, "reason", declined.Reason)

		id := declined.IntentID
		if id == "" {
			// A decline the processor could not name an attempt for. The ledger
			// keys on (stripe_id, status), so a synthesised id per booking still
			// records the attempt exactly once and cannot collide with a real
			// one.
			id = "declined:" + d.Code
		}

		// RecordFailure flags the booking and mails the guest in one
		// transaction. It deliberately leaves the stay confirmed: they are still
		// arriving, there is just money outstanding.
		if _, err := RecordFailure(ctx, beginner, Charge{
			BookingCode: d.Code,
			StripeID:    id,
			Kind:        KindBalance,
			AmountCents: d.AmountCents,
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("payments: charging the balance for %s: %w", d.Code, err)
	}

	if _, err := RecordCharge(ctx, beginner, Charge{
		BookingCode: d.Code,
		StripeID:    intent.ID,
		Kind:        KindBalance,
		AmountCents: d.AmountCents,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// flagUncollectable marks a balance that cannot be charged and tells the guest,
// without recording a payment nobody attempted.
func flagUncollectable(ctx context.Context, beginner Beginner, d Due) error {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("payments: flagging %s as uncollectable: %w", d.Code, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	found, err := q.GetBookingForPayment(ctx, d.Code)
	if err != nil {
		return fmt.Errorf("payments: loading booking %q: %w", d.Code, err)
	}
	if err := q.MarkBalanceChargeFailed(ctx, found.ID); err != nil {
		return fmt.Errorf("payments: flagging the failed balance charge: %w", err)
	}
	if err := balanceFailed(ctx, q, found); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("payments: committing the failure for %s: %w", d.Code, err)
	}
	return nil
}

// ChargeJob adapts ChargeBalances to the runner's handler signature.
func ChargeJob(q *db.Queries, beginner Beginner, gw Gateway) func(context.Context, []byte) error {
	return func(ctx context.Context, _ []byte) error {
		collected, err := ChargeBalances(ctx, q, beginner, gw, civil.Today())
		if collected > 0 {
			slog.Info("collected outstanding balances", "count", collected)
		}
		return err
	}
}

// WarnJob adapts WarnBalances to the runner's handler signature.
//
// Today at the inn is resolved here rather than passed in, because "is it T-8
// yet" is a question about the calendar in New Hampshire and the answer changes
// between runs.
func WarnJob(q *db.Queries, beginner Beginner) func(context.Context, []byte) error {
	return func(ctx context.Context, _ []byte) error {
		sent, err := WarnBalances(ctx, q, beginner, civil.Today())
		if sent > 0 {
			slog.Info("warned guests about their balance charge", "count", sent)
		}
		return err
	}
}
