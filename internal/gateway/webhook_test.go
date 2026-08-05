package gateway

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"bealhouse/internal/payments"
)

// None of this needs a Stripe account. A webhook signature is an HMAC over the
// raw body with a shared secret, so a test can produce a genuine one with a
// secret of its own choosing — which means the code standing between an
// anonymous POST and a confirmed booking is fully exercised before the account
// exists.
const testSecret = "whsec_test_secret"

// event builds a delivery body in the shape Stripe sends.
func event(id, eventType string, intent map[string]any) []byte {
	body, err := json.Marshal(map[string]any{
		"id":          id,
		"object":      "event",
		"api_version": stripe.APIVersion,
		"created":     time.Now().Unix(),
		"type":        eventType,
		"data":        map[string]any{"object": intent},
	})
	if err != nil {
		panic(err)
	}
	return body
}

// succeeded is an ordinary deposit landing on a booking.
func succeeded(code string) map[string]any {
	return map[string]any{
		"id":              "pi_test_1",
		"object":          "payment_intent",
		"amount":          12500,
		"amount_received": 12500,
		"currency":        "usd",
		"customer":        "cus_test_1",
		"payment_method":  "pm_test_1",
		"metadata": map[string]string{
			metadataBookingCode: code,
			metadataKind:        payments.KindDeposit,
		},
	}
}

// sign produces the header Stripe would have sent with this body.
func sign(t *testing.T, body []byte, secret string, at time.Time) string {
	t.Helper()
	return webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   body,
		Secret:    secret,
		Timestamp: at,
	}).Header
}

func TestVerifiedSuccessBecomesACharge(t *testing.T) {
	body := event("evt_1", "payment_intent.succeeded", succeeded("BH-ABC123"))

	got, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got.Action != Charged {
		t.Fatalf("action %q, want %q", got.Action, Charged)
	}

	// The booking comes off the intent's metadata, which the server set when it
	// opened the payment. That is the whole reason a guest cannot attach their
	// payment to somebody else's stay.
	if got.Charge.BookingCode != "BH-ABC123" {
		t.Errorf("booking %q, want BH-ABC123", got.Charge.BookingCode)
	}
	if got.Charge.StripeID != "pi_test_1" {
		t.Errorf("stripe id %q", got.Charge.StripeID)
	}
	if got.Charge.Kind != payments.KindDeposit {
		t.Errorf("kind %q", got.Charge.Kind)
	}
	if got.Charge.AmountCents != 12500 {
		t.Errorf("amount %d, want 12500", got.Charge.AmountCents)
	}

	// The event id has to travel with the charge: RecordCharge writes it inside
	// its own transaction, which is what stops a failure mid-handler from
	// leaving the event marked handled and the payment unrecorded.
	if got.Charge.EventID != "evt_1" {
		t.Errorf("event id %q", got.Charge.EventID)
	}

	// The card, without which the T-7 charge has nothing to bill.
	if got.Charge.CustomerID != "cus_test_1" || got.Charge.PaymentMethodID != "pm_test_1" {
		t.Errorf("card came back as %q/%q", got.Charge.CustomerID, got.Charge.PaymentMethodID)
	}
}

// What arrived, not what was asked for. Recording the requested amount would
// confirm a stay on money that never landed — the exact hole the ledger's
// Underpaid check exists to catch, and this is the other side of it.
func TestAmountIsWhatWasReceived(t *testing.T) {
	intent := succeeded("BH-ABC123")
	intent["amount"] = 12500
	intent["amount_received"] = 9900
	body := event("evt_1", "payment_intent.succeeded", intent)

	got, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got.Charge.AmountCents != 9900 {
		t.Errorf("amount %d, want the 9900 actually received", got.Charge.AmountCents)
	}
}

// A failed attempt records what was tried, since nothing was received. The row
// is what an owner argues from when a charge is later disputed (decision #28).
func TestFailedPaymentRecordsWhatWasAttempted(t *testing.T) {
	intent := succeeded("BH-ABC123")
	intent["amount_received"] = 0
	body := event("evt_2", "payment_intent.payment_failed", intent)

	got, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got.Action != Failed {
		t.Fatalf("action %q, want %q", got.Action, Failed)
	}
	if got.Charge.AmountCents != 12500 {
		t.Errorf("amount %d, want the 12500 attempted", got.Charge.AmountCents)
	}
}

