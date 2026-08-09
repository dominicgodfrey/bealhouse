package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
	"bealhouse/internal/testdb"
)

// Every test here runs inside a rolled-back transaction. RecordCharge's own
// transaction becomes a savepoint inside it — as do the nested savepoints it
// takes to survive a lost race — so the whole state machine is exercised for
// real and none of it survives the test.
//
// The dates sit at today+300, in a stretch of calendar no other package writes
// to. `go test ./...` runs packages in parallel against one database, and a
// booking committed inside somebody else's window is how a suite like this
// starts lying.
const (
	stayStart = 300
	stayEnd   = 302
)

func setup(t *testing.T) (context.Context, *db.Queries, pgx.Tx) {
	t.Helper()
	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	return context.Background(), db.New(tx), tx
}

func day(offset int) time.Time { return civil.AddDays(civil.Today(), offset) }

// held books a room the way a guest does, leaving a pending booking and the
// hold that reserves its room.
func held(t *testing.T, ctx context.Context, b booking.Beginner) booking.Booking {
	t.Helper()

	made, err := booking.Create(ctx, b, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(stayStart),
		Checkout:         day(stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Ada Lovelace", Email: "ada@example.com", Phone: "603-555-0100"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("holding a room: %v", err)
	}
	return made
}

func deposit(made booking.Booking, id string) Charge {
	return Charge{
		BookingCode: made.Code,
		StripeID:    id,
		Kind:        KindDeposit,
		AmountCents: made.Quote.DepositCents,
	}
}

// state reads back the parts of a booking these tests assert on.
func state(t *testing.T, ctx context.Context, q *db.Queries, code string) db.GetBookingForPaymentRow {
	t.Helper()
	row, err := q.GetBookingForPayment(ctx, code)
	if err != nil {
		t.Fatalf("reading booking %s: %v", code, err)
	}
	return row
}

// occupancyKinds reports what the booking currently occupies.
func occupancyKinds(t *testing.T, ctx context.Context, tx pgx.Tx, bookingID int64) []string {
	t.Helper()

	rows, err := tx.Query(ctx,
		`SELECT kind || CASE WHEN expires_at IS NULL THEN '' ELSE '/expiring' END
		 FROM room_occupancy WHERE booking_id = $1 ORDER BY id`, bookingID)
	if err != nil {
		t.Fatalf("reading occupancy: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scanning occupancy: %v", err)
		}
		out = append(out, kind)
	}
	return out
}

// sweepHold ages the hold past its TTL and reclaims it, standing in for a guest
// who took longer over their card details than the fifteen minutes allowed.
func sweepHold(t *testing.T, ctx context.Context, q *db.Queries, tx pgx.Tx, code string) {
	t.Helper()

	if _, err := tx.Exec(ctx, `
		UPDATE room_occupancy o
		SET expires_at = now() - interval '1 minute'
		FROM bookings b
		WHERE b.id = o.booking_id AND b.code = $1 AND o.kind = 'hold'`, code); err != nil {
		t.Fatalf("ageing the hold: %v", err)
	}
	if _, _, err := booking.Sweep(ctx, q); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
}

// The ordinary path: the money lands while the hold is still live.
func TestDepositConfirmsTheStayAndPromotesTheHold(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	got, err := RecordCharge(ctx, tx, deposit(made, "pi_ordinary"))
	if err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}
	if got.Outcome != Confirmed {
		t.Errorf("outcome %q, want %q", got.Outcome, Confirmed)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q, want confirmed", after.Status)
	}
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the %d deposit", after.AmountPaidCents, made.Quote.DepositCents)
	}

	// The hold became the stay in place. Had it been deleted and re-inserted,
	// the room would have been free for an instant in between.
	if kinds := occupancyKinds(t, ctx, tx, got.BookingID); len(kinds) != 1 || kinds[0] != "booking" {
		t.Errorf("occupancy is %v, want one non-expiring booking row", kinds)
	}
}

// Stripe delivers at least once and redelivers on any non-2xx. The second
// delivery must change nothing at all — above all it must not add to the gross
// collected a second time.
func TestRedeliveredChargeChangesNothing(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_twice")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	again, err := RecordCharge(ctx, tx, deposit(made, "pi_twice"))
	if err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if again.Outcome != AlreadyApplied {
		t.Errorf("outcome %q on redelivery, want %q", again.Outcome, AlreadyApplied)
	}

	after := state(t, ctx, q, made.Code)
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d after a redelivery, want the %d deposit charged once",
			after.AmountPaidCents, made.Quote.DepositCents)
	}

	rows, err := q.ListPaymentsForBooking(ctx, after.ID)
	if err != nil {
		t.Fatalf("listing payments: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("%d ledger rows, want 1", len(rows))
	}
}

