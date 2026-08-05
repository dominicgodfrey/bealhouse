// Package gateway is everything that knows what Stripe is.
//
// The interface it satisfies lives in internal/payments, which never imports
// the SDK. That split is the reason the payment state machine — the redelivered
// webhook, the declined-then-retried card, the money that lands after the room
// was resold — is tested against real Postgres with no API key and no network,
// and why the account arriving later changes one constructor rather than a
// design.
//
// Three implementations, and only one of them ever moves money:
//
//   - Stripe, when a secret key and a webhook secret are both configured.
//   - Fake, for development before the account exists. Explicitly opted into,
//     and refuses to exist under any other conditions — see New.
//   - Disabled, the default: every operation fails and the endpoints that would
//     have taken money answer 503. Which is the truth. The inn can hold a room
//     today; it cannot charge for one.
package gateway

import (
	"context"
	"fmt"

	"bealhouse/internal/config"
	"bealhouse/internal/payments"
)

// Metadata keys on every object this system creates at the processor.
//
// The booking code is the one that matters: it is set server-side when a
// payment is opened and read back off the webhook, which is how a payment finds
// its stay without the browser ever being asked.
const (
	metadataBookingCode = "booking_code"
	metadataKind        = "kind"
)

// New picks a processor, and refuses rather than guessing.
//
// The fake mints ids and confirms whatever it is asked to, so the only real
// question here is how sure we are that it can never do that in production.
// Three conditions have to hold together, and the first two are the important
// ones because ENV defaults to "dev" — an unconfigured deploy would otherwise
// look exactly like a developer's laptop:
//
//  1. STRIPE_FAKE is set explicitly. Nothing defaults it on.
//  2. No Stripe variable is set at all. A half-configured deploy — a secret key
//     but no webhook secret, say — is a mistake worth stopping on, never a
//     licence to substitute a fake.
//  3. ENV is dev.
//
// Anything else with no keys is Disabled, which fails loudly and takes no money.
func New(cfg config.Config) (payments.Gateway, error) {
	if cfg.StripeConfigured() {
		return NewStripe(cfg.StripeSecretKey), nil
	}

	if !cfg.StripeFake {
		return Disabled{}, nil
	}

	if cfg.StripeSecretKey != "" || cfg.StripeWebhookSecret != "" {
		return nil, fmt.Errorf(
			"gateway: STRIPE_FAKE is set alongside real Stripe settings; " +
				"remove one — a half-configured processor must not be replaced by a fake")
	}
	if !cfg.IsDev() {
		return nil, fmt.Errorf("gateway: STRIPE_FAKE is set with ENV=%q; the fake is for development only", cfg.Env)
	}
	return NewFake(), nil
}

// Disabled is the processor when there is none.
//
// Every operation fails the same way, and the handlers that call them answer
// 503. Deliberately not a silent success: a booking flow that appears to take
// payment and does not is worse in every way than one that says it cannot.
type Disabled struct{}

func (Disabled) CreateIntent(context.Context, payments.IntentRequest) (payments.Intent, error) {
	return payments.Intent{}, payments.ErrGatewayDisabled
}

func (Disabled) ChargeOffSession(context.Context, payments.OffSessionRequest) (payments.Intent, error) {
	return payments.Intent{}, payments.ErrGatewayDisabled
}

func (Disabled) Refund(context.Context, payments.RefundRequest) (string, error) {
	return "", payments.ErrGatewayDisabled
}
