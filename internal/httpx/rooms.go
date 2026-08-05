package httpx

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bealhouse/internal/availability"
	db "bealhouse/internal/db/gen"
)

// room serves GET /api/rooms/{slug}, the room page.
//
//	/api/rooms/flume
//	/api/rooms/flume?checkin=2027-06-10&checkout=2027-06-13&guests=2
//
// Dates are optional. Without them this is the room as the marketing site
// describes it; with them it also says whether the room is free and what the
// stay costs, so the guest carries their search through to the book button
// instead of re-entering it.
func room(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := optionalStay(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		detail, err := availability.Lookup(r.Context(), q, chi.URLParam(r, "slug"), req)
		switch {
		case errors.Is(err, availability.ErrRoomNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such room"})
		case err != nil:
			if reason, ok := searchProblem(err); ok {
				badRequest(w, reason)
				return
			}
			serverError(w, r, err)
		default:
			writeJSON(w, http.StatusOK, detail)
		}
	}
}

// optionalStay reads the search parameters when they are present and returns
// nil when they are not. Half a stay — a check-in with no check-out — is a
// caller bug rather than a dateless request, so it is rejected.
func optionalStay(r *http.Request) (*availability.Request, error) {
	query := r.URL.Query()
	rawIn, rawOut := query.Get("checkin"), query.Get("checkout")

	if rawIn == "" && rawOut == "" {
		return nil, nil
	}

	checkin, err := parseDate(rawIn)
	if err != nil {
		return nil, errors.New("checkin must be a date in YYYY-MM-DD form")
	}
	checkout, err := parseDate(rawOut)
	if err != nil {
		return nil, errors.New("checkout must be a date in YYYY-MM-DD form")
	}
	guests, err := parseGuests(query.Get("guests"))
	if err != nil {
		return nil, errors.New("guests must be a whole number")
	}

	return &availability.Request{
		Checkin:  checkin,
		Checkout: checkout,
		Guests:   guests,
		WithPet:  query.Get("pet") == "true",
	}, nil
}

// searchProblem translates the validation the domain does into something a
// guest can read, in one place, because three endpoints now share it.
func searchProblem(err error) (string, bool) {
	switch {
	case errors.Is(err, availability.ErrCheckoutNotAfterCheckin):
		return "check-out must be after check-in", true
	case errors.Is(err, availability.ErrCheckinInPast):
		return "check-in cannot be in the past", true
	case errors.Is(err, availability.ErrGuestsOutOfRange):
		return "at least one guest is required", true
	// The one refusal with somewhere to send the guest. Everything else here is
	// a correctable mistake; this is an invitation to get in touch.
	case errors.Is(err, availability.ErrStayTooLong):
		return "that stay is longer than we can book online; please contact the inn to arrange it", true
	default:
		return "", false
	}
}
