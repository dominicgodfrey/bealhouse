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

	db "bealhouse/internal/db/gen"
)

// Deps are the collaborators the HTTP layer needs. Pool may be nil when the
// server is started without a DATABASE_URL.
type Deps struct {
	Pool *pgxpool.Pool
	SPA  fs.FS

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

	reads := newLimiter(apiRate, apiBurst)
	bookings := newLimiter(bookingRate, bookingBurst)

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

		if d.Pool != nil {
			q := db.New(d.Pool)
			api.Get("/availability", searchAvailability(q))
			api.Get("/calendar", calendar(q))
			api.Get("/rooms/{slug}", room(q))
			api.Get("/bookings/{code}", getBooking(q))
		} else {
			// Better an honest 503 than a route that silently does not exist.
			api.Get("/availability", databaseRequired)
			api.Get("/calendar", databaseRequired)
			api.Get("/rooms/{slug}", databaseRequired)
			api.Get("/bookings/{code}", databaseRequired)
		}

		// Remaining domain routes land here: admin.
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "no such endpoint",
			})
		})
	})

	// Everything that is not /api is the SPA. No CORS, no second origin.
	//
	// Reads only. A POST to an unrouted path is not a client-side route that
	// needs index.html, it is a caller who thinks an endpoint exists — and
	// answering it with the page and a 200 is worse than answering nothing.
	// `POST /webhooks/stripe` is the case this is written for: registered on
	// the root router in the next step, and until it exists Stripe would
	// otherwise record every event as delivered successfully and never retry.
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
