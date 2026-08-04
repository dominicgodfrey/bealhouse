// Package httpx wires the HTTP surface: the JSON API under /api and the
// embedded SPA on everything else.
package httpx

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
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

		// Domain routes land here in build-order step 2 onward:
		// availability, rooms, bookings, admin.
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
