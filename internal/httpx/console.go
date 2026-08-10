package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"bealhouse/internal/console"
)

// The owner's console, above internal/console.
//
// Everything here decodes a request, calls one method, and encodes an answer.
// No rule lives in this file: not the refund arithmetic, not the availability
// re-check, not what a season save does to the calendar. That is deliberate —
// the console is the surface where an owner is *allowed* to do things a guest
// cannot, and the moment a rule exists here as well as in the domain, the two
// start to drift and the version an owner reaches is the one nobody tested.
//
// Every route below sits behind requireAdmin and sameSiteOnly. There is no
// second authorisation check in any handler because there is nothing to check
// against: one account, and the session is what says whose it is.

// consoleBodyLimit bounds a console write.
//
// Larger than the guest API's, because a room description, a whole menu and a
// long-form about page all come through here as one document, and smaller than
// unbounded, because this is still a public port.
const consoleBodyLimit = 512 << 10

// decodeBody reads a JSON body into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, consoleBodyLimit)).Decode(v); err != nil {
		return console.BadRequest{Reason: "that request body is not the JSON this endpoint expects"}
	}
	return nil
}

// consoleError maps the domain's errors onto status codes, in one place so a
// new handler cannot invent a fourth way to report the same three things.
//
// Unlike the authentication surface, which answers every refusal identically,
// this one is allowed to be specific: the caller is already signed in, so
// telling them a booking code does not exist reveals nothing they could not
// read off the list they are looking at.
func consoleError(w http.ResponseWriter, r *http.Request, err error) {
	var bad console.BadRequest
	switch {
	case errors.As(err, &bad):
		badRequest(w, bad.Reason)
	case errors.Is(err, console.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such record"})
	default:
		serverError(w, r, err)
	}
}

// mountConsole registers everything behind the session.
//
// Called from inside mountAdmin's authenticated group, so nothing here has to
// remember to be closed — the closure is a property of where it is mounted
// rather than of each handler getting it right.
func mountConsole(in chi.Router, ops *console.Ops) {
	if ops == nil {
		// No database. The auth routes above still answer, so the owner gets a
		// console that says what is wrong rather than a screen of empty boxes.
		in.HandleFunc("/today", databaseRequired)
		in.HandleFunc("/bookings", databaseRequired)
		in.HandleFunc("/calendar", databaseRequired)
		in.HandleFunc("/rates", databaseRequired)
		in.HandleFunc("/guests", databaseRequired)
		in.HandleFunc("/rooms", databaseRequired)
		in.HandleFunc("/settings", databaseRequired)
		in.HandleFunc("/menu", databaseRequired)
		in.HandleFunc("/events", databaseRequired)
		in.HandleFunc("/inquiries", databaseRequired)
		in.HandleFunc("/email-templates", databaseRequired)
		in.HandleFunc("/copy", databaseRequired)
		return
	}

	in.Get("/today", consoleToday(ops))

	in.Get("/bookings", consoleBookings(ops))
	in.Post("/bookings", consoleCreateBooking(ops))
	in.Get("/bookings/{code}", consoleBooking(ops))
	in.Post("/bookings/{code}/cancel", consoleCancel(ops))
	in.Post("/bookings/{code}/refund", consoleRefund(ops))
	in.Post("/bookings/{code}/request-payment", consoleRequestPayment(ops))
	in.Post("/bookings/{code}/collect", consoleCollectPayment(ops))

	in.Get("/calendar", consoleCalendar(ops))
	in.Post("/blocks", consoleBlock(ops))
	in.Delete("/blocks/{id}", consoleUnblock(ops))

	in.Get("/rates", consoleRates(ops))
	in.Post("/rates/preview", consoleRatePreview(ops))
	in.Post("/rates/seasons", consoleSaveSeason(ops))
	in.Delete("/rates/seasons/{id}", consoleDeleteSeason(ops))
	in.Post("/rates/rebuild", consoleRebuild(ops))

	in.Get("/guests", consoleGuests(ops))
	in.Get("/guests/{id}", consoleGuest(ops))
	in.Post("/guests/{id}/notes", consoleAddNote(ops))
	in.Delete("/guests/{id}/notes/{noteId}", consoleDeleteNote(ops))

	in.Get("/rooms", consoleRooms(ops))
	in.Put("/rooms/{id}", consoleSaveRoom(ops))

	in.Get("/settings", consoleSettings(ops))
	in.Put("/settings", consoleSaveSettings(ops))

	in.Get("/menu", consoleMenu(ops))
	in.Put("/menu", consoleSaveMenu(ops))

	in.Get("/events", consoleEvents(ops))
	in.Put("/events", consoleSaveEvents(ops))

	in.Get("/inquiries", consoleInquiries(ops))
	in.Put("/inquiries/{id}", consoleSetInquiryStatus(ops))

	in.Get("/email-templates", consoleEmailCopy(ops))
	in.Put("/email-templates/{name}", consoleSaveEmailCopy(ops))
	in.Delete("/email-templates/{name}", consoleResetEmailCopy(ops))

	// A POST because it carries the unsaved draft in its body, and writes
	// nothing. The alternative — previewing whatever is stored — would answer
	// the owner's question one save too late.
	in.Post("/email-templates/{name}/preview", consolePreviewEmailCopy(ops))

	in.Get("/copy", consoleCopy(ops))
	in.Put("/copy/{slug}", consoleSaveCopy(ops))
	in.Put("/copy/{slug}/photos", consoleSavePagePhotos(ops))
	in.Get("/attractions", consoleAttractions(ops))
	in.Put("/attractions", consoleSaveAttractions(ops))
}

// ---------------------------------------------------------------------------
// Today and bookings
// ---------------------------------------------------------------------------

func consoleToday(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// `on` is a parameter and not a clock read in the handler, so the owner
		// can look at tomorrow's arrivals tonight. It defaults to the civil day
		// at the inn, which is the only "today" this system recognises.
		on := console.Today()
		if raw := r.URL.Query().Get("on"); raw != "" {
			parsed, err := parseDate(raw)
			if err != nil {
				badRequest(w, "on must be a date in YYYY-MM-DD form")
				return
			}
			on = parsed
		}

		board, err := ops.Today(r.Context(), on)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, board)
	}
}

