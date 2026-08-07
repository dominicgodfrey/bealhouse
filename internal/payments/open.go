package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/pricing"
)

var (
	// ErrNotPayable means the booking is in no state to take money: already
	// paid for, cancelled, or expired with the room back on sale.
	ErrNotPayable = errors.New("payments: this booking cannot be paid for")

	// ErrNothingToPay means the amount due at booking has already arrived.
	ErrNothingToPay = errors.New("payments: nothing is outstanding on this booking")
)

// Opened is a payment waiting for the guest to complete it.
type Opened struct {
	Code string

	// IntentID is the processor's id, recorded on the booking so the sweeper
	// knows somebody is mid-payment.
	IntentID string

	// ClientSecret is a credential for this one payment. It goes to the browser
	// that is paying and nowhere else — never a log line, never an error
	// message.
	ClientSecret string

	// AmountCents is what will be charged, derived here and returned so the page
	// can show the guest the figure the card will actually see.
	AmountCents int64
}

// Open starts a payment for a booking.
//
// **The amount is derived here, from the booking's own row.** That is the whole
// point of the function: the alternative — a browser naming what it intends to
// pay — is a guest choosing their own price. `RecordCharge` refuses to confirm a
// stay that came up short, but that is a backstop under this, not a substitute
// for it.
//
// Which figure depends on decision #7's flag: a NULL balance_charge_at means the
// arrival was inside the T-8 window and the whole total was due up front, not
// half of it. Read exactly the way booking.Get and shortfall read it, so the
// page, the charge and the check cannot drift apart.
//
// The processor is called outside any transaction, deliberately. It is a network
// round trip to somebody else's server, and holding a Postgres transaction open
// across one is how a slow afternoon at Stripe becomes a pile of locks here. Two
// concurrent calls are harmless: the gateway keys the request on the booking, so
// both get the same payment rather than two authorisations against one card.
func Open(ctx context.Context, q *db.Queries, gw Gateway, code string) (Opened, error) {
	return open(ctx, q, gw, code, false)
}

// OpenKeyedIn is Open for a card somebody at the inn is typing in from what a
// guest is reading out over the telephone.
//
// Everything about it is the same except the flag that reaches the processor:
// the amount still comes from the booking's own row, the state rules are still
// the ones above, and the card still goes from the browser to Stripe without
// touching this server. What the flag buys is a payment the bank will not send a
// 3-D Secure challenge to — there is nobody at the guest's end to answer one.
//
// A separate function rather than a boolean argument, because `Open(…, true)`
// at a call site says nothing about what the true means, and this is a call
// whose meaning is the whole difference.
func OpenKeyedIn(ctx context.Context, q *db.Queries, gw Gateway, code string) (Opened, error) {
	return open(ctx, q, gw, code, true)
}

func open(ctx context.Context, q *db.Queries, gw Gateway, code string, moto bool) (Opened, error) {
	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, ErrBookingNotFound
	}
	if err != nil {
		return Opened{}, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	// Two states can take money, and a cancelled or expired booking is neither:
	// those have already put their room back on sale, and paying for one would
	// create exactly the "paid for a room somebody else is standing in" case the
	// rest of this package exists to prevent.
	//
	//   - **Pending.** A guest partway through checkout, which is the ordinary
	//     case and the one the hold is protecting.
	//   - **Confirmed with nothing scheduled to collect it.** That is a booking
	//     the owner took on the phone: real, unpaid, and with no saved card, so
	//     the only way money reaches it is somebody being asked for it.
	//
	// The second is narrow on purpose. A confirmed booking that *does* have a
	// balance_charge_at is a website booking whose deposit landed and whose
	// balance the T-7 job will take off-session; letting a page collect that
	// early would leave the job to charge a card for money already paid.
	// ErrNothingToPay below catches the short-notice stay that is confirmed,
	// unscheduled and already settled in full.
	payable := found.Status == booking.StatusPending ||
		(found.Status == booking.StatusConfirmed && !found.BalanceChargeAt.Valid)
	if !payable {
		return Opened{}, ErrNotPayable
	}

	quote := pricing.Quote{
		TotalCents:   found.TotalCents,
		DepositCents: found.DepositCents,
		BalanceCents: found.BalanceDueCents,
	}
	amount := quote.ChargeAtBooking(!found.BalanceChargeAt.Valid) - found.AmountPaidCents
	if amount <= 0 {
		return Opened{}, ErrNothingToPay
	}

	// The card is kept only when there is a second charge coming. A short-notice
	// stay pays in full at booking (decision #7), so nothing is ever taken from
	// it again and there is no reason to be holding on to a way to.
	saveCard := found.BalanceChargeAt.Valid

	kind := KindDeposit
	if !saveCard {
		kind = KindFull
	}

	// Money arriving against a stay that is already confirmed is its balance,
	// whatever opened it. The label decides which message the guest gets when it
	// lands: RecordCharge sends the confirmation only to a stay this payment
	// confirmed, and a receipt to one that was already booked. Without this a
	// guest paying an emailed link for a phone booking would be charged and told
	// nothing at all.
	if found.Status == booking.StatusConfirmed {
		kind = KindBalance
	}

	intent, err := gw.CreateIntent(ctx, IntentRequest{
		BookingCode: found.Code,
		AmountCents: amount,
		GuestEmail:  found.GuestEmail,
		GuestName:   found.GuestName,
		Kind:        kind,
		SaveCard:    saveCard,
		MOTO:        moto,
	})
	if err != nil {
		return Opened{}, err
	}

	// Recorded after the fact rather than before, because until the processor
	// answers there is no id to record. The cost is a payment that exists at
	// Stripe and not here if this write fails; the webhook still carries the
	// booking code, so the money still finds its stay.
	if err := StartPayment(ctx, q, found.ID, intent.ID); err != nil {
		return Opened{}, err
	}

	return Opened{
		Code:         found.Code,
		IntentID:     intent.ID,
		ClientSecret: intent.ClientSecret,
		AmountCents:  amount,
	}, nil
}