// Regression, and the most expensive bug this package has had.
//
// The Payment Element retries a declined card on the *same* PaymentIntent: the
// intent goes back to requires_payment_method and the guest tries another card.
// While payments.stripe_id was unique on its own, the succeeded row collided
// with the failed one, RecordCharge read the empty insert as "already applied",
// and returned before adding anything to the gross. The guest's second card was
// charged, the booking stayed pending, and the sweeper resold the room.
//
// Both attempts belong in the ledger, and the stay has to end up confirmed.
func TestDeclinedCardRetriedOnTheSameIntentStillConfirms(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	const intent = "pi_declined_then_retried"

	if _, err := RecordFailure(ctx, tx, deposit(made, intent)); err != nil {
		t.Fatalf("recording the decline: %v", err)
	}

	res, err := RecordCharge(ctx, tx, deposit(made, intent))
	if err != nil {
		t.Fatalf("recording the successful retry: %v", err)
	}
	if res.Outcome != Confirmed {
		t.Errorf("outcome %q on the retry, want %q", res.Outcome, Confirmed)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q after a successful retry, want confirmed", after.Status)
	}
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the %d deposit — the guest was charged either way",
			after.AmountPaidCents, made.Quote.DepositCents)
	}
	if kinds := occupancyKinds(t, ctx, tx, after.ID); len(kinds) != 1 || kinds[0] != "booking" {
		t.Errorf("occupancy %v, want the hold promoted to a booking", kinds)
	}

	// The decline is still on the record: it is what an owner looking at a
	// disputed charge needs to see.
	rows, err := q.ListPaymentsForBooking(ctx, after.ID)
	if err != nil {
		t.Fatalf("listing payments: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d ledger rows, want 2 — one failed attempt and one success", len(rows))
	}
	if rows[0].Status != "failed" || rows[1].Status != "succeeded" {
		t.Errorf("ledger reads %q then %q, want failed then succeeded",
			rows[0].Status, rows[1].Status)
	}
}

