package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"bealhouse/internal/availability"
	db "bealhouse/internal/db/gen"
)

// searchAvailability serves GET /api/availability.
//
//	?checkin=2027-06-10&checkout=2027-06-13&guests=2&pet=true
//
// Bad input is a 400 with a reason the UI can show, not a 500: a guest typing a
// date in the past has not broken anything.
func searchAvailability(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		checkin, err := parseDate(query.Get("checkin"))
		if err != nil {
			badRequest(w, "checkin must be a date in YYYY-MM-DD form")
			return
		}
		checkout, err := parseDate(query.Get("checkout"))
		if err != nil {
			badRequest(w, "checkout must be a date in YYYY-MM-DD form")
			return
		}

		// Guests defaults to 1 rather than erroring, so a bare date search works.
		guests := 1
		if raw := query.Get("guests"); raw != "" {
			guests, err = strconv.Atoi(raw)
			if err != nil {
				badRequest(w, "guests must be a whole number")
				return
			}
		}

		result, err := availability.Search(r.Context(), q, availability.Request{
			Checkin:  checkin,
			Checkout: checkout,
			Guests:   guests,
			WithPet:  query.Get("pet") == "true",
		})
		if err != nil {
			switch {
			case errors.Is(err, availability.ErrCheckoutNotAfterCheckin):
				badRequest(w, "check-out must be after check-in")
			case errors.Is(err, availability.ErrCheckinInPast):
				badRequest(w, "check-in cannot be in the past")
			case errors.Is(err, availability.ErrGuestsOutOfRange):
				badRequest(w, "at least one guest is required")
			default:
				serverError(w, r, err)
			}
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}

func parseDate(s string) (time.Time, error) {
	return time.Parse(time.DateOnly, s)
}
