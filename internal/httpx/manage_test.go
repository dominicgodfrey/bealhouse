package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/payments"
	"bealhouse/internal/testdb"
)

const linkSecret = "httpx-test-manage-secret"

// A confirmed stay in this package's stretch of calendar, and a link to it.
func manageable(t *testing.T) (context.Context, pgx.Tx, *db.Queries, booking.Booking, string) {
	t.Helper()

	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	ctx := context.Background()
	q := db.New(tx)

	made, err := booking.Create(ctx, tx, booking.Request{
		RoomSlug:         "rose-chamber",
		Checkin:          civil.AddDays(civil.Today(), stayStart),
		Checkout:         civil.AddDays(civil.Today(), stayEnd),
		Guests:           2,
		Guest:            booking.Guest{Name: "Ada Lovelace", Email: "ada@example.com", Phone: "603-555-0100"},
		AcceptedPolicies: true,
	})
	if err != nil {
		t.Fatalf("holding a room: %v", err)
	}

	if _, err := payments.RecordCharge(ctx, tx, payments.Charge{
		BookingCode: made.Code,
		StripeID:    "pi_manage_" + made.Code,
		Kind:        payments.KindDeposit,
		AmountCents: made.Quote.DepositCents,
	}); err != nil {
		t.Fatalf("recording the deposit: %v", err)
	}

	token := booking.NewLinks(linkSecret).Sign(made.Code, time.Now().Add(time.Hour))
	return ctx, tx, q, made, token
}

// call drives a handler with the code in chi's route context, the way the
// router would.
func call(t *testing.T, h http.HandlerFunc, method, code, token string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/api/bookings/" + code + "/manage"
	if token != "" {
		target += "?t=" + token
	}

	req := httptest.NewRequest(method, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("code", code)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestManageAnswersTheStayAndItsRefundBehindTheLink(t *testing.T) {
	ctx, _, q, made, token := manageable(t)

	rec := call(t, manageBooking(q, booking.NewLinks(linkSecret)), http.MethodGet, made.Code, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", rec.Code, rec.Body)
	}

	var got manageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}

	if got.Booking.Code != made.Code {
		t.Errorf("returned booking %q, want %q", got.Booking.Code, made.Code)
	}
	if !got.Cancellable {
		t.Errorf("a confirmed stay 400 days out is not cancellable: %q", got.Reason)
	}

	// The figure the page shows has to be the one cancelling would produce, or
	// the button lies. Both come from the same arithmetic against the same day.
	quote, err := payments.RefundFor(ctx, q, made.Code, civil.Today())
	if err != nil {
		t.Fatalf("quoting: %v", err)
	}
	if got.Refund.RefundCents != quote.RefundCents {
		t.Errorf("page shows %d, cancelling would return %d", got.Refund.RefundCents, quote.RefundCents)
	}
	if got.Refund.RefundCents >= made.Quote.DepositCents {
		t.Errorf("refund of %d does not keep the processor's cut of %d",
			got.Refund.RefundCents, made.Quote.DepositCents)
	}
}

// The gate. A booking code is short, spoken aloud and printed on paperwork, so
// it cannot be the only thing between a stranger and somebody else's stay.
func TestManageAndCancelRefuseWithoutAValidLink(t *testing.T) {
	ctx, tx, q, made, token := manageable(t)
	links := booking.NewLinks(linkSecret)

	elsewhere := booking.NewLinks("a-different-deployments-secret").
		Sign(made.Code, time.Now().Add(time.Hour))

	for _, c := range []struct {
		name  string
		token string
	}{
		{"no token at all", ""},
		{"a token that is not one", "nonsense"},
		{"a token signed with another secret", elsewhere},
		{"an expired token", links.Sign(made.Code, time.Now().Add(-time.Minute))},
	} {
		t.Run(c.name, func(t *testing.T) {
			read := call(t, manageBooking(q, links), http.MethodGet, made.Code, c.token)
			if read.Code != http.StatusForbidden {
				t.Errorf("manage answered %d, want 403", read.Code)
			}

			wrote := call(t, cancelBooking(tx, q, links), http.MethodPost, made.Code, c.token)
			if wrote.Code != http.StatusForbidden {
				t.Errorf("cancel answered %d, want 403", wrote.Code)
			}
		})
	}

	// And none of it touched the booking.
	after, err := q.GetBookingForPayment(ctx, made.Code)
	if err != nil {
		t.Fatalf("reading the booking back: %v", err)
	}
	if after.Status != booking.StatusConfirmed {
		t.Errorf("status %q after refused requests, want confirmed", after.Status)
	}

	// Sanity: the real token does work, so the 403s above are the gate rather
	// than something else being broken.
	if rec := call(t, manageBooking(q, links), http.MethodGet, made.Code, token); rec.Code != http.StatusOK {
		t.Errorf("the genuine link answered %d, want 200", rec.Code)
	}
}

// Nothing about the endpoint may reveal which six-character codes exist: the
// answer to an unknown booking is the same 403 as a bad token.
func TestAnUnknownCodeIsIndistinguishableFromABadToken(t *testing.T) {
	_, _, q, _, _ := manageable(t)
	links := booking.NewLinks(linkSecret)

	rec := call(t, manageBooking(q, links), http.MethodGet, "BH-NOSUCH", "nonsense")
	if rec.Code != http.StatusForbidden {
		t.Errorf("answered %d for a booking that does not exist, want the same 403", rec.Code)
	}
}

func TestTheConfirmationPDFIsBehindTheSameLink(t *testing.T) {
	_, _, q, made, token := manageable(t)
	links := booking.NewLinks(linkSecret)

	rec := call(t, confirmationPDF(q, links), http.MethodGet, made.Code, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q", got)
	}
	// The filename is what a guest searches their downloads for.
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, made.Code) {
		t.Errorf("Content-Disposition = %q, want the reference in the filename", got)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Error("the body is not a PDF")
	}

	// It carries the guest's name, which is the reason it is behind the token at
	// all: the code alone must not hand that out.
	if bare := call(t, confirmationPDF(q, links), http.MethodGet, made.Code, ""); bare.Code != http.StatusForbidden {
		t.Errorf("answered %d without a token, want 403", bare.Code)
	}
}

func TestCancelThroughTheEndpointReleasesTheRoom(t *testing.T) {
	ctx, tx, q, made, token := manageable(t)
	links := booking.NewLinks(linkSecret)

	rec := call(t, cancelBooking(tx, q, links), http.MethodPost, made.Code, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200: %s", rec.Code, rec.Body)
	}

	var got refundPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if got.RefundCents <= 0 {
		t.Errorf("refunded %d for a stay cancelled over a year out", got.RefundCents)
	}

	after, err := q.GetBookingForPayment(ctx, made.Code)
	if err != nil {
		t.Fatalf("reading the booking back: %v", err)
	}
	if after.Status != booking.StatusCancelled {
		t.Errorf("status %q, want cancelled", after.Status)
	}

	var occupancies int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM room_occupancy WHERE booking_id = $1", after.ID,
	).Scan(&occupancies); err != nil {
		t.Fatalf("reading occupancy: %v", err)
	}
	if occupancies != 0 {
		t.Errorf("the room is still held by %d rows after cancelling", occupancies)
	}

	// A second attempt is a conflict, not another refund.
	repeat := call(t, cancelBooking(tx, q, links), http.MethodPost, made.Code, token)
	if repeat.Code != http.StatusConflict {
		t.Errorf("cancelling twice answered %d, want 409", repeat.Code)
	}
}
