package httpx

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"bealhouse/internal/gateway"
	"bealhouse/internal/payments"
)

// maxWebhookBody bounds what an unauthenticated caller can make this server
// read before the signature is checked. A PaymentIntent event is a few
// kilobytes; this is room for a very expanded one and nothing like room for a
// denial of service.
const maxWebhookBody = 256 << 10

// stripeWebhook serves POST /webhooks/stripe, which is what actually confirms a
// booking.
//
// Not the browser redirect (ARCHITECTURE.md, step 6 of the booking flow). A
// guest who pays and closes the tab has still paid, and their stay has to be
// confirmed whether or not they ever come back to the page.
//
// Registered on the **root** router rather than under /api, because that is
// where Stripe is configured to send it and because the SPA fallback answers
// GET and HEAD only — a POST to an unrouted path 404s, so an unregistered
// webhook is retried rather than recorded as delivered.
//
// The handler is deliberately thin: verify, translate, hand to the state
// machine, answer. Everything that decides anything lives in internal/payments
// and is tested against Postgres without any of this.
func stripeWebhook(beginner payments.Beginner, secret, ownerEmail string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read the raw body before anything else touches it. The signature is
		// computed over exactly these bytes, so a decode-and-re-encode anywhere
		// upstream makes every genuine delivery fail to verify.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			badRequest(w, "the webhook body could not be read")
			return
		}

		delivery, err := gateway.ParseWebhook(body, r.Header.Get(gateway.SignatureHeader), secret)
		if errors.Is(err, gateway.ErrBadSignature) {
			// 401, and nothing about why. Whoever sent this either is not
			// Stripe or is replaying something old, and neither deserves help
			// telling the two apart.
			slog.Warn("rejected an unverified webhook delivery",
				"remote", clientIP(r, false), "err", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature verification failed"})
			return
		}
		if err != nil {
			// Verified, but not something this server can act on — an event
			// naming no booking, say. Retrying will not improve it.
			slog.Error("could not read a verified webhook", "err", err)
			badRequest(w, "the webhook could not be understood")
			return
		}

		ctx := r.Context()

		switch delivery.Action {
		case gateway.Charged:
			// Attached here rather than read inside payments: the confirmation
			// and the owner's copy are queued in the transaction that records
			// the money, and where the inn's own address comes from is this
			// layer's business.
			delivery.Charge.OwnerEmail = ownerEmail

			result, err := payments.RecordCharge(ctx, beginner, delivery.Charge)
			if err != nil {
				// 500 on purpose. Stripe redelivers on any non-2xx, and a
				// payment this server failed to record is exactly what the
				// redelivery is for — RecordCharge is idempotent, so the retry
				// costs nothing and losing it costs a guest their room.
				webhookFailed(w, r, err)
				return
			}
			slog.Info("recorded a payment",
				"booking", result.Code, "outcome", result.Outcome, "event", delivery.Charge.EventID)

		case gateway.Failed:
			if _, err := payments.RecordFailure(ctx, beginner, delivery.Charge); err != nil {
				webhookFailed(w, r, err)
				return
			}
			slog.Info("recorded a declined payment", "booking", delivery.Charge.BookingCode)
		}

		// 200 for everything that got this far, including a redelivery of work
		// already done. Asking Stripe to try again would only produce this same
		// answer, more slowly.
		writeJSON(w, http.StatusOK, map[string]string{"status": string(delivery.Action)})
	}
}

// webhookFailed answers 500 so the delivery is retried, and says nothing.
func webhookFailed(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("failed to record a webhook; asking Stripe to retry",
		"err", err, "request_id", middleware.GetReqID(r.Context()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record this event"})
}
