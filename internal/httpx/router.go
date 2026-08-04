// Package httpx wires the HTTP surface: the JSON API under /api and the
// embedded SPA on everything else.
package httpx

import (
	"io/fs"
	"log/slog"
	"net/http"

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
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(api chi.Router) {
		api.Get("/health", health(d.Pool))

		if d.Pool != nil {
			q := db.New(d.Pool)
			api.Get("/availability", searchAvailability(q))
			api.Get("/calendar", calendar(q))
			api.Get("/rooms/{slug}", room(q))

			// Creating a booking needs to start its own transaction rather
			// than borrow a connection, because the booking and the hold that
			// reserves its room have to commit together or not at all.
			api.Post("/bookings", createBooking(d.Pool))
			api.Get("/bookings/{code}", getBooking(q))
		} else {
			// Better an honest 503 than a route that silently does not exist.
			api.Get("/availability", databaseRequired)
			api.Get("/calendar", databaseRequired)
			api.Get("/rooms/{slug}", databaseRequired)
			api.Post("/bookings", databaseRequired)
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
	r.NotFound(serveSPA(d.SPA))

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