// A redelivery of the *same* outcome must still change nothing, which is the
// half of the index that was always right and must survive the fix above.
func TestRedeliveredFailureIsRecordedOnce(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	for range 2 {
		if _, err := RecordFailure(ctx, tx, deposit(made, "pi_declined_twice")); err != nil {
			t.Fatalf("recording the decline: %v", err)
		}
	}

	after := state(t, ctx, q, made.Code)
	rows, err := q.ListPaymentsForBooking(ctx, after.ID)
	if err != nil {
		t.Fatalf("listing payments: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("%d ledger rows after a redelivered decline, want 1", len(rows))
	}
}

// Money that does not cover what the booking says was due at booking buys
// nothing. The stay is left pending and its hold left to lapse, so the room
// goes back on sale rather than being confirmed for whatever turned up.
func TestUnderpaymentIsRecordedButConfirmsNothing(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	short := deposit(made, "pi_one_dollar")
	short.AmountCents = 100

	res, err := RecordCharge(ctx, tx, short)
	if err != nil {
		t.Fatalf("recording the short payment: %v", err)
	}
	if res.Outcome != Underpaid {
		t.Fatalf("outcome %q, want %q", res.Outcome, Underpaid)
	}
	if want := made.Quote.DepositCents - 100; res.ShortfallCents != want {
		t.Errorf("shortfall %d, want %d", res.ShortfallCents, want)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusPending {
		t.Errorf("status %q after an underpayment, want it left pending", after.Status)
	}
	// The money is real and is recorded as such: the ledger does not get to be
	// selective about payments it disapproves of.
	if after.AmountPaidCents != 100 {
		t.Errorf("collected %d, want the 100 that actually arrived", after.AmountPaidCents)
	}
	if kinds := occupancyKinds(t, ctx, tx, after.ID); len(kinds) != 1 || kinds[0] != "hold/expiring" {
		t.Errorf("occupancy %v, want the hold left alone to lapse", kinds)
	}
}

// ...and topping it up finishes the job, so a guest who really did pay in two
// goes is not left stranded by the guard above.
func TestToppingUpAnUnderpaymentConfirms(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	short := deposit(made, "pi_part_one")
	short.AmountCents = 100
	if _, err := RecordCharge(ctx, tx, short); err != nil {
		t.Fatalf("recording the first part: %v", err)
	}

	rest := deposit(made, "pi_part_two")
	rest.AmountCents = made.Quote.DepositCents - 100
	res, err := RecordCharge(ctx, tx, rest)
	if err != nil {
		t.Fatalf("recording the remainder: %v", err)
	}
	if res.Outcome != Confirmed {
		t.Errorf("outcome %q once the deposit is covered, want %q", res.Outcome, Confirmed)
	}

	if after := state(t, ctx, q, made.Code); after.Status != booking.StatusConfirmed {
		t.Errorf("status %q, want confirmed once the full deposit arrived", after.Status)
	}
}

// The event id and the payment it describes commit together. A redelivery of an
// event already recorded stops before touching the gross — and, more to the
// point, an event is only ever on record because its transaction committed.
func TestEventIDIsRecordedWithThePaymentAndStopsARedelivery(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	charge := deposit(made, "pi_with_event")
	charge.EventID = "evt_delivered_twice"
	charge.EventType = "payment_intent.succeeded"

	if _, err := RecordCharge(ctx, tx, charge); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	again, err := RecordCharge(ctx, tx, charge)
	if err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if again.Outcome != AlreadyApplied {
		t.Errorf("outcome %q on redelivery, want %q", again.Outcome, AlreadyApplied)
	}

	after := state(t, ctx, q, made.Code)
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the deposit once", after.AmountPaidCents)
	}

	// Seen agrees, because the event really is on record — written by the same
	// transaction that recorded the payment rather than by a separate one that
	// could have committed without it.
	seen, err := Seen(ctx, q, "evt_delivered_twice", "payment_intent.succeeded")
	if err != nil {
		t.Fatalf("checking the event: %v", err)
	}
	if !seen {
		t.Error("the event is not on record, so a redelivery would be reprocessed")
	}

	// Stripe's own vocabulary, not the inn's word for which part of the money
	// this was. An audit table that says "deposit" cannot be reconciled against
	// the Stripe dashboard.
	var eventType string
	if err := tx.QueryRow(ctx,
		"SELECT type FROM stripe_events WHERE id = $1", "evt_delivered_twice",
	).Scan(&eventType); err != nil {
		t.Fatalf("reading the event back: %v", err)
	}
	if eventType != "payment_intent.succeeded" {
		t.Errorf("stripe_events.type = %q, want Stripe's event type", eventType)
	}
}

// Hazard: the guest pays after their hold has been reclaimed, but nobody else
// took the room. It is simply taken back.
func TestChargeAfterTheHoldLapsedReclaimsTheRoom(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	sweepHold(t, ctx, q, tx, made.Code)

	// The sweeper did its job: no occupancy, and the booking says what happened.
	if kinds := occupancyKinds(t, ctx, tx, state(t, ctx, q, made.Code).ID); len(kinds) != 0 {
		t.Fatalf("occupancy is %v, want nothing after the sweep", kinds)
	}
	if s := state(t, ctx, q, made.Code).Status; s != booking.StatusExpired {
		t.Fatalf("status %q after the sweep, want expired", s)
	}

	got, err := RecordCharge(ctx, tx, deposit(made, "pi_late_but_free"))
	if err != nil {
		t.Fatalf("recording the late deposit: %v", err)
	}
	if got.Outcome != Confirmed {
		t.Errorf("outcome %q, want %q — the room was still free", got.Outcome, Confirmed)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q, want confirmed", after.Status)
	}
	if kinds := occupancyKinds(t, ctx, tx, got.BookingID); len(kinds) != 1 || kinds[0] != "booking" {
		t.Errorf("occupancy is %v, want the room taken back as a booking", kinds)
	}
}

// The hazard that costs real money: the guest pays after their hold lapsed and
// the room has already gone to somebody else. Nobody may end up charged for a
// room another guest is standing in, so the charge is recorded, the stay is
// cancelled, and the whole amount is owed back.
func TestChargeAfterTheRoomWasResoldOwesARefund(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	sweepHold(t, ctx, q, tx, made.Code)

	// Somebody else takes the room for the same nights.
	roomID, err := q.GetRoomIDBySlug(ctx, "rose-chamber")
	if err != nil {
		t.Fatalf("looking up the room: %v", err)
	}
	if _, err := occupancy.Create(ctx, q, db.CreateOccupancyParams{
		RoomID:   roomID,
		Checkin:  pgtype.Date{Time: day(stayStart), Valid: true},
		Checkout: pgtype.Date{Time: day(stayEnd), Valid: true},
		Kind:     "booking",
		Source:   "direct",
	}); err != nil {
		t.Fatalf("reselling the room: %v", err)
	}

	got, err := RecordCharge(ctx, tx, deposit(made, "pi_too_late"))
	if err != nil {
		t.Fatalf("recording the doomed deposit: %v", err)
	}
	if got.Outcome != RefundDue {
		t.Fatalf("outcome %q, want %q", got.Outcome, RefundDue)
	}
	if got.RefundCents != made.Quote.DepositCents {
		t.Errorf("refund %d, want the whole %d collected — the guest is not at fault here",
			got.RefundCents, made.Quote.DepositCents)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}
	// Recorded honestly even though it has to go back: the money did move.
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the charge recorded as %d",
			after.AmountPaidCents, made.Quote.DepositCents)
	}
	if kinds := occupancyKinds(t, ctx, tx, got.BookingID); len(kinds) != 0 {
		t.Errorf("occupancy is %v, want none — the room belongs to the other guest", kinds)
	}

	// And the transaction survived the lost race: the ledger row is readable,
	// which is the whole point of taking the savepoint.
	rows, err := q.ListPaymentsForBooking(ctx, after.ID)
	if err != nil || len(rows) != 1 {
		t.Errorf("%d ledger rows (err %v), want the charge recorded", len(rows), err)
	}
}

