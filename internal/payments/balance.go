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
