package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"bealhouse/internal/payments"
)

// FakeWebhookSecret is what the fake signs its events with.
//
// A fixed constant rather than configuration, because setting a real
// STRIPE_WEBHOOK_SECRET is one of the things that stops the fake existing at
// all (see New). It is not a secret in any meaningful sense — it is in this
// file — which is exactly why nothing that has a real one will use it.
const FakeWebhookSecret = "whsec_bealhouse_fake_not_a_real_secret"

// Fake is a processor that mints plausible ids and takes no money.
//
// It exists so the whole journey — hold, pay, webhook, confirmed stay,
// confirmation email — can be walked through in a browser and driven from tests
// before the Stripe account exists. Everything downstream of it is real: the
// webhook is signature-verified with FakeWebhookSecret, the ledger writes are
// the production ones, and the state machine cannot tell the difference.
//
// The ids it produces are prefixed `_fake` on purpose. If one ever turns up in
// the payments table of a real database, that is a fact somebody needs to be
// able to see at a glance rather than infer.
type Fake struct {
	mu sync.Mutex

	// declineOffSession makes every T-7 charge fail, so decision #6's failure
	// path — the flag on the booking, the email to the guest — can be walked
	// without waiting for a real card to be declined.
	declineOffSession bool

	// charged records what was asked for, so tests can assert on it without
	// reaching for a network.
	charged []payments.OffSessionRequest
}

func NewFake() *Fake {
	slog.Warn("using the FAKE payment processor: no money will move, and any booking it confirms was not paid for")
	return &Fake{}
}

// DeclineOffSession makes every later off-session charge decline.
func (f *Fake) DeclineOffSession(decline bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declineOffSession = decline
}

// Charged is every off-session charge this fake was asked for.
func (f *Fake) Charged() []payments.OffSessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]payments.OffSessionRequest(nil), f.charged...)
}

func (f *Fake) CreateIntent(_ context.Context, in payments.IntentRequest) (payments.Intent, error) {
	id := "pi_fake_" + token()

	out := payments.Intent{
		ID: id,
		// Shaped like the real one because the front end parses it: Stripe.js
		// splits a client secret on `_secret_` to find the intent it belongs to.
		ClientSecret: id + "_secret_fake",
	}
	if in.SaveCard {
		out.CustomerID = "cus_fake_" + token()
		out.PaymentMethodID = "pm_fake_" + token()
	}
	return out, nil
}

func (f *Fake) ChargeOffSession(_ context.Context, in payments.OffSessionRequest) (payments.Intent, error) {
	f.mu.Lock()
	f.charged = append(f.charged, in)
	decline := f.declineOffSession
	f.mu.Unlock()

	id := "pi_fake_" + token()
	if decline {
		return payments.Intent{}, &payments.Declined{
			IntentID: id,
			Reason:   "the fake processor is set to decline",
		}
	}
	return payments.Intent{
		ID:              id,
		CustomerID:      in.CustomerID,
		PaymentMethodID: in.PaymentMethodID,
	}, nil
}

func (f *Fake) Refund(_ context.Context, _ payments.RefundRequest) (string, error) {
	return "re_fake_" + token(), nil
}

// FakePayment describes a payment the fake is pretending succeeded.
type FakePayment struct {
	BookingCode string
	IntentID    string
	AmountCents int64
	Kind        string

	// CustomerID and PaymentMethodID stand in for the card Stripe would have
	// saved. Set when a T-7 charge is coming, so the whole of decision #6 can be
	// walked through locally rather than only its first half.
	CustomerID      string
	PaymentMethodID string
}

// SucceededEvent builds a signed `payment_intent.succeeded` delivery.
//
// It exists so a developer with no Stripe account can walk the entire journey —
// hold, pay, webhook, confirmed stay, confirmation email — through the real
// webhook handler, signature verification and all. Nothing downstream of the
// delivery is faked; only the delivery itself.
//
// A method on Fake rather than a package function, so it is unreachable without
// one. The fake refuses to exist outside dev with no keys, which makes this
// unreachable there too.
func (f *Fake) SucceededEvent(in FakePayment) (body []byte, signature string) {
	intent := map[string]any{
		"id":              in.IntentID,
		"object":          "payment_intent",
		"amount":          in.AmountCents,
		"amount_received": in.AmountCents,
		"currency":        currency,
		"status":          "succeeded",
		"metadata": map[string]string{
			metadataBookingCode: in.BookingCode,
			metadataKind:        in.Kind,
		},
	}
	if in.CustomerID != "" {
		intent["customer"] = in.CustomerID
	}
	if in.PaymentMethodID != "" {
		intent["payment_method"] = in.PaymentMethodID
	}

	body, err := json.Marshal(map[string]any{
		"id":          "evt_fake_" + token(),
		"object":      "event",
		"api_version": stripe.APIVersion,
		"created":     time.Now().Unix(),
		"type":        eventPaymentSucceeded,
		"data":        map[string]any{"object": intent},
	})
	if err != nil {
		// Every value here is a string, an int64 or a map of them.
		panic("gateway: encoding a fake event: " + err.Error())
	}

	return body, webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: body,
		Secret:  FakeWebhookSecret,
	}).Header
}

// token is the random tail of a fake id. Long enough that two payments in the
// same second cannot collide on the unique index the ledger keys on.
func token() string {
	var b [12]byte
	// rand.Read is documented never to fail.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
