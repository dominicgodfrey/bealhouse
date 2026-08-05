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
	found, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, ErrBookingNotFound
	}
	if err != nil {
		return Opened{}, fmt.Errorf("payments: loading booking %q: %w", code, err)
	}

	// Only a stay still waiting for its first payment. A confirmed booking's
	// balance is taken off-session by the T-7 job and never through a page, and
	// a cancelled or expired one has already put its room back on sale — taking
	// money for it would create exactly the "paid for a room somebody else is
	// standing in" case the rest of this package exists to prevent.
	if found.Status != booking.StatusPending {
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

	intent, err := gw.CreateIntent(ctx, IntentRequest{
		BookingCode: found.Code,
		AmountCents: amount,
		GuestEmail:  found.GuestEmail,
		GuestName:   found.GuestName,
		Kind:        kind,
		SaveCard:    saveCard,
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
