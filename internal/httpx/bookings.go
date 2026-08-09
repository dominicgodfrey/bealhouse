package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"bealhouse/internal/booking"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
)

// maxBookingBody is generous for the handful of fields a booking carries and
// small enough that nobody can make the server read a megabyte of JSON before
// finding out it is nonsense.
const maxBookingBody = 8 << 10

// bookingRequest is the wire form of a booking. Dates are strings here and
// civil dates by the time they reach the domain; nothing downstream ever sees
// a timestamp.
type bookingRequest struct {
	RoomSlug           string        `json:"roomSlug"`
	Checkin            string        `json:"checkin"`
	Checkout           string        `json:"checkout"`
	Guests             int           `json:"guests"`
	WithPet            bool          `json:"withPet"`
	ExpectedTotalCents int64         `json:"expectedTotalCents"`
	Guest              booking.Guest `json:"guest"`

	// The guest ticking the policies box. What is stored is a server-side
	// timestamp, never this value — a client cannot claim to have agreed at a
	// time of its choosing, only that it agreed now.
	AcceptedPolicies bool `json:"acceptedPolicies"`
}

// createBooking serves POST /api/bookings: it books a room and holds it.
//
// Nothing is charged. The hold is a room_occupancy row with an expiry, and
// payment is build-order step 4, so this whole endpoint works without a Stripe
// account.
func createBooking(beginner booking.Beginner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body bookingRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBookingBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			badRequest(w, "the booking could not be read as JSON")
			return
		}

		checkin, err := parseDate(body.Checkin)
		if err != nil {
			badRequest(w, "checkin must be a date in YYYY-MM-DD form")
			return
		}
		checkout, err := parseDate(body.Checkout)
		if err != nil {
			badRequest(w, "checkout must be a date in YYYY-MM-DD form")
			return
		}

		made, err := booking.Create(r.Context(), beginner, booking.Request{
			RoomSlug:           body.RoomSlug,
			Checkin:            checkin,
			Checkout:           checkout,
			Guests:             body.Guests,
			WithPet:            body.WithPet,
			Guest:              body.Guest,
			ExpectedTotalCents: body.ExpectedTotalCents,
			AcceptedPolicies:   body.AcceptedPolicies,
		})
		if err != nil {
			bookingProblem(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, made)
	}
}

// getBooking serves GET /api/bookings/{code}, which the confirm page polls for
// the hold countdown.
//
// It answers with the stay and what it costs, and deliberately not with the
// guest's name, email or phone. A booking code is an identifier, not an
// authenticator; contact details wait for the signed expiring link in
// decision #19.
func getBooking(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := booking.Get(r.Context(), q, chi.URLParam(r, "code"))
		switch {
		case errors.Is(err, booking.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})
		case err != nil:
			serverError(w, r, err)
		default:
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, found)
		}
	}
}

// bookingProblem maps a failed booking onto a status code.
//
// The room being gone is a 409 rather than a 400: the request was well formed
// and would have worked a minute earlier, and the guest's next move is to pick
// again rather than to correct anything.
func bookingProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, occupancy.ErrRoomTaken):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "that room has just been taken for those dates; please choose another",
		})
	// Everything else the availability check rejects — occupied, too small,
	// unpriced, or under the minimum stay — says the same thing, because the
	// guest's move is the same in all four and spelling them out to an
	// anonymous caller only helps someone mapping the calendar. The date
	// picker means nobody arrives here by an honest route anyway.
	case errors.Is(err, booking.ErrRoomUnavailable):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "that room is not available for those dates; please choose another",
		})
	case errors.Is(err, booking.ErrPriceChanged):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "the price for those dates has changed; please review the new total",
		})
	case errors.Is(err, booking.ErrGuestNameRequired):
		badRequest(w, "a name is required")
	case errors.Is(err, booking.ErrGuestEmailRequired):
		badRequest(w, "a valid email address is required")
	case errors.Is(err, booking.ErrPoliciesNotAccepted):
		badRequest(w, "the Beal House policies have to be accepted before booking")
	default:
		if reason, ok := searchProblem(err); ok {
			badRequest(w, reason)
			return
		}
		serverError(w, r, err)
	}
}
