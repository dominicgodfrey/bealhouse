package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/payments"
	"bealhouse/internal/testdb"
)

// The webhook is what actually confirms a booking, and it answers to an
// anonymous POST. These tests drive it exactly as Stripe would — a raw body and
// a real signature over it — which needs no Stripe account, because a signature
// is an HMAC with a shared secret and a test can hold both ends of it.
//
// The one test here that books a room does so at today+400, a stretch of
// calendar no other package writes to. `go test ./...` runs packages in
// parallel against one database, and a booking in somebody else's window is how
// a suite starts lying.
const (
	hookSecret = "whsec_httpx_test"
	stayStart  = 400
	stayEnd    = 402
)

// stubbornBeginner fails to start a transaction, standing in for a database
// that has gone away mid-delivery.
type stubbornBeginner struct{}

func (stubbornBeginner) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("the database is unreachable")
}

// forbiddenBeginner fails the test if the state machine is reached at all.
type forbiddenBeginner struct{ t *testing.T }

func (f forbiddenBeginner) Begin(context.Context) (pgx.Tx, error) {
	f.t.Error("an unverified delivery reached the payment state machine")
	return nil, errors.New("must not be called")
}

func signedEvent(t *testing.T, id, kind string, intent map[string]any) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"id":          id,
		"object":      "event",
		"api_version": stripe.APIVersion,
		"created":     time.Now().Unix(),
		"type":        kind,
		"data":        map[string]any{"object": intent},
	})
	if err != nil {
		t.Fatalf("building the event: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: body,
		Secret:  hookSecret,
	}).Header)
	return req
}

// The whole path, end to end: an anonymous POST with a valid signature turns a
// held room into a confirmed stay and money in the ledger. The browser redirect
// does none of this, which is the point — a guest who pays and closes the tab
// has still paid.
func TestSignedDeliveryConfirmsAHeldBooking(t *testing.T) {
	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	ctx := context.Background()
	q := db.New(tx)

	made, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug: "rose-chamber",
		Checkin:  civil.AddDays(civil.Today(), stayStart),
		Checkout: civil.AddDays(civil.Today(), stayEnd),
		Guests:   2,
		Guest:    booking.Guest{Name: "Ada Lovelace", Email: "ada@example.com"},
	})
	if err != nil {
		t.Fatalf("holding a room: %v", err)
	}

	req := signedEvent(t, "evt_confirm", "payment_intent.succeeded", map[string]any{
		"id":              "pi_confirm",
		"object":          "payment_intent",
		"amount":          made.Quote.DepositCents,
		"amount_received": made.Quote.DepositCents,
		"currency":        "usd",
		"customer":        "cus_confirm",
		"payment_method":  "pm_confirm",
		"metadata": map[string]string{
			"booking_code": made.Code,
			"kind":         payments.KindDeposit,
		},
	})

	rec := httptest.NewRecorder()
	stripeWebhook(tx, hookSecret, letterhead{})(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200 — Stripe would retry a payment already recorded", rec.Code)
	}

	after, err := q.GetBookingForPayment(ctx, made.Code)
	if err != nil {
		t.Fatalf("reading the booking back: %v", err)
	}
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q, want confirmed", after.Status)
	}
	if after.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("collected %d, want the %d deposit", after.AmountPaidCents, made.Quote.DepositCents)
	}

	// A redelivery must change nothing and still answer 200. Stripe delivers at
	// least once and retries on any non-2xx, so both halves matter.
	repeat := httptest.NewRecorder()
	stripeWebhook(tx, hookSecret, letterhead{})(repeat, signedEvent(t, "evt_confirm", "payment_intent.succeeded", map[string]any{
		"id":              "pi_confirm",
		"object":          "payment_intent",
		"amount":          made.Quote.DepositCents,
		"amount_received": made.Quote.DepositCents,
		"currency":        "usd",
		"metadata": map[string]string{
			"booking_code": made.Code,
			"kind":         payments.KindDeposit,
		},
	}))
	if repeat.Code != http.StatusOK {
		t.Errorf("a redelivery answered %d, want 200", repeat.Code)
	}

	again, err := q.GetBookingForPayment(ctx, made.Code)
	if err != nil {
		t.Fatalf("reading the booking back: %v", err)
	}
	if again.AmountPaidCents != made.Quote.DepositCents {
		t.Errorf("a redelivery collected the deposit twice: %d", again.AmountPaidCents)
	}
}

// An unverified delivery must not reach the state machine at all — not be
// rejected somewhere further in, where a later edit could let it through.
func TestUnverifiedDeliveryIsRejectedBeforeAnythingHappens(t *testing.T) {
	req := signedEvent(t, "evt_1", "payment_intent.succeeded", map[string]any{
		"id":              "pi_1",
		"object":          "payment_intent",
		"amount_received": 12500,
		"metadata":        map[string]string{"booking_code": "BH-ABC123", "kind": payments.KindDeposit},
	})
	req.Header.Set("Stripe-Signature", "t=1,v1=deadbeef")

	rec := httptest.NewRecorder()
	stripeWebhook(forbiddenBeginner{t}, hookSecret, letterhead{})(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("answered %d, want 401", rec.Code)
	}
}

// Genuine traffic the inn does not act on. 200, because there is nothing to do
// and nothing a retry would change.
func TestIgnoredEventIsAnswered200(t *testing.T) {
	req := signedEvent(t, "evt_1", "customer.created", map[string]any{"id": "cus_1", "object": "customer"})

	rec := httptest.NewRecorder()
	stripeWebhook(forbiddenBeginner{t}, hookSecret, letterhead{})(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("answered %d, want 200", rec.Code)
	}
}

// The half of the contract that keeps a payment from being lost: if this server
// could not record the money, it must not tell Stripe the event is done.
func TestAFailureToRecordAsksStripeToRetry(t *testing.T) {
	req := signedEvent(t, "evt_1", "payment_intent.succeeded", map[string]any{
		"id":              "pi_1",
		"object":          "payment_intent",
		"amount":          12500,
		"amount_received": 12500,
		"metadata":        map[string]string{"booking_code": "BH-ABC123", "kind": payments.KindDeposit},
	})

	rec := httptest.NewRecorder()
	stripeWebhook(stubbornBeginner{}, hookSecret, letterhead{})(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("answered %d, want 500 so the delivery is retried", rec.Code)
	}
}

// Without a secret there is nothing to verify against, so the route is not
// registered and the request falls through to the 404 that makes Stripe retry.
// Answering 200 here would have it record every event as delivered.
func TestWebhookRouteIsAbsentWithoutASecret(t *testing.T) {
	rec := get(t, router(t, false), http.MethodPost, "/webhooks/stripe", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("answered %d, want 404", rec.Code)
	}
}
