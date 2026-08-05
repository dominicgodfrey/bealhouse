package payments

import (
	"context"
	"errors"
	"fmt"
)

// Gateway is the card processor, described in the only three operations this
// inn actually performs.
//
// The port, not the adapter: `internal/gateway` holds the Stripe implementation
// and a fake for development, and nothing in this package knows which one it
// has. That is what keeps the state machine testable against real Postgres with
// no API key, which is the property the whole package is built around.
//
// Kept deliberately narrow. Everything Stripe can do that the inn does not need
// is one more thing to get wrong the day somebody wires the real client in.
type Gateway interface {
	// CreateIntent opens a payment for the guest to complete in their browser.
	// The amount is the caller's to decide and must come from the booking, never
	// from a request body.
	CreateIntent(ctx context.Context, in IntentRequest) (Intent, error)

	// ChargeOffSession takes money from a card already on file, with nobody
	// present to approve it — decision #6's T-7 balance charge.
	//
	// A card that says no comes back as *Declined, which is an ordinary outcome
	// with an email attached rather than a failure to retry. Anything else is a
	// problem with the request or the network and should be retried.
	ChargeOffSession(ctx context.Context, in OffSessionRequest) (Intent, error)

	// Refund returns money against a payment that already succeeded, and gives
	// back the processor's id for it.
	Refund(ctx context.Context, in RefundRequest) (string, error)
}

// IntentRequest opens a payment.
type IntentRequest struct {
	// BookingCode travels to the processor as metadata and comes back on the
	// webhook, which is how a payment finds its stay. It is not read from the
	// browser at any point in that round trip.
	BookingCode string

	AmountCents int64

	// GuestEmail is for the processor's own receipt; the inn sends its own.
	GuestEmail string
	GuestName  string

	// Kind is KindDeposit or KindFull, carried as metadata so the webhook can
	// record which part of the money this was without inferring it from the
	// amount.
	Kind string

	// SaveCard keeps the card usable off-session afterwards.
	//
	// True only when a T-7 charge is coming. A short-notice stay pays in full at
	// booking (decision #7) and there is no second charge, so its card is not
	// stored — the less of a guest's payment detail this system is attached to,
	// the better.
	SaveCard bool
}

// Intent is a payment the processor has opened or completed.
type Intent struct {
	// ID is the PaymentIntent id, which becomes the ledger's idempotency anchor
	// once a webhook reports on it.
	ID string

	// ClientSecret is what the browser needs to complete the payment. It is a
	// credential for this one payment: it goes to the guest who is paying and
	// nowhere else, and must never be logged.
	ClientSecret string

	// CustomerID and PaymentMethodID are what a later off-session charge needs.
	// Set once a card has actually been attached, which for the browser flow is
	// on the webhook rather than here.
	CustomerID      string
	PaymentMethodID string
}

// OffSessionRequest charges a card with nobody at the keyboard.
type OffSessionRequest struct {
	BookingCode     string
	AmountCents     int64
	CustomerID      string
	PaymentMethodID string
}

// RefundRequest sends money back.
type RefundRequest struct {
	BookingCode string

	// IntentID is the payment being refunded.
	IntentID string

	// AmountCents comes from pricing.Refund, which derives it from what was
	// actually collected (decision #25). It is not the booking's total.
	AmountCents int64
}

// Declined is a card that said no.
//
// A distinct type because the two failures need opposite handling: a decline is
// an outcome the ledger records and the guest is emailed about, while a network
// error is a job that should run again. Treating a decline as retryable would
// mail the guest every hour; treating a timeout as a decline would tell a guest
// their card failed when it may well have been charged.
type Declined struct {
	// IntentID is the attempt that failed. Recorded in the ledger, because the
	// decline is what an owner needs when a charge is later disputed
	// (decision #28).
	IntentID string

	// Reason is the processor's own words, safe to show a guest.
	Reason string
}

func (d *Declined) Error() string {
	if d.Reason == "" {
		return fmt.Sprintf("payments: card declined on %s", d.IntentID)
	}
	return fmt.Sprintf("payments: card declined on %s: %s", d.IntentID, d.Reason)
}

// IsDeclined reports whether an error is a card saying no rather than something
// worth retrying.
func IsDeclined(err error) (*Declined, bool) {
	var d *Declined
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// ErrGatewayDisabled is what every operation returns when no processor is
// configured. The endpoints that move money answer 503 on it, which is the
// truth: the inn can hold a room today and cannot take money for it.
var ErrGatewayDisabled = errors.New("payments: no payment processor is configured")
