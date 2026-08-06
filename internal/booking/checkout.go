package booking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
)

// CheckoutJobKind is the durable job that sends the departure-morning email.
const CheckoutJobKind = "checkout.remind"

// CheckoutInterval is how often the scan runs.
//
// Fifteen minutes rather than the hour the balance jobs use, and the difference
// is the promise this message makes. The T-8 warning only has to arrive before
// the T-7 charge, so landing anywhere inside the right day is fine; this one is
// meant to be in the inbox when the guest wakes up on the day they leave, and
// an hourly job phased by whenever the server last restarted can put it as much
// as an hour into the day. Fifteen minutes puts it within a quarter of an hour
// of midnight at the inn, every time.
//
// The cost of the other ninety-five runs a day is one indexed lookup that
// matches nothing, against a partial index over a seven-room inn.
const CheckoutInterval = 15 * time.Minute

// SendCheckoutEmails writes to every guest leaving today, and reports how many
// messages went out.
//
// `on` is today at the inn — America/New_York, like every other date a guest
// sees. **The scan matches that day exactly**, where the balance warning uses a
// threshold so a missed day catches up: this message tells a guest they are
// leaving today, and one that arrives after they got home is worse than one
// that never arrives. A day the server spent entirely off is a departure note
// that does not go out.
//
// One transaction per guest, for the reason the T-8 warning has one: the
// message and the note that it was sent commit together, so a crash between
// them can neither lose it nor send it every quarter of an hour until midnight
// — and one bad row does not take everybody else's message down with it.
func SendCheckoutEmails(ctx context.Context, q *db.Queries, beginner Beginner, on time.Time) (int, error) {
	due, err := q.ListBookingsDueForCheckoutEmail(ctx, pgtype.Date{Time: on, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("booking: listing today's departures: %w", err)
	}
	if len(due) == 0 {
		return 0, nil
	}

	// Read once for the batch rather than once per guest. It is the same answer
	// for everybody leaving today, and the alternative is a round trip per row
	// that can fail halfway through.
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("booking: loading settings for the checkout email: %w", err)
	}
	checkoutTime := email.Clock(time.Duration(settings.CheckoutTime.Microseconds) * time.Microsecond)

	var sent int
	var failures []error
	for _, d := range due {
		if err := remindOne(ctx, beginner, d, checkoutTime); err != nil {
			failures = append(failures, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(failures...)
}

// remindOne queues one departure email and marks the booking written to,
// together.
func remindOne(
	ctx context.Context,
	beginner Beginner,
	d db.ListBookingsDueForCheckoutEmailRow,
	checkoutTime string,
) error {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("booking: beginning the checkout email for %s: %w", d.Code, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	rooms, err := q.ListBookingRooms(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("booking: loading rooms for the checkout email for %s: %w", d.Code, err)
	}
	names := make([]string, 0, len(rooms))
	for _, r := range rooms {
		names = append(names, r.Name)
	}

	if err := email.Queue(ctx, q, email.Envelope{
		To:       d.GuestEmail,
		Template: email.CheckoutReminder,
		Data: email.CheckoutReminderData{
			Code:         d.Code,
			GuestName:    d.GuestName,
			Rooms:        names,
			Checkin:      email.Day(d.Checkin.Time),
			Checkout:     email.Day(d.Checkout.Time),
			Nights:       fmt.Sprint(civil.Nights(d.Checkin.Time, d.Checkout.Time)),
			CheckoutTime: checkoutTime,
		},
	}); err != nil {
		return err
	}

	// In the same transaction, deliberately. Marking outside it would write to
	// the same guest every fifteen minutes until the day turned over, or lose
	// the message entirely, depending on which side of a crash the write landed.
	if err := q.MarkCheckoutEmailSent(ctx, d.ID); err != nil {
		return fmt.Errorf("booking: marking the checkout email sent for %s: %w", d.Code, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("booking: committing the checkout email for %s: %w", d.Code, err)
	}
	return nil
}

// CheckoutJob adapts SendCheckoutEmails to the runner's handler signature.
//
// Today at the inn is resolved here rather than passed in, because "who is
// leaving today" is a question about the calendar in New Hampshire and the
// answer changes between runs.
func CheckoutJob(q *db.Queries, beginner Beginner) func(context.Context, []byte) error {
	return func(ctx context.Context, _ []byte) error {
		sent, err := SendCheckoutEmails(ctx, q, beginner, civil.Today())
		if sent > 0 {
			slog.Info("wrote to guests leaving today", "count", sent)
		}
		return err
	}
}
