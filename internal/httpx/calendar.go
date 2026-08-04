package httpx

import (
	"net/http"

	"bealhouse/internal/availability"
	db "bealhouse/internal/db/gen"
)

// calendar serves GET /api/calendar, which is what the date picker greys dates
// from.
//
//	?from=2027-06-01&to=2027-09-01&guests=2&pet=true
//
// Every parameter is optional. A picker opening on the current month has no
// dates to offer yet and should not have to invent a window to ask about one.
func calendar(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		var req availability.CalendarRequest

		if raw := query.Get("from"); raw != "" {
			from, err := parseDate(raw)
			if err != nil {
				badRequest(w, "from must be a date in YYYY-MM-DD form")
				return
			}
			req.From = from
		}
		if raw := query.Get("to"); raw != "" {
			to, err := parseDate(raw)
			if err != nil {
				badRequest(w, "to must be a date in YYYY-MM-DD form")
				return
			}
			req.To = to
		}

		guests, err := parseGuests(query.Get("guests"))
		if err != nil {
			badRequest(w, "guests must be a whole number")
			return
		}
		req.Guests = guests
		req.WithPet = query.Get("pet") == "true"

		result, err := availability.Spans(r.Context(), q, req)
		if err != nil {
			serverError(w, r, err)
			return
		}

		// The calendar changes the moment anyone books, so it is never cached.
		// A picker offering a room that went ten minutes ago is the one failure
		// this endpoint exists to prevent.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}
}
