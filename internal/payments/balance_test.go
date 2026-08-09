package payments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
)

// warned reads back the T-8 messages queued for one booking.
//
// Filtered by the booking's own code rather than counted by kind, and so needs
// neither the exclusive lock nor an emptied queue: the code is unique to this
// test's booking, so no other package's committed rows can be mistaken for
// these, and none of these can be mistaken for anybody else's.
func warned(t *testing.T, ctx context.Context, tx pgx.Tx, code string) []email.BalanceWarningData {
	t.Helper()

	rows, err := tx.Query(ctx, `
		SELECT payload
		FROM jobs
		WHERE kind = $1
		  AND payload->>'template' = $2
		  AND payload->'data'->>'Code' = $3
		ORDER BY id`, email.JobKind, email.BalanceWarning, code)
	if err != nil {
		t.Fatalf("reading queued warnings: %v", err)
	}
	defer rows.Close()

	var out []email.BalanceWarningData
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scanning a queued warning: %v", err)
		}
		var env struct {
			To   string                   `json:"to"`
			Data email.BalanceWarningData `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decoding a queued warning: %v", err)
		}
		if env.To != "ada@example.com" {
			t.Errorf("warning addressed to %q, want the guest", env.To)
		}
		out = append(out, env.Data)
	}
	return out
}

// dueForWarning is a confirmed stay that has paid its deposit and is inside the
// T-8 window, which is the only shape the warning scan looks for.
func dueForWarning(t *testing.T, ctx context.Context, q *db.Queries, tx pgx.Tx) (code string, balance int64) {
	t.Helper()

	made := held(t, ctx, tx)
	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit_"+made.Code)); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	// The stay sits at today+300, so its own T-8 is months away. Pulling the
	// charge date back to today is what a booking looks like on the day the
	// warning is due, without moving the stay into another package's calendar.
	//
	// Today at the inn, not Postgres' current_date: the container is in UTC, so
	// after 8pm Eastern the two are different days. The warning scan's range is
	// wide enough to hide that; the charge scan's is not, and there is no reason
	// for one of these helpers to date differently from the other.
	dueToday(t, ctx, tx, made.Code)
	return made.Code, made.Quote.BalanceCents
}

// The job's whole purpose: the guest hears about the charge before it happens.
func TestWarningIsQueuedForAStayDueOne(t *testing.T) {
	ctx, q, tx := setup(t)
	code, balance := dueForWarning(t, ctx, q, tx)

	sent, err := WarnBalances(ctx, q, tx, day(0))
	if err != nil {
		t.Fatalf("warning: %v", err)
	}
	if sent < 1 {
		t.Fatal("no warnings went out for a stay inside the window")
	}

	queued := warned(t, ctx, tx, code)
	if len(queued) != 1 {
		t.Fatalf("%d warnings queued, want 1", len(queued))
	}

	// The amount and the date are the message: a warning that does not say what
	// is coming or when is the surprise decision #6 exists to prevent.
	if queued[0].Amount != email.Money(balance) {
		t.Errorf("warned about %q, want the %s balance", queued[0].Amount, email.Money(balance))
	}
	if queued[0].ChargeOn == "" {
		t.Error("the warning does not say when the card will be charged")
	}
	if queued[0].GuestName == "" {
		t.Error("the warning does not name the guest")
	}
}

// The regression the mark exists for. The scan deliberately catches up rather
// than matching one day, so without MarkWarned committing alongside the queued
// message the same guest is warned every hour until they arrive.
func TestWarningIsNotSentTwice(t *testing.T) {
	ctx, q, tx := setup(t)
	code, _ := dueForWarning(t, ctx, q, tx)

	for i := range 3 {
		if _, err := WarnBalances(ctx, q, tx, day(0)); err != nil {
			t.Fatalf("warning run %d: %v", i+1, err)
		}
	}

	if queued := warned(t, ctx, tx, code); len(queued) != 1 {
		t.Errorf("%d warnings queued after three runs, want 1", len(queued))
	}
}

// shortNotice books an arrival inside the T-8 window, which decision #7 charges
// in full at booking and gives no balance_charge_at at all.
func shortNotice(t *testing.T, ctx context.Context, b booking.Beginner) booking.Booking {
	t.Helper()

	made, err := booking.Create(ctx, b, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(2),
		Checkout:         day(4),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("holding a short-notice room: %v", err)
	}
	return made
}

// A stay charged in full at booking has no T-7 charge and so nothing to warn
// about (decision #7). Its NULL balance_charge_at is the flag, not a gap.
func TestShortNoticeStayIsNeverWarned(t *testing.T) {
	ctx, q, tx := setup(t)

	made := shortNotice(t, ctx, tx)
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_full_" + made.Code,
		Kind:        KindFull,
		AmountCents: made.Quote.TotalCents,
	}); err != nil {
		t.Fatalf("charging in full: %v", err)
	}

	if _, err := WarnBalances(ctx, q, tx, day(0)); err != nil {
		t.Fatalf("warning: %v", err)
	}
	if queued := warned(t, ctx, tx, made.Code); len(queued) != 0 {
		t.Errorf("%d warnings queued for a stay paid in full", len(queued))
	}
}

// A stay still pending — the guest never paid — is not warned about a balance
// charge that will never be attempted.
func TestUnpaidHoldIsNeverWarned(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	dueToday(t, ctx, tx, made.Code)

	if _, err := WarnBalances(ctx, q, tx, day(0)); err != nil {
		t.Fatalf("warning: %v", err)
	}
	if queued := warned(t, ctx, tx, made.Code); len(queued) != 0 {
		t.Errorf("%d warnings queued for a stay that never paid", len(queued))
	}
}
