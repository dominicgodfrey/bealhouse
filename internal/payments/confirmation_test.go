package payments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/email"
)

// queuedMail reads back the messages queued for one booking, by template.
//
// Filtered on the booking's own code, so it needs neither the exclusive lock
// nor an emptied queue: no other package's committed rows can be mistaken for
// these.
func queuedMail(t *testing.T, ctx context.Context, tx pgx.Tx, template, code string) []map[string]any {
	t.Helper()

	rows, err := tx.Query(ctx, `
		SELECT payload
		FROM jobs
		WHERE kind = $1
		  AND payload->>'template' = $2
		  AND payload->'data'->>'Code' = $3
		ORDER BY id`, email.JobKind, template, code)
	if err != nil {
		t.Fatalf("reading queued mail: %v", err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scanning queued mail: %v", err)
		}
		var env map[string]any
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatalf("decoding queued mail: %v", err)
		}
		out = append(out, env)
	}
	return out
}

func field(t *testing.T, env map[string]any, key string) string {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("the message carries no data")
	}
	value, _ := data[key].(string)
	return value
}

// The guest has been charged by the time this runs, so the confirmation is not
// optional. It is queued inside the transaction that confirmed the stay, which
// is what makes losing it impossible rather than unlikely.
func TestConfirmingAStayQueuesTheGuestsConfirmation(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_confirm")); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	queued := queuedMail(t, ctx, tx, email.BookingConfirmation, made.Code)
	if len(queued) != 1 {
		t.Fatalf("%d confirmations queued, want 1", len(queued))
	}
	if to, _ := queued[0]["to"].(string); to != "ada@example.com" {
		t.Errorf("confirmation addressed to %q", to)
	}

	if got := field(t, queued[0], "PaidNow"); got != email.Money(made.Quote.DepositCents) {
		t.Errorf("PaidNow is %q, want the deposit", got)
	}
	if got := field(t, queued[0], "Total"); got != email.Money(made.Quote.TotalCents) {
		t.Errorf("Total is %q", got)
	}

	// A stay with a balance to come has to say when it is coming, or the T-7
	// charge is the surprise decision #6 exists to prevent.
	if field(t, queued[0], "BalanceChargeOn") == "" {
		t.Error("the confirmation does not say when the balance will be charged")
	}
	if got := field(t, queued[0], "BalanceDue"); got != email.Money(made.Quote.BalanceCents) {
		t.Errorf("BalanceDue is %q, want the balance", got)
	}
}

// Decision #7: nothing more is ever taken, so the message must not promise a
// charge that will never happen. Empty fields are how the template tells the
// two kinds of booking apart.
func TestConfirmationForAStayPaidInFullPromisesNoFurtherCharge(t *testing.T) {
	ctx, _, tx := setup(t)
	made := shortNotice(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_full_" + made.Code,
		Kind:        KindFull,
		AmountCents: made.Quote.TotalCents,
	}); err != nil {
		t.Fatalf("charging in full: %v", err)
	}

	queued := queuedMail(t, ctx, tx, email.BookingConfirmation, made.Code)
	if len(queued) != 1 {
		t.Fatalf("%d confirmations queued, want 1", len(queued))
	}
	if got := field(t, queued[0], "BalanceChargeOn"); got != "" {
		t.Errorf("BalanceChargeOn is %q on a stay paid in full", got)
	}
	if got := field(t, queued[0], "BalanceDue"); got != "" {
		t.Errorf("BalanceDue is %q on a stay paid in full", got)
	}
}

// The owner's copy carries the guest's contact details, which the guest's own
// confirmation does not need and the public booking API never returns.
func TestOwnerIsNotifiedWhenAnAddressIsGiven(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	charge := deposit(made, "pi_confirm")
	charge.OwnerEmail = "owner@bealhouse.test"
	if _, err := RecordCharge(ctx, tx, charge); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	queued := queuedMail(t, ctx, tx, email.OwnerNotification, made.Code)
	if len(queued) != 1 {
		t.Fatalf("%d owner notifications queued, want 1", len(queued))
	}
	if to, _ := queued[0]["to"].(string); to != "owner@bealhouse.test" {
		t.Errorf("owner notification addressed to %q", to)
	}
	if got := field(t, queued[0], "GuestEmail"); got != "ada@example.com" {
		t.Errorf("the owner's copy does not carry the guest's address (%q)", got)
	}
}

func TestNoOwnerAddressMeansNoOwnerNotification(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_confirm")); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}
	if queued := queuedMail(t, ctx, tx, email.OwnerNotification, made.Code); len(queued) != 0 {
		t.Errorf("%d owner notifications queued with no address configured", len(queued))
	}
}

// Stripe delivers at least once. A second delivery must not send the guest a
// second confirmation for a stay that was confirmed once.
func TestRedeliveryDoesNotConfirmTwice(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	for range 3 {
		if _, err := RecordCharge(ctx, tx, deposit(made, "pi_confirm")); err != nil {
			t.Fatalf("recording the deposit: %v", err)
		}
	}

	if queued := queuedMail(t, ctx, tx, email.BookingConfirmation, made.Code); len(queued) != 1 {
		t.Errorf("%d confirmations queued after three deliveries, want 1", len(queued))
	}
}

// Money arrived but not enough of it, so nothing is confirmed — and a guest
// must not be told their stay is booked when it is not.
func TestUnderpaymentConfirmsNothingAndTellsNobody(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	short := deposit(made, "pi_short")
	short.AmountCents = made.Quote.DepositCents - 100
	got, err := RecordCharge(ctx, tx, short)
	if err != nil {
		t.Fatalf("recording: %v", err)
	}
	if got.Outcome != Underpaid {
		t.Fatalf("outcome %q, want %q", got.Outcome, Underpaid)
	}

	if queued := queuedMail(t, ctx, tx, email.BookingConfirmation, made.Code); len(queued) != 0 {
		t.Errorf("%d confirmations queued for a stay that is not confirmed", len(queued))
	}
}

// The T-7 charge lands on a stay that was confirmed weeks ago. It is a receipt,
// not a confirmation, and sending a second confirmation would read as a second
// booking.
func TestBalanceChargeDoesNotResendTheConfirmation(t *testing.T) {
	ctx, _, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_balance",
		Kind:        KindBalance,
		AmountCents: made.Quote.BalanceCents,
	}); err != nil {
		t.Fatalf("balance: %v", err)
	}

	if queued := queuedMail(t, ctx, tx, email.BookingConfirmation, made.Code); len(queued) != 1 {
		t.Errorf("%d confirmations queued, want the one from the deposit", len(queued))
	}
}
