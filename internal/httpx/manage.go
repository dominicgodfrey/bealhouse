package httpx

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/payments"
)

// manageResponse is a guest's own view of their stay, plus what cancelling it
// today would return.
//
// The refund is quoted rather than merely promised: decision #19 sends a guest
// to a page with a cancel button on it, and a button that spends money without
// first saying how much is one nobody should press.
type manageResponse struct {
	Booking booking.Booking `json:"booking"`
	Refund  refundPreview   `json:"refund"`

	// Cancellable is false for a stay that is already over, already cancelled,
	// or never confirmed. Reason says which, in words for the guest.
	Cancellable bool   `json:"cancellable"`
	Reason      string `json:"reason,omitempty"`
}

type refundPreview struct {
	PaidCents     int64 `json:"paidCents"`
	RetainedCents int64 `json:"retainedCents"`
	RefundCents   int64 `json:"refundCents"`

	// Late is the guest-visible half of decision #9: inside seven days the
	// deposit is forfeit, and the page says so before the button is pressed.
	Late bool `json:"late"`
}

// manageBooking serves GET /api/bookings/{code}/manage.
//
// Behind the signed link, which is what lets it answer with more than the
// polling endpoint does. Everything here is derived from the booking's own
// snapshot, so a rate edit since the guest booked cannot move any of it.
func manageBooking(q *db.Queries, links *booking.Links) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if !authorised(r, links, code) {
			forbidden(w)
			return
		}

		found, err := booking.Get(r.Context(), q, code)
		if errors.Is(err, booking.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		today := civil.Today()

		quote, err := payments.RefundFor(r.Context(), q, code, today)
		if err != nil {
			serverError(w, r, err)
			return
		}

		out := manageResponse{
			Booking: found,
			Refund: refundPreview{
				PaidCents:     quote.PaidCents,
				RetainedCents: quote.RetainedCents,
				RefundCents:   quote.RefundCents,
				Late:          quote.Late,
			},
		}
		out.Cancellable, out.Reason = cancellable(found, today)

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}
}

// cancelBooking serves POST /api/bookings/{code}/cancel.
//
// The refund it computes is the one the page has already shown, because both
// come from the same function against the same civil day. The money moves in a
// queued job rather than here: this request cancels the stay and puts the room
// back on sale, and a guest whose browser gave up waiting on Stripe must not be
// left with a booking that is neither cancelled nor refunded.
func cancelBooking(beginner payments.Beginner, q *db.Queries, links *booking.Links) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if !authorised(r, links, code) {
			forbidden(w)
			return
		}

		done, err := payments.Cancel(r.Context(), beginner, code, civil.Today())
		switch {
		case errors.Is(err, payments.ErrBookingNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})
		case errors.Is(err, payments.ErrNotCancellable):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "this booking is not one that can be cancelled online; " +
					"please contact the inn",
			})
		case errors.Is(err, payments.ErrStayUnderway):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "this stay has already begun; please speak to the inn directly",
			})
		case err != nil:
			serverError(w, r, err)
		default:
			writeJSON(w, http.StatusOK, refundPreview{
				PaidCents:     done.RetainedCents + done.RefundCents,
				RetainedCents: done.RetainedCents,
				RefundCents:   done.RefundCents,
				Late:          done.Late,
			})
		}
	}
}

// authorised checks the signed link.
//
// The token is a query parameter because its only source is a link in an email,
// and there is nowhere else in a clicked URL to put one. It is a capability, so
// it is checked on every request rather than exchanged for a session: there is
// no account here to have a session with (decision #11).
// A real instant rather than civil.Today: the expiry is a moment the token
// stops working, not a date the inn does business on.
func authorised(r *http.Request, links *booking.Links, code string) bool {
	return links.Valid(code, r.URL.Query().Get("t"), time.Now())
}

// forbidden answers a missing, wrong or expired token.
//
// One answer for all three, and the same answer for a booking that does not
// exist would give away — without it, this endpoint would confirm which
// six-character codes are real to anyone willing to try them.
func forbidden(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "this link is not valid, or has expired; " +
			"the inn can send you a new one",
	})
}

// cancellable says whether the button should be offered, and why not.
//
// The reasons are the guest's, not the system's: a stay that has begun and one
// that was cancelled last week are both "no", and telling them apart is the
// difference between a page that helps and one that says "conflict".
func cancellable(b booking.Booking, today time.Time) (bool, string) {
	switch b.Status {
	case booking.StatusCancelled:
		return false, "This booking has been cancelled."
	case booking.StatusExpired:
		return false, "This booking expired before it was paid for."
	case booking.StatusPending:
		return false, "This booking has not been paid for yet."
	}

	// Both sides are civil YYYY-MM-DD, where lexicographic order is date order.
	if b.Checkin <= today.Format(time.DateOnly) {
		return false, "This stay has already begun. Please speak to the inn directly."
	}
	return true, ""
}
