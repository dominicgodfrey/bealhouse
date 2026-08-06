// Package httpx wires the HTTP surface: the JSON API under /api and the
// embedded SPA on everything else.
package httpx

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/gateway"
	"bealhouse/internal/payments"
)

// Deps are the collaborators the HTTP layer needs. Pool may be nil when the
// server is started without a DATABASE_URL.
type Deps struct {
	Pool *pgxpool.Pool
	SPA  fs.FS

	// Gateway is the card processor. Never nil: with no keys configured it is
	// gateway.Disabled, whose every operation fails and whose endpoints answer
	// 503. A nil check here would be a fourth way to express "payments are off"
	// and one more place to get it wrong.
	Gateway payments.Gateway

	// StripePublishableKey identifies the account to the card form in the
	// browser. Public by design — it can do nothing on its own — and served to
	// the page with the payment it belongs to.
	StripePublishableKey string

	// StripeWebhookSecret verifies that a delivery really came from Stripe.
	// Empty leaves the webhook route unregistered rather than unverified.
	StripeWebhookSecret string

	// OwnerEmail gets the inn's own copy of a confirmed booking. Empty sends
	// none.
	OwnerEmail string

	// Links signs the manage-booking capability that reaches a guest in their
	// confirmation (decision #19). Nil when no secret is configured, which
	// leaves the routes behind it registered and refusing rather than absent:
	// a guest clicking an old link is owed "this link is not valid", not a page
	// of JavaScript that never loads.
	Links *booking.Links

	// SiteURL is the public origin the manage link is built on. Emails cannot
	// carry a relative path.
	SiteURL string

	// BehindProxy says a trusted reverse proxy terminates TLS in front of this
	// server, which is the deployed shape (Caddy, decision #2). It decides
	// whether X-Forwarded-For and X-Forwarded-Proto are believed at all: on a
	// directly exposed port both are attacker-supplied, and believing them
	// would hand anyone a way around the rate limits below.
	BehindProxy bool
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders(d.BehindProxy))

	// A slow or stuck handler must not hold a connection forever. Shorter than
	// the server's WriteTimeout so the handler is the thing that gives up, and
	// the client gets an answer rather than a dropped socket.
	r.Use(middleware.Timeout(20 * time.Second))

	// What this layer contributes to the mail payments queues, gathered once so
	// the webhook and its dev stand-in cannot be given different answers.
	letters := letterhead{ownerEmail: d.OwnerEmail, links: d.Links, siteURL: d.SiteURL}

	reads := newLimiter(apiRate, apiBurst)
	bookings := newLimiter(bookingRate, bookingBurst)
	paying := newLimiter(paymentRate, paymentBurst)

	r.Route("/api", func(api chi.Router) {
		api.Use(rateLimit(reads, d.BehindProxy))

		api.Get("/health", health(d.Pool))

		// Creating a booking is the one anonymous endpoint that takes inventory
		// off sale, so it carries a much tighter limit on top of the shared
		// one: the cost of abusing it is the owner's revenue rather than some
		// CPU. Bound to the route rather than to either branch below, so the
		// limit is a property of the endpoint and cannot be left off by
		// whichever arm someone edits next.
		makeBooking := databaseRequired
		if d.Pool != nil {
			// Booking needs to start its own transaction rather than borrow a
			// connection, because the booking and the hold that reserves its
			// room have to commit together or not at all.
			makeBooking = createBooking(d.Pool)
		}
		api.With(rateLimit(bookings, d.BehindProxy)).Post("/bookings", makeBooking)

		// Opening a payment calls out to the processor on the inn's account, so
		// it gets its own allowance rather than sharing the readers' loose one.
		// It takes no inventory off sale, so it does not need booking's.
		openPayment := databaseRequired
		if d.Pool != nil {
			openPayment = createPaymentIntent(db.New(d.Pool), d.Gateway, d.StripePublishableKey)
		}
		api.With(rateLimit(paying, d.BehindProxy)).
			Post("/bookings/{code}/payment-intent", openPayment)

		// Standing in for the card form, and only ever that. The route exists
		// exactly when the processor is the fake — which needs STRIPE_FAKE set,
		// no Stripe variable configured, and ENV=dev — so it is registered by
		// the same decision that decided money cannot move, rather than by a
		// separate flag that could disagree with it.
		if fake, ok := d.Gateway.(*gateway.Fake); ok && d.Pool != nil {
			slog.Warn("registering the FAKE payment route at POST /api/dev/pay/{code}")
			api.With(rateLimit(paying, d.BehindProxy)).
				Post("/dev/pay/{code}", devPay(fake, d.Pool, d.StripeWebhookSecret, letters))
		}

		// Cancelling moves money, so it shares the payment allowance rather than
		// the readers' loose one. The signed link is the real gate; the limit is
		// what stops a leaked one being used to hammer the processor.
		cancel := databaseRequired
		if d.Pool != nil {
			cancel = cancelBooking(d.Pool, db.New(d.Pool), d.Links)
		}
		api.With(rateLimit(paying, d.BehindProxy)).Post("/bookings/{code}/cancel", cancel)

		if d.Pool != nil {
			q := db.New(d.Pool)
			api.Get("/availability", searchAvailability(q))
			api.Get("/calendar", calendar(q))
			api.Get("/rooms/{slug}", room(q))
			api.Get("/bookings/{code}", getBooking(q))
			api.Get("/bookings/{code}/manage", manageBooking(q, d.Links))
			api.Get("/bookings/{code}/confirmation.pdf", confirmationPDF(q, d.Links))
		} else {
			// Better an honest 503 than a route that silently does not exist.
			api.Get("/availability", databaseRequired)
			api.Get("/calendar", databaseRequired)
			api.Get("/rooms/{slug}", databaseRequired)
			api.Get("/bookings/{code}", databaseRequired)
			api.Get("/bookings/{code}/manage", databaseRequired)
			api.Get("/bookings/{code}/confirmation.pdf", databaseRequired)
		}

		// Remaining domain routes land here: admin.
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "no such endpoint",
			})
		})
	})

	// The webhook, on the root router rather than under /api.
	//
	// It is Stripe's own route and answers to a signature rather than to this
	// server's rate limits or JSON conventions, so it does not belong in the
	// guest-facing API's middleware stack. It is also what actually confirms a
	// booking: the browser redirect does not, because a guest who pays and
	// closes the tab has still paid.
	//
	// Registered only when both a pool and a webhook secret exist. Without a
	// secret nothing could verify a delivery, and a route that accepted
	// unverified ones would let anyone confirm their own stay for free — so the
	// path is left to fall through to the 404 below, which is what makes Stripe
	// retry rather than record every event as delivered.
	if d.Pool != nil && d.StripeWebhookSecret != "" {
		r.Post("/webhooks/stripe", stripeWebhook(d.Pool, d.StripeWebhookSecret, letters))
	}

	// Everything that is not /api is the SPA. No CORS, no second origin.
	//
	// Reads only. A POST to an unrouted path is not a client-side route that
	// needs index.html, it is a caller who thinks an endpoint exists — and
	// answering it with the page and a 200 is worse than answering nothing.
	// `POST /webhooks/stripe` is the case this is written for, and it still
	// matters now that the route exists: it is registered only when a webhook
	// secret is configured, so a deploy that forgets one must 404 and have
	// Stripe retry rather than answer index.html and have it record every
	// payment as delivered.
	spa := serveSPA(d.SPA)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			spa(w, r)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "no such endpoint",
			})
		}
	})

	return r
}

func badRequest(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": reason})
}

// serverError logs the cause and tells the client nothing about it.
func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed",
		"err", err,
		"path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()),
	)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "something went wrong",
	})
}

func databaseRequired(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "this endpoint needs a database; DATABASE_URL is not configured",
	})
}