func consoleBookings(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		stays, err := ops.Bookings(r.Context(), console.BookingFilter{
			From:        query.Get("from"),
			To:          query.Get("to"),
			Status:      query.Get("status"),
			RoomID:      idParam(query.Get("room")),
			Query:       query.Get("q"),
			OnlyFlagged: query.Get("flagged") == "true",
			Limit:       intParam(query.Get("limit")),
		})
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, stays)
	}
}

func consoleBooking(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := ops.Booking(r.Context(), chi.URLParam(r, "code"), console.Today())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

// consoleCreateBooking is the manual reservation an owner took on the phone.
//
// It goes through booking.Create with Manual set, so the same availability
// re-check and the same exclusion constraint decide whether the room is free.
// An owner is allowed to take a booking the website would not offer; they are
// not allowed to double-book.
func consoleCreateBooking(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.ManualBooking
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		made, err := ops.CreateBooking(r.Context(), in)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, made)
	}
}

// consoleCancel cancels a stay and starts its refund.
//
// The browser sends no amount. What comes back is what the same arithmetic the
// guest's own page uses decided, computed against the civil day at the inn —
// so an owner and a guest cancelling the same booking on the same day get the
// same number, which is the property that matters when one of them is on the
// phone to the other.
func consoleCancel(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		done, err := ops.Cancel(r.Context(), chi.URLParam(r, "code"), console.Today())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, done)
	}
}

