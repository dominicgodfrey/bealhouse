package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"bealhouse/internal/payments"
)

// SignatureHeader is where Stripe puts the signature it wants verified.
const SignatureHeader = "Stripe-Signature"

// ErrBadSignature means the delivery did not come from Stripe, or was altered
// on the way, or is old enough to be a replay.
//
// The webhook promotes a hold into a confirmed stay, so an unverified request
// reaching that code is somebody confirming their own booking for free. This is
// the only thing standing between the two.
var ErrBadSignature = errors.New("gateway: the webhook signature did not verify")

// Action is what a delivery asks the server to do.
type Action string

const (
	// Charged is money that arrived: record it and let the state machine work
	// out what it means for the room.
	Charged Action = "charged"

	// Failed is a card that said no.
	Failed Action = "failed"

	// Ignored is an event type this system does not act on. Answered 200 all
	// the same — the delivery was genuine and there is nothing to retry.
	Ignored Action = "ignored"
)

// Delivery is a verified webhook, in the vocabulary of internal/payments.
type Delivery struct {
	Action Action

	// Charge is filled for Charged and Failed. Its EventID is what makes the
	// event and the payment one fact, committed together inside RecordCharge's
	// own transaction.
	Charge payments.Charge
}

// The event types this system acts on. Everything else is genuine traffic that
// happens not to concern the inn, and is answered 200 rather than retried.
const (
	eventPaymentSucceeded = "payment_intent.succeeded"
	eventPaymentFailed    = "payment_intent.payment_failed"
)

// ParseWebhook verifies a delivery and translates it.
//
// **The payload must be the raw request body, byte for byte.** The signature is
// computed over exactly what was sent, so anything that re-encodes the JSON
// first — decoding into a map and back, a middleware that pretty-prints —
// produces a body that will not verify. Read it before anything else touches it.
//
// Verification includes Stripe's timestamp tolerance, which is what stops a
// delivery captured off the wire being replayed a week later.
func ParseWebhook(payload []byte, signature, secret string) (Delivery, error) {
	if secret == "" {
		// Refusing is the only safe answer. An empty secret verifies nothing,
		// and a handler that accepted deliveries on that basis would confirm
		// stays for anyone who found the URL.
		return Delivery{}, fmt.Errorf("%w: no webhook secret is configured", ErrBadSignature)
	}

	// Verified and decoded in two steps, rather than with ConstructEvent, so
	// that "this did not come from Stripe" stays distinguishable from "this came
	// from Stripe and says something unexpected". ConstructEvent folds both into
	// one error, and the first is a 401 while the second is not — debugging a
	// webhook that reports an auth failure because of a dashboard setting is a
	// bad afternoon nobody should have to have.
	if err := webhook.ValidatePayload(payload, signature, secret); err != nil {
		return Delivery{}, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	var event stripe.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return Delivery{}, fmt.Errorf("gateway: reading the webhook body: %w", err)
	}
	if event.Object != "event" {
		// A v2 "thin" event notification, or something else entirely. It
		// carries no object to act on, and guessing is worse than refusing.
		return Delivery{}, fmt.Errorf("gateway: %q is not a webhook event this system understands", event.Object)
	}

	// A mismatch is logged and worked with rather than refused.
	//
	// ConstructEvent would reject it outright, which means an endpoint left on
	// a different API version in the dashboard fails every delivery until
	// somebody notices — a launch day where no payment is ever recorded.
	// Against that: this reads five fields off a PaymentIntent — the id, two
	// metadata keys, amount_received, and the customer and payment method ids —
	// and those have been stable across API versions for years. If that ever
	// stops being true, this is where to reconsider.
	if event.APIVersion != "" && event.APIVersion != stripe.APIVersion {
		slog.Warn("webhook event was sent on a different Stripe API version",
			"event_version", event.APIVersion, "sdk_expects", stripe.APIVersion, "event", event.ID)
	}

	switch string(event.Type) {
	case eventPaymentSucceeded, eventPaymentFailed:
	default:
		return Delivery{Action: Ignored}, nil
	}

	var intent stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &intent); err != nil {
		return Delivery{}, fmt.Errorf("gateway: reading the payment in event %s: %w", event.ID, err)
	}

	charge := payments.Charge{
		// From the intent's metadata, which the server set when it opened the
		// payment. Never from anything a browser said.
		BookingCode: intent.Metadata[metadataBookingCode],
		Kind:        intent.Metadata[metadataKind],

		StripeID:  intent.ID,
		EventID:   event.ID,
		EventType: string(event.Type),
	}
	if charge.BookingCode == "" {
		return Delivery{}, fmt.Errorf("gateway: event %s names no booking", event.ID)
	}
	if charge.Kind == "" {
		// Older intents, or one created by hand in the dashboard. A deposit is
		// the wrong guess often enough to be worth refusing over: the ledger's
		// kind is what an owner reconciles against.
		return Delivery{}, fmt.Errorf("gateway: event %s does not say which part of the money it is", event.ID)
	}

	if string(event.Type) == eventPaymentFailed {
		// What was attempted, since nothing was received. The row exists so a
		// disputed charge can be argued from the whole history (decision #28).
		charge.AmountCents = intent.Amount
		return Delivery{Action: Failed, Charge: charge}, nil
	}

	// What actually arrived, not what was asked for. If those differ the ledger
	// records the smaller number and RecordCharge declines to confirm the stay.
	charge.AmountCents = intent.AmountReceived

	// The card, for the T-7 charge. Present only when the payment was opened
	// with SaveCard, which is exactly the bookings that have a balance to come.
	if intent.Customer != nil {
		charge.CustomerID = intent.Customer.ID
	}
	if intent.PaymentMethod != nil {
		charge.PaymentMethodID = intent.PaymentMethod.ID
	}

	return Delivery{Action: Charged, Charge: charge}, nil
}