// The one thing standing between an anonymous POST and a confirmed booking.
func TestUnverifiedDeliveriesAreRefused(t *testing.T) {
	body := event("evt_1", "payment_intent.succeeded", succeeded("BH-ABC123"))
	good := sign(t, body, testSecret, time.Now())

	for _, c := range []struct {
		name      string
		body      []byte
		signature string
		secret    string
	}{
		{"no signature at all", body, "", testSecret},
		{"signed with another secret", body, sign(t, body, "whsec_someone_else", time.Now()), testSecret},
		{"garbage in the header", body, "t=1,v1=deadbeef", testSecret},

		// The signature covers the bytes, so any edit invalidates it. This is
		// what stops somebody intercepting a $1 payment and turning it into a
		// confirmation of a $2,000 stay.
		{"body altered after signing", []byte(strings.Replace(string(body), "12500", "99900", 1)), good, testSecret},

		// Replay: a delivery captured off the wire and sent again next week.
		{"signed too long ago", body, sign(t, body, testSecret, time.Now().Add(-2*time.Hour)), testSecret},

		// No secret verifies nothing. Accepting on that basis would let anyone
		// who found the URL confirm their own booking.
		{"no secret configured", body, good, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseWebhook(c.body, c.signature, c.secret)
			if !errors.Is(err, ErrBadSignature) {
				t.Errorf("accepted a delivery that should not verify (err = %v)", err)
			}
		})
	}
}

// Genuine Stripe traffic that happens not to concern the inn. Answered rather
// than retried: there is nothing to do and nothing that will change.
func TestUnrelatedEventsAreIgnored(t *testing.T) {
	for _, kind := range []string{
		"charge.succeeded",
		"customer.created",
		"payment_intent.created",
		"payout.paid",
	} {
		body := event("evt_x", kind, succeeded("BH-ABC123"))
		got, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got.Action != Ignored {
			t.Errorf("%s came back as %q, want ignored", kind, got.Action)
		}
	}
}

// A payment with no booking on it cannot be applied to anything. Refused rather
// than guessed at — the guess would be somebody else's stay.
func TestEventWithoutABookingIsRefused(t *testing.T) {
	intent := succeeded("")
	delete(intent, "metadata")
	body := event("evt_1", "payment_intent.succeeded", intent)

	if _, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret); err == nil {
		t.Error("accepted a payment that names no booking")
	} else if errors.Is(err, ErrBadSignature) {
		t.Error("a well-signed but unusable event was reported as a signature failure")
	}
}

// The ledger's kind is what an owner reconciles against, so guessing at it is
// worse than refusing. A deposit recorded as a balance charge is a stay that
// looks paid in full.
func TestEventWithoutAKindIsRefused(t *testing.T) {
	intent := succeeded("BH-ABC123")
	intent["metadata"] = map[string]string{metadataBookingCode: "BH-ABC123"}
	body := event("evt_1", "payment_intent.succeeded", intent)

	if _, err := ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret); err == nil {
		t.Error("accepted a payment that does not say which part of the money it is")
	}
}

// A v2 event notification carries no object to act on. It verifies, so it must
// not be mistaken for a bad signature, and it cannot be acted on either.
func TestThinEventNotificationIsRefusedButNotAsAForgery(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"id":      "evt_thin",
		"object":  "v2.core.event",
		"type":    "v1.billing.meter.error_report_triggered",
		"created": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ParseWebhook(body, sign(t, body, testSecret, time.Now()), testSecret)
	if err == nil {
		t.Fatal("accepted an event notification with no object on it")
	}
	if errors.Is(err, ErrBadSignature) {
		t.Error("a correctly signed notification was reported as a signature failure")
	}
}

// An endpoint left on a different API version in the dashboard must not fail
// every delivery. It is logged and worked with: the five fields this reads off a
// PaymentIntent have been stable for years, and a launch day where no payment is
// ever recorded is the worse outcome.
func TestOlderAPIVersionStillRecordsThePayment(t *testing.T) {
	var body map[string]any
	if err := json.Unmarshal(event("evt_1", "payment_intent.succeeded", succeeded("BH-ABC123")), &body); err != nil {
		t.Fatal(err)
	}
	body["api_version"] = "2019-02-19"

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ParseWebhook(encoded, sign(t, encoded, testSecret, time.Now()), testSecret)
	if err != nil {
		t.Fatalf("an event on an older API version was refused: %v", err)
	}
	if got.Action != Charged {
		t.Errorf("action %q, want %q", got.Action, Charged)
	}
}