// consoleRefund sends money back without cancelling: the no-show, the
// cut-short visit, the goodwill gesture. No step-up authentication (decision
// #15) — it is the owners' call and the phone is locked.
func consoleRefund(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AmountCents int64 `json:"amountCents"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			consoleError(w, r, err)
			return
		}

		if err := ops.Refund(r.Context(), chi.URLParam(r, "code"), body.AmountCents); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// consoleRequestPayment emails the guest a link to pay what is outstanding.
//
// Its own endpoint as well as an option on the booking form, because the case
// recurs: a booking taken meaning to settle in cash, and a guest who a fortnight
// later would rather pay by card.
func consoleRequestPayment(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ops.RequestPayment(r.Context(), chi.URLParam(r, "code")); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// consoleCollectPayment opens a payment for a card somebody at the inn is about
// to key in, from what a guest is reading out over the telephone.
//
// **The card still never touches this server.** What this returns is a client
// secret; the console mounts Stripe's own card form against it, and the details
// go from that iframe to Stripe directly — the same path a guest's card takes on
// the pay page, and the reason this system stays in PCI SAQ-A. There is no
// endpoint anywhere here that accepts a card number, and there must never be.
//
// Separate from the guest-facing pay endpoint because the payment is built
// differently: declared as mail-order/telephone-order, so the bank does not
// send a 3-D Secure challenge to a guest who is on the phone and cannot answer
// one. It is behind the session rather than the booking code, since the only
// caller is an owner with the console open.
func consoleCollectPayment(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opened, err := ops.CollectByCard(r.Context(), chi.URLParam(r, "code"))
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, opened)
	}
}

// ---------------------------------------------------------------------------
// Calendar and blocking
// ---------------------------------------------------------------------------

func consoleCalendar(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to := console.DefaultCalendarWindow()

		query := r.URL.Query()
		if raw := query.Get("from"); raw != "" {
			parsed, err := parseDate(raw)
			if err != nil {
				badRequest(w, "from must be a date in YYYY-MM-DD form")
				return
			}
			from = parsed
		}
		if raw := query.Get("to"); raw != "" {
			parsed, err := parseDate(raw)
			if err != nil {
				badRequest(w, "to must be a date in YYYY-MM-DD form")
				return
			}
			to = parsed
		}

		grid, err := ops.Grid(r.Context(), from, to)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, grid)
	}
}

func consoleBlock(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.NewBlock
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		id, err := ops.Block(r.Context(), in)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
	}
}

func consoleUnblock(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a block id")
			return
		}
		if err := ops.Unblock(r.Context(), id); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Rates
// ---------------------------------------------------------------------------

func consoleRates(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		board, err := ops.Rates(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, board)
	}
}

// consoleRatePreview is decision #21's "show a diff before saving".
//
// A POST because it carries the whole proposed season in its body, not because
// it changes anything: the edit is applied inside a transaction that is rolled
// back, which is what makes the number the real resolution rule rather than an
// estimate from a second copy of it.
func consoleRatePreview(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.SaveSeason
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		change, err := ops.PreviewSeason(r.Context(), in)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, change)
	}
}

func consoleSaveSeason(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.SaveSeason
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		change, err := ops.SaveSeasonAndRebuild(r.Context(), in)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, change)
	}
}

func consoleDeleteSeason(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a season id")
			return
		}

		change, err := ops.DeleteSeason(r.Context(), id)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, change)
	}
}

func consoleRebuild(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nights, err := ops.Rebuild(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"nights": nights})
	}
}

// ---------------------------------------------------------------------------
// Guests
// ---------------------------------------------------------------------------

func consoleGuests(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		guests, err := ops.Guests(r.Context(), console.GuestSearch{
			Query:  query.Get("q"),
			RoomID: idParam(query.Get("room")),
			From:   query.Get("from"),
			To:     query.Get("to"),
			Limit:  intParam(query.Get("limit")),
		})
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, guests)
	}
}

func consoleGuest(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a guest id")
			return
		}

		file, err := ops.Guest(r.Context(), id)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, file)
	}
}

// consoleAddNote records who wrote it from the session, never from the body.
//
// There is one account today, so it changes nothing in practice — and it is
// worth getting right now rather than after a second user exists and every note
// in the table is attributed to whoever the browser claimed to be.
func consoleAddNote(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a guest id")
			return
		}

		var body struct {
			Body string `json:"body"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			consoleError(w, r, err)
			return
		}

		note, err := ops.AddNote(r.Context(), id, currentAdmin(r).UserID, body.Body)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, note)
	}
}