// The T-7 charge lands on a stay that is already confirmed and already occupies
// its room. There is no hold to promote and nothing to re-claim.
func TestBalanceChargeOnAConfirmedStayLeavesTheRoomAlone(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	got, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_balance",
		Kind:        KindBalance,
		AmountCents: made.Quote.BalanceCents,
	})
	if err != nil {
		t.Fatalf("balance charge: %v", err)
	}
	if got.Outcome != Confirmed {
		t.Errorf("outcome %q, want %q", got.Outcome, Confirmed)
	}

	after := state(t, ctx, q, made.Code)
	if after.AmountPaidCents != made.Quote.TotalCents {
		t.Errorf("collected %d, want the full %d", after.AmountPaidCents, made.Quote.TotalCents)
	}
	if kinds := occupancyKinds(t, ctx, tx, got.BookingID); len(kinds) != 1 || kinds[0] != "booking" {
		t.Errorf("occupancy is %v, want the one booking row untouched", kinds)
	}
}

// A declined T-7 charge leaves the stay confirmed — the guest is still coming —
// and raises a flag the owner cannot miss.
func TestFailedBalanceChargeFlagsRatherThanCancels(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	if _, err := RecordFailure(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_declined",
		Kind:        KindBalance,
		AmountCents: made.Quote.BalanceCents,
	}); err != nil {
		t.Fatalf("recording the decline: %v", err)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q after a declined balance charge, want it left confirmed", after.Status)
	}
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the deposit only — a decline collects nothing",
			after.AmountPaidCents)
	}

	var flagged bool
	if err := tx.QueryRow(ctx,
		"SELECT balance_charge_failed_at IS NOT NULL FROM bookings WHERE id = $1", after.ID,
	).Scan(&flagged); err != nil {
		t.Fatalf("reading the flag: %v", err)
	}
	if !flagged {
		t.Error("a declined balance charge left no flag for the owner")
	}
}

// Refunds are recorded, never subtracted from the gross. Reducing
// amount_paid_cents would make a second cancellation compute a smaller refund
// off an already-reduced figure.
func TestRefundCancelsTheStayWithoutRewritingWhatWasCollected(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	got, err := RecordRefund(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "re_full",
		Kind:        KindRefund,
		AmountCents: made.Quote.DepositCents,
	})
	if err != nil {
		t.Fatalf("recording the refund: %v", err)
	}
	if got.Outcome != RefundDue {
		t.Errorf("outcome %q, want %q", got.Outcome, RefundDue)
	}

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("gross collected is now %d; it must stay %d so a second refund cannot be computed off a reduced figure",
			after.AmountPaidCents, made.Quote.DepositCents)
	}
	if kinds := occupancyKinds(t, ctx, tx, after.ID); len(kinds) != 0 {
		t.Errorf("occupancy is %v, want the room back on sale", kinds)
	}

	// The ledger carries both movements, which is what makes the net derivable.
	rows, err := q.ListPaymentsForBooking(ctx, after.ID)
	if err != nil {
		t.Fatalf("listing payments: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d ledger rows, want the charge and the refund", len(rows))
	}
}

