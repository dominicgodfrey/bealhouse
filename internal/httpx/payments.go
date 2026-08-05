package httpx

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/gateway"
	"bealhouse/internal/payments"
)

// paymentIntentResponse is what the pay page needs to mount the card form.
type paymentIntentResponse struct {
	// ClientSecret authorises the browser to complete this one payment. It is
	// no-store for the same reason a booking is: a shared machine's back button
	// must not hand the next person a live payment.
	ClientSecret string `json:"clientSecret"`

	// PublishableKey is Stripe's, and is meant to be public — it identifies the
	// account to the card form and can do nothing on its own. It rides along
	// here so the page needs one round trip rather than two, and so a build
	// never has to be told which account it is talking to.
	PublishableKey string `json:"publishableKey"`

	// AmountCents is what the card will actually be charged, echoed back so the
	// page can show the guest the figure rather than recomputing one.
	AmountCents int64 `json:"amountCents"`

	// DevPayment says this payment is against the fake processor, so the page
	// should offer the stand-in button instead of a card form.
	//
	// Said explicitly rather than inferred from an empty publishable key. A
	// deploy with real keys and a missing publishable one would look identical,
	// and "the server told me to" is the wrong reason to show a developer's
	// button to a paying guest.
	DevPayment bool `json:"devPayment"`
}

// createPaymentIntent serves POST /api/bookings/{code}/payment-intent.
//
// A POST rather than a GET because it creates something at the processor and
// writes to the booking, and because a browser is free to prefetch a GET.
//
// **It takes no body at all.** There is nothing a client could usefully say
// here: the amount comes from the booking's own snapshot inside payments.Open,
// and the booking comes from the path. An endpoint that accepted an amount
// would be a guest naming their own price, and the ledger's Underpaid check is
// the backstop under that rule rather than the rule itself.
func createPaymentIntent(q *db.Queries, gw payments.Gateway, publishableKey string) http.HandlerFunc {
	_, fake := gw.(*gateway.Fake)

	return func(w http.ResponseWriter, r *http.Request) {
		opened, err := payments.Open(r.Context(), q, gw, chi.URLParam(r, "code"))
		if err != nil {
			paymentProblem(w, r, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, paymentIntentResponse{
			ClientSecret:   opened.ClientSecret,
			PublishableKey: publishableKey,
			AmountCents:    opened.AmountCents,
			DevPayment:     fake,
		})
	}
}

// paymentProblem maps a refused payment onto a status code.
func paymentProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, payments.ErrBookingNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})

	// The truth, and worth saying plainly rather than as a 500: the inn can
	// hold a room today and cannot take money for one.
	case errors.Is(err, payments.ErrGatewayDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "payments are not available yet; the inn has not connected a card processor",
		})

	// 409 rather than 400: the request was well formed and would have worked
	// earlier. The hold ran out, or somebody has already paid.
	case errors.Is(err, payments.ErrNotPayable):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "this booking can no longer be paid for; the hold may have run out",
		})
	case errors.Is(err, payments.ErrNothingToPay):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "this booking has already been paid",
		})

	default:
		serverError(w, r, err)
	}
}
