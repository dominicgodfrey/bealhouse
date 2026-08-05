package httpx

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/gateway"
	"bealhouse/internal/payments"
	"bealhouse/internal/pricing"
)

// devPay serves POST /api/dev/pay/{code}: it pretends the guest completed the
// card form.
//
// **Registered only when the processor is the fake**, which itself only exists
// with STRIPE_FAKE set, no Stripe variable configured at all, and ENV=dev. There
// is no combination of settings that puts this route on a server that can take
// real money.
//
// It exists because the interesting half of the payment flow is what happens
// after the card form, and none of that could be walked through in a browser
// before the Stripe account arrives. So rather than shortcut to a confirmed
// booking, it builds a properly signed delivery and sends it through the real
// webhook handler — signature verification, the state machine, the mail queue,
// all of it. The only thing pretended is that a card was charged.
func devPay(fake *gateway.Fake, beginner payments.Beginner, secret, ownerEmail string) http.HandlerFunc {
	hook := stripeWebhook(beginner, secret, ownerEmail)

	return func(w http.ResponseWriter, r *http.Request) {
		tx, err := beginner.Begin(r.Context())
		if err != nil {
			serverError(w, r, err)
			return
		}
		defer func() { _ = tx.Rollback(r.Context()) }()

		found, err := db.New(tx).GetBookingForPayment(r.Context(), chi.URLParam(r, "code"))
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}
		_ = tx.Rollback(r.Context())

		// The same derivation payments.Open does, for the same reason: the
		// amount is the booking's, never the caller's.
		quote := pricing.Quote{
			TotalCents:   found.TotalCents,
			DepositCents: found.DepositCents,
			BalanceCents: found.BalanceDueCents,
		}
		amount := quote.ChargeAtBooking(!found.BalanceChargeAt.Valid) - found.AmountPaidCents
		if amount <= 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "nothing is outstanding on this booking"})
			return
		}

		payment := gateway.FakePayment{
			BookingCode: found.Code,
			IntentID:    found.PaymentIntentID,
			AmountCents: amount,
			Kind:        payments.KindFull,
		}
		if found.PaymentIntentID == "" {
			// The pay page opens a payment before this is ever reached, so this
			// is somebody calling the endpoint directly. Give them an id rather
			// than a confusing failure.
			payment.IntentID = "pi_fake_direct_" + found.Code
		}

		// A stay with a balance to come needs a card on file, or the T-7 job has
		// nothing to charge and the second half of decision #6 cannot be walked
		// through at all.
		if found.BalanceChargeAt.Valid {
			payment.Kind = payments.KindDeposit
			payment.CustomerID = "cus_fake_" + found.Code
			payment.PaymentMethodID = "pm_fake_" + found.Code
		}

		body, signature := fake.SucceededEvent(payment)
		slog.Warn("FAKE payment: delivering a synthetic webhook",
			"booking", found.Code, "amount_cents", amount)

		// Through the real handler, headers and all. Anything less would be
		// testing a path that does not exist in production.
		delivery := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
		delivery.Header.Set(gateway.SignatureHeader, signature)
		hook(w, delivery.WithContext(r.Context()))
	}
}