// A late payment against a stay the guest already cancelled cannot revive it:
// the room went back on sale and somebody may be in it.
func TestChargeAgainstACancelledStayOwesARefund(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	after := state(t, ctx, q, made.Code)
	if err := q.CancelBooking(ctx, after.ID); err != nil {
		t.Fatalf("cancelling: %v", err)
	}

	got, err := RecordCharge(ctx, tx, deposit(made, "pi_after_cancel"))
	if err != nil {
		t.Fatalf("recording: %v", err)
	}
	if got.Outcome != RefundDue {
		t.Errorf("outcome %q, want %q", got.Outcome, RefundDue)
	}
}

func TestUnknownBookingIsRejected(t *testing.T) {
	ctx, _, tx := setup(t)

	_, err := RecordCharge(ctx, tx, Charge{
		BookingCode: "BH-ZZZZZZ",
		StripeID:    "pi_orphan",
		Kind:        KindDeposit,
		AmountCents: 1000,
	})
	if !errors.Is(err, ErrBookingNotFound) {
		t.Errorf("got %v, want ErrBookingNotFound", err)
	}
}

func TestChargeValidation(t *testing.T) {
	ctx, _, tx := setup(t)

	tests := []struct {
		name string
		in   Charge
		want error
	}{
		{"no stripe id", Charge{BookingCode: "BH-AAAAAA", Kind: KindDeposit, AmountCents: 1}, ErrStripeIDRequired},
		{"zero amount", Charge{BookingCode: "BH-AAAAAA", StripeID: "pi_1", Kind: KindDeposit}, ErrAmountNotPositive},
		{"negative amount", Charge{BookingCode: "BH-AAAAAA", StripeID: "pi_1", Kind: KindDeposit, AmountCents: -50}, ErrAmountNotPositive},
		{"unknown kind", Charge{BookingCode: "BH-AAAAAA", StripeID: "pi_1", Kind: "tip", AmountCents: 1}, ErrKindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RecordCharge(ctx, tx, tt.in); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// Events that write no payment row still have to be recognised on redelivery,
// or Stripe reprocesses them for as long as it keeps retrying.
func TestSeenIsTrueOnlyTheSecondTime(t *testing.T) {
	ctx, q, _ := setup(t)

	seen, err := Seen(ctx, q, "evt_abc", "payment_intent.succeeded")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if seen {
		t.Error("a brand new event was reported as already handled")
	}

	seen, err = Seen(ctx, q, "evt_abc", "payment_intent.succeeded")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !seen {
		t.Error("a redelivered event was not recognised")
	}
}

// The T-7 scan is the schedule. It has to stop finding a booking the moment its
// balance is collected, because that is what makes running it every minute
// free and a missed day self-correcting.
func TestBalanceScanStopsFindingAStayOnceItIsPaid(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	chargeOn := day(stayStart - 7)

	due, err := DueForBalanceCharge(ctx, q, chargeOn)
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	found := findDue(due, made.Code)
	if found == nil {
		t.Fatalf("the stay is not due at T-7; scan returned %d rows", len(due))
	}
	if found.AmountCents != made.Quote.BalanceCents {
		t.Errorf("due %d, want the %d balance", found.AmountCents, made.Quote.BalanceCents)
	}

	// The day before, it is not yet due.
	early, err := DueForBalanceCharge(ctx, q, day(stayStart-8))
	if err != nil {
		t.Fatalf("scanning early: %v", err)
	}
	if findDue(early, made.Code) != nil {
		t.Error("the stay was due a day before T-7")
	}

	// Once the balance lands, it drops out.
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_balance",
		Kind:        KindBalance,
		AmountCents: made.Quote.BalanceCents,
	}); err != nil {
		t.Fatalf("balance: %v", err)
	}

	after, err := DueForBalanceCharge(ctx, q, chargeOn)
	if err != nil {
		t.Fatalf("scanning after payment: %v", err)
	}
	if findDue(after, made.Code) != nil {
		t.Error("a fully paid stay is still due for its balance charge")
	}
}

// Regression: the T-8 warning used to match the charge date exactly, so a
// server that was switched off that day never sent it and the guest's card was
// charged at T-7 with no notice — the one thing decision #6 asks the warning to
// prevent. It catches up now, and MarkWarned is what keeps it from repeating.
func TestBalanceWarningCatchesUpAndThenStops(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// T-8 is the day the warning is meant to go out.
	onTime, err := DueForBalanceWarning(ctx, q, day(stayStart-8))
	if err != nil {
		t.Fatalf("scanning on time: %v", err)
	}
	if findDue(onTime, made.Code) == nil {
		t.Fatal("the stay was not due a warning at T-8")
	}

	// A day too early it is not due yet.
	early, err := DueForBalanceWarning(ctx, q, day(stayStart-9))
	if err != nil {
		t.Fatalf("scanning early: %v", err)
	}
	if findDue(early, made.Code) != nil {
		t.Error("the stay was warned a day before T-8")
	}

	// The server was down for T-8. The warning is late, but it is still worth
	// sending, so the scan must still find it.
	late, err := DueForBalanceWarning(ctx, q, day(stayStart-6))
	if err != nil {
		t.Fatalf("scanning late: %v", err)
	}
	if findDue(late, made.Code) == nil {
		t.Error("a missed T-8 warning was skipped permanently rather than caught up")
	}

	// Once it has gone out it stops, or the guest is warned every day until
	// they arrive.
	if err := MarkWarned(ctx, q, state(t, ctx, q, made.Code).ID); err != nil {
		t.Fatalf("marking warned: %v", err)
	}
	after, err := DueForBalanceWarning(ctx, q, day(stayStart-6))
	if err != nil {
		t.Fatalf("scanning after warning: %v", err)
	}
	if findDue(after, made.Code) != nil {
		t.Error("a stay already warned is still due a warning")
	}
}

// Decision #7: a short-notice arrival is charged in full and has no T-7 job at
// all, which the NULL balance_charge_at encodes. It must never appear in the
// scan, whatever date is asked for.
func TestShortNoticeStaysNeverAppearInTheBalanceScan(t *testing.T) {
	ctx, q, tx := setup(t)

	made, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(2),
		Checkout:         day(4),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com", Phone: "603-555-0101"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("holding a short-notice room: %v", err)
	}
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_full",
		Kind:        KindFull,
		AmountCents: made.Quote.TotalCents,
	}); err != nil {
		t.Fatalf("charging in full: %v", err)
	}

	for _, offset := range []int{-10, -5, 0, 2, 10} {
		due, err := DueForBalanceCharge(ctx, q, day(offset))
		if err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if findDue(due, made.Code) != nil {
			t.Errorf("a short-notice stay was scheduled for a balance charge at today%+d", offset)
		}
	}
}