func consoleDeleteNote(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guestID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a guest id")
			return
		}
		noteID, err := strconv.ParseInt(chi.URLParam(r, "noteId"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a note id")
			return
		}

		if err := ops.DeleteNote(r.Context(), guestID, noteID); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Content
// ---------------------------------------------------------------------------

func consoleRooms(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rooms, err := ops.Rooms(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, rooms)
	}
}

// consoleSaveRoom takes the id from the path and overwrites whatever the body
// claims, so a body naming a different room edits the one in the URL rather
// than quietly editing another.
func consoleSaveRoom(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not a room id")
			return
		}

		var in console.RoomContent
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}
		in.ID = id

		if err := ops.SaveRoom(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleSettings(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := ops.Settings(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	}
}

func consoleSaveSettings(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.Settings
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}
		if err := ops.SaveSettings(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleMenu(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		menu, err := ops.Menu(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, menu)
	}
}

func consoleSaveMenu(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in []console.MenuSection
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}
		if err := ops.SaveMenu(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleEvents(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		events, err := ops.Events(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}

func consoleSaveEvents(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in []console.Event
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}
		if err := ops.SaveEvents(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleInquiries(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		inquiries, err := ops.Inquiries(r.Context(),
			q.Get("status"), q.Get("kind"), intParam(q.Get("limit")))
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, inquiries)
	}
}

func consoleSetInquiryStatus(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			badRequest(w, "that is not an inquiry id")
			return
		}

		var body struct {
			Status string `json:"status"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			consoleError(w, r, err)
			return
		}

		if err := ops.SetInquiryStatus(r.Context(), id, body.Status); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleEmailCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		copies, err := ops.EmailCopy(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, copies)
	}
}

// consoleSaveEmailCopy refuses copy that will not render, before it is stored.
//
// The failure it exists to prevent happens at send time, which is after the
// guest's card has been charged and with nothing in front of the owner to
// connect it to the sentence they typed. So the template is compiled here,
// while they are still looking at it, and the refusal names the problem.
func consoleSaveEmailCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			consoleError(w, r, err)
			return
		}

		err := ops.SaveEmailCopy(r.Context(), chi.URLParam(r, "name"), body.Subject, body.Body)
		if err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// consolePreviewEmailCopy renders a draft against sample data.
//
// The rendered document comes back as a JSON string rather than as text/html,
// so this endpoint can never be navigated to directly and made to serve
// owner-authored markup from the site's own origin. The console puts it in a
// sandboxed frame, which is where it is safe to look at.
func consolePreviewEmailCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			consoleError(w, r, err)
			return
		}

		msg, err := ops.PreviewEmailCopy(chi.URLParam(r, "name"), body.Subject, body.Body)
		if err != nil {
			consoleError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"subject": msg.Subject,
			"html":    msg.HTML,
		})
	}
}

func consoleResetEmailCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ResetEmailCopy(r.Context(), chi.URLParam(r, "name")); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pages, err := ops.Copy(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, pages)
	}
}

func consoleSaveCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.PageCopy
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}
		in.Slug = chi.URLParam(r, "slug")

		if err := ops.SaveCopy(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func consoleAttractions(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.Attractions(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func consoleSaveAttractions(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in []console.Attraction
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		if err := ops.SaveAttractions(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// consoleSavePagePhotos replaces one page's gallery.
//
// Its own endpoint rather than a field on the copy save, because the two are
// independent: emptying the prose deletes the page_copy row, and a gallery
// riding along on that request would go with it.
func consoleSavePagePhotos(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in []console.Photo
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		if err := ops.SavePagePhotos(r.Context(), chi.URLParam(r, "slug"), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Query-string helpers
// ---------------------------------------------------------------------------

// idParam reads an optional numeric filter. Zero is "not filtering", which is
// what the SQL reads it as, so an unparseable value narrows nothing rather than
// failing a whole screen over a stray character in a URL.
func idParam(s string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// intParam is the same for a limit, where zero means "use the default".
func intParam(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
