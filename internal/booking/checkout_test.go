package booking

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/email"
)

// conn is the test's own transaction, for the two things the generated queries
// deliberately do not offer: confirming a booking by hand, and reading a queued
// job's payload back out.
func conn(t *testing.T, b Beginner) pgx.Tx {
	t.Helper()
	tx, ok := b.(pgx.Tx)
	if !ok {
		t.Fatalf("setup did not hand back a transaction, got %T", b)
	}
	return tx
}

// A confirmed stay leaving on the day under test.
//
// Everything here rolls back — remindOne's transaction becomes a savepoint
// inside the test's, the same way Create's does — so these bookings never reach
// another package's stretch of calendar even though they use this one's.
func leaving(t *testing.T, ctx context.Context, b Beginner) Booking {
	t.Helper()

	made := create(t, ctx, b, request())

	// The scan only looks at confirmed stays, and confirming one properly means
	// a payment, a webhook and a gateway. None of that is what is under test.
	if _, err := conn(t, b).Exec(ctx,
		"UPDATE bookings SET status = 'confirmed' WHERE code = $1", made.Code); err != nil {
		t.Fatalf("confirming the booking: %v", err)
	}
	return made
}

// queuedCheckoutEmails counts the departure messages for one booking.
//
// By kind, template and code rather than by counting the table: `go test ./...`
// runs the packages in parallel against one database and other people's jobs
// rows are committed in it.
func queuedCheckoutEmails(t *testing.T, ctx context.Context, b Beginner, code string) int {
	t.Helper()

	var n int
	err := conn(t, b).QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE kind = $1
		  AND payload->>'template' = $2
		  AND payload->'data'->>'Code' = $3`,
		email.JobKind, email.CheckoutReminder, code).Scan(&n)
	if err != nil {
		t.Fatalf("counting queued checkout emails: %v", err)
	}
	return n
}

func TestCheckoutEmailReachesGuestsLeavingToday(t *testing.T) {
	ctx, q, b := setup(t)
	made := leaving(t, ctx, b)

	sent, err := SendCheckoutEmails(ctx, q, b, day(202))
	if err != nil {
		t.Fatalf("sending the checkout emails: %v", err)
	}
	if sent != 1 {
		t.Fatalf("%d messages sent, want 1", sent)
	}
	if got := queuedCheckoutEmails(t, ctx, b, made.Code); got != 1 {
		t.Fatalf("%d messages queued for %s, want 1", got, made.Code)
	}

	// The payload the owner's copy will be written against. The checkout hour
	// comes from settings rather than from the words, so the sentence about it
	// stays true when the owner changes the setting.
	var payload struct {
		CheckoutTime string
		GuestName    string
		Rooms        []string
		Nights       string
	}
	err = conn(t, b).QueryRow(ctx, `
		SELECT payload->'data' FROM jobs
		WHERE kind = $1 AND payload->'data'->>'Code' = $2`,
		email.JobKind, made.Code).Scan(&payload)
	if err != nil {
		t.Fatalf("reading the queued payload: %v", err)
	}

	if payload.CheckoutTime != "11:00 AM" {
		t.Errorf("checkout time %q, want the seeded 11:00", payload.CheckoutTime)
	}
	if payload.GuestName != "Ada Lovelace" {
		t.Errorf("guest %q, want the one who booked", payload.GuestName)
	}
	if len(payload.Rooms) != 1 || payload.Rooms[0] == "" {
		t.Errorf("rooms %v, want the room they stayed in", payload.Rooms)
	}
	if payload.Nights != "2" {
		t.Errorf("nights %q, want 2", payload.Nights)
	}
}

// The scan runs every fifteen minutes all day. Without the marker written in
// the same transaction as the message, a guest hears from the inn ninety-six
// times on the morning they leave.
func TestCheckoutEmailIsSentOnceOnly(t *testing.T) {
	ctx, q, b := setup(t)
	made := leaving(t, ctx, b)

	if _, err := SendCheckoutEmails(ctx, q, b, day(202)); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	sent, err := SendCheckoutEmails(ctx, q, b, day(202))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if sent != 0 {
		t.Errorf("%d messages sent on the second pass, want 0", sent)
	}
	if got := queuedCheckoutEmails(t, ctx, b, made.Code); got != 1 {
		t.Errorf("%d messages queued for %s after two passes, want 1", got, made.Code)
	}
}

// The day itself, and no other. The T-8 warning deliberately catches up when
// the server was off, because a late warning still does its job; this message
// says the guest is leaving today, and one that arrives after they got home is
// a thing the inn told them that was not true.
func TestCheckoutEmailIsOnlySentOnTheDayItself(t *testing.T) {
	for _, c := range []struct {
		name   string
		offset int
	}{
		{"the day before", 201},
		{"the day after", 203},
		{"a week late", 209},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, q, b := setup(t)
			made := leaving(t, ctx, b)

			sent, err := SendCheckoutEmails(ctx, q, b, day(c.offset))
			if err != nil {
				t.Fatalf("sending: %v", err)
			}
			if sent != 0 {
				t.Errorf("%d messages sent, want 0", sent)
			}
			if got := queuedCheckoutEmails(t, ctx, b, made.Code); got != 0 {
				t.Errorf("%d messages queued for %s, want 0", got, made.Code)
			}
		})
	}
}

// A hold that was never paid for is not a stay, and nobody is leaving it.
func TestCheckoutEmailSkipsStaysThatWereNeverConfirmed(t *testing.T) {
	ctx, q, b := setup(t)
	made := create(t, ctx, b, request())

	sent, err := SendCheckoutEmails(ctx, q, b, day(202))
	if err != nil {
		t.Fatalf("sending: %v", err)
	}
	if sent != 0 {
		t.Errorf("%d messages sent for a pending booking, want 0", sent)
	}
	if got := queuedCheckoutEmails(t, ctx, b, made.Code); got != 0 {
		t.Errorf("%d messages queued for a pending booking, want 0", got)
	}
}