// Decision #26: cancelling in time is a full refund from the guest's side, less
// the card processor's cut, which Stripe keeps whether or not the payment is
// refunded. Without this the inn pays for every cancellation it had no part in.
func TestEarlyCancellationStillKeepsTheProcessorsCut(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	if _, err := RecordCharge(ctx, tx, deposit(made, "pi_deposit")); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Cancelling today, with arrival still 300 days out: comfortably in time.
	quote, err := RefundFor(ctx, q, made.Code, civil.Today())
	if err != nil {
		t.Fatalf("quoting the refund: %v", err)
	}
	if quote.Late {
		t.Fatal("a cancellation 300 days out was treated as late")
	}

	paid := made.Quote.DepositCents
	wantKept := (paid*3 + 99) / 100 // 3%, rounded up
	if quote.RetainedCents != wantKept {
		t.Errorf("retained %d, want %d (3%% of the %d collected, rounded up)",
			quote.RetainedCents, wantKept, paid)
	}
	if quote.RefundCents != paid-wantKept {
		t.Errorf("refund %d, want %d", quote.RefundCents, paid-wantKept)
	}
	if quote.RefundCents >= paid {
		t.Error("an early cancellation refunded everything; the processor's cut is unrecoverable")
	}
}

// A late cancellation is unchanged: the forfeited deposit already covers the
// processor many times over, so the fee is absorbed rather than added.
func TestLateCancellationForfeitsTheDepositAndNoMore(t *testing.T) {
	ctx, q, tx := setup(t)

	// A stay arriving in four days is inside the cancellation window, and is
	// charged in full at booking (decision #7).
	made, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          day(4),
		Checkout:         day(6),
		Guests:           2,
		Guest:            booking.Guest{Name: "Grace Hopper", Email: "grace@example.com", Phone: "603-555-0101"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("booking: %v", err)
	}
	if _, err := RecordCharge(ctx, tx, Charge{
		BookingCode: made.Code,
		StripeID:    "pi_full",
		Kind:        KindFull,
		AmountCents: made.Quote.TotalCents,
	}); err != nil {
		t.Fatalf("charging in full: %v", err)
	}

	quote, err := RefundFor(ctx, q, made.Code, civil.Today())
	if err != nil {
		t.Fatalf("quoting the refund: %v", err)
	}
	if !quote.Late {
		t.Fatal("a cancellation four days before arrival was not treated as late")
	}
	if quote.RetainedCents != made.Quote.DepositCents {
		t.Errorf("retained %d, want exactly the %d deposit — the processor's cut must be absorbed, not added",
			quote.RetainedCents, made.Quote.DepositCents)
	}
	if quote.RefundCents != made.Quote.BalanceCents {
		t.Errorf("refund %d, want the %d balance", quote.RefundCents, made.Quote.BalanceCents)
	}
}

