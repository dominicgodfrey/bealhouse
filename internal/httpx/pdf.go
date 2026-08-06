package httpx

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/pdf"
)

// confirmationPDF serves GET /api/bookings/{code}/confirmation.pdf.
//
// Behind the signed link, like the manage page: the document carries the guest's
// name, and a booking code is not something to hand that out on. It is what the
// manage page links to and what a guest keeps, prints, or hands to whoever is
// paying for the trip.
//
// Rendered on demand rather than stored. It is a few kilobytes of the booking's
// own snapshot, so generating it is cheaper than keeping a file per stay in step
// with a row that can still change — and a stay whose balance landed this
// morning must not hand out a PDF saying it is outstanding.
// The inn's name is left to pdf.Render's default, the same way email.New
// supplies it for the letterhead. It is one string with no setting behind it;
// two settings that had to agree would be worse than one default in each.
func confirmationPDF(q *db.Queries, links *booking.Links) http.HandlerFunc {
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

		// The guest's name and what has actually been collected, neither of which
		// booking.Get returns — it answers an endpoint that takes no token.
		paid, err := q.GetBookingForPayment(r.Context(), found.Code)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such booking"})
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		doc, err := pdf.Render(confirmationFor(found, paid))
		if err != nil {
			serverError(w, r, err)
			return
		}

		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Length", fmt.Sprint(len(doc)))
		// Downloaded rather than opened in place: a guest on a phone tapping this
		// wants it in their files, and the filename is what they will search for.
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", "beal-house-"+found.Code+".pdf"))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(doc)
	}
}

// confirmationFor assembles the document from the two rows that describe a stay.
//
// Nothing here computes money. Every figure is the snapshot taken when the guest
// booked, so the document, the email and the page can never disagree.
func confirmationFor(b booking.Booking, paid db.GetBookingForPaymentRow) pdf.Confirmation {
	out := pdf.Confirmation{
		Code:        b.Code,
		Guest:       paid.GuestName,
		Guests:      b.Guests,
		PetFeeCents: b.Quote.PetFeeCents,
		TaxCents:    b.Quote.TaxCents,
		TotalCents:  b.Quote.TotalCents,
		PaidCents:   paid.AmountPaidCents,
	}

	// Dates are strings on the wire and civil dates everywhere else. A booking
	// that came out of the database always parses, so a failure here leaves the
	// zero date rather than refusing the document over a field nobody reads back.
	out.Checkin, _ = parseDate(b.Checkin)
	out.Checkout, _ = parseDate(b.Checkout)

	if len(b.Rooms) > 0 {
		room := b.Rooms[0]
		out.RoomName = room.Name

		// One row per night, dated from the arrival. The prices are the snapshot
		// in booking_rooms.nightly_prices, in order, so night n is arrival + n.
		for i, cents := range room.NightlyCents {
			out.Nights = append(out.Nights, pdf.Night{
				Date:        civil.AddDays(out.Checkin, i),
				AmountCents: cents,
			})
		}
	}

	// What is left, not the snapshotted balance: if the T-7 charge has already
	// landed, this is zero and the document says paid in full.
	if outstanding := b.Quote.TotalCents - paid.AmountPaidCents; outstanding > 0 {
		out.BalanceCents = outstanding
		if paid.BalanceChargeAt.Valid {
			out.BalanceChargeOn = paid.BalanceChargeAt.Time
		}
	}

	return out
}