// Hazard: the sweeper and a guest mid-payment.
//
// Before payments existed, every pending booking with no hold was abandoned by
// definition. Now one can be a guest halfway through a 3-D Secure challenge,
// and telling them their room is gone moments before their money arrives is
// both wrong and unnecessary. The hold is still reclaimed on its own TTL — the
// room goes back on sale regardless — but the booking is left alone while the
// payment is in flight.
func TestSweeperLeavesABookingAloneWhileItsPaymentIsInFlight(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	before := state(t, ctx, q, made.Code)
	if err := StartPayment(ctx, q, before.ID, "pi_in_flight"); err != nil {
		t.Fatalf("starting payment: %v", err)
	}

	sweepHold(t, ctx, q, tx, made.Code)

	after := state(t, ctx, q, made.Code)
	if after.Status != booking.StatusPending {
		t.Errorf("status %q, want it left pending while the payment is in flight", after.Status)
	}

	// The room really did go back on sale: the grace period protects the
	// booking's bookkeeping, never the room.
	if kinds := occupancyKinds(t, ctx, tx, after.ID); len(kinds) != 0 {
		t.Errorf("occupancy is %v, want the hold reclaimed regardless", kinds)
	}

	// ...and the guest can still complete, because the room was not resold.
	got, err := RecordCharge(ctx, tx, deposit(made, "pi_in_flight"))
	if err != nil {
		t.Fatalf("completing the payment: %v", err)
	}
	if got.Outcome != Confirmed {
		t.Errorf("outcome %q, want %q", got.Outcome, Confirmed)
	}
}

// The grace period is bounded. A guest who opened the payment page and walked
// away must not leave a booking pending forever.
func TestSweeperExpiresABookingOnceTheGracePeriodPasses(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	before := state(t, ctx, q, made.Code)
	if err := StartPayment(ctx, q, before.ID, "pi_abandoned"); err != nil {
		t.Fatalf("starting payment: %v", err)
	}

	// Age the attempt past settings.payment_grace_minutes rather than waiting
	// half an hour for it.
	if _, err := tx.Exec(ctx, `
		UPDATE bookings
		SET payment_started_at = now() - (SELECT payment_grace_minutes * interval '1 minute' FROM settings)
		                                - interval '1 minute'
		WHERE id = $1`, before.ID); err != nil {
		t.Fatalf("ageing the payment attempt: %v", err)
	}

	sweepHold(t, ctx, q, tx, made.Code)

	if s := state(t, ctx, q, made.Code).Status; s != booking.StatusExpired {
		t.Errorf("status %q after the grace period, want expired", s)
	}
}

// A booking nobody ever tried to pay for is expired on the old terms, with no
// grace period at all.
func TestSweeperStillExpiresBookingsThatNeverStartedPaying(t *testing.T) {
	ctx, q, tx := setup(t)
	made := held(t, ctx, tx)

	sweepHold(t, ctx, q, tx, made.Code)

	if s := state(t, ctx, q, made.Code).Status; s != booking.StatusExpired {
		t.Errorf("status %q, want expired", s)
	}
}

func findDue(rows []Due, code string) *Due {
	for i := range rows {
		if rows[i].Code == code {
			return &rows[i]
		}
	}
	return nil
}
