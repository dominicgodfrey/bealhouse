package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/availability"
	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/media"
	"bealhouse/internal/pricing"
)

// The marketing site's read endpoints, plus the one write on it.
//
// All of them serve content the owner manages in the console, and all of them
// answer with an empty list or an empty page rather than a placeholder when
// nothing has been written yet. That is the same rule the seed follows: a
// placeholder in the database is one somebody has to remember to delete, and a
// page that invents a sentence about the restaurant is a page that will still
// be lying about it a year later.

// RoomCard is a room as the rooms index lists it.
//
// A summary, not the room page: enough to choose from, with the search carried
// through to the detail. FromCents is the cheapest night currently on the
// calendar, and is absent — not zero — for a room no season prices, because
// such a room cannot be sold at all and "from $0.00" would be a lie the booking
// flow then refuses to honour.
type RoomCard struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	View        string `json:"view,omitempty"`

	MaxOccupancy int      `json:"maxOccupancy"`
	Amenities    []string `json:"amenities"`
	Photos       []Photo  `json:"photos"`

	// PlaceholderPhotoURL is what to render while Photos is empty, through the
	// same availability.PlaceholderPhoto the search results use — so a room with
	// no uploaded photo looks the same wherever it appears, and the fallback
	// stays a fallback rather than becoming a seeded row somebody has to
	// remember to delete.
	PlaceholderPhotoURL string `json:"placeholderPhotoUrl"`

	FromCents *int64 `json:"fromCents,omitempty"`

	// When the owner last changed this room, for the sitemap's <lastmod>. Not
	// on the wire: it is a crawl-scheduling hint and no part of what the page
	// shows, and a timestamp on a public payload is a thing somebody eventually
	// renders.
	UpdatedAt time.Time `json:"-"`
}

// Photo mirrors the guest-side shape the results page already uses.
type Photo struct {
	URL string `json:"url"`
	Alt string `json:"alt"`

	// The other sizes it is stored at, for srcset. Derived from URL.
	media.Ladder
}

// rooms serves GET /api/rooms, the rooms index.
//
// Dateless on purpose. This is the page somebody lands on from a search engine
// before they have decided when to come, so it describes the seven rooms and
// hands each one's link the dates once the visitor has picked some.
func rooms(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := roomCards(r.Context(), q)
		if err != nil {
			serverError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// roomCards is the rooms index, as one read model.
//
// A function rather than the body of the handler above because the server-
// rendered <head> describes the same rooms (decision #3, internal/httpx/meta.go)
// and a second query assembling them slightly differently is how the document a
// crawler indexes ends up quoting a price the page does not show.
func roomCards(ctx context.Context, q *db.Queries) ([]RoomCard, error) {
	all, err := q.ListRooms(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(all))
	for _, room := range all {
		ids = append(ids, room.ID)
	}

	photos, err := q.ListPhotosForRooms(ctx, ids)
	if err != nil {
		return nil, err
	}
	byRoom := map[int64][]Photo{}
	for _, p := range photos {
		byRoom[p.RoomID] = append(byRoom[p.RoomID],
			Photo{URL: p.Path, Alt: p.AltText, Ladder: media.Sources(p.Path)})
	}

	lowest, err := q.ListLowestRates(ctx, pgtype.Date{Time: console.Today(), Valid: true})
	if err != nil {
		return nil, err
	}
	from := map[int64]int64{}
	for _, l := range lowest {
		from[l.RoomID] = int64(l.FromCents)
	}

	// A slice, never nil. The API returns [] and never null for an empty
	// list — a room with no photos crashed the results page once already.
	out := make([]RoomCard, 0, len(all))
	for _, room := range all {
		card := RoomCard{
			Slug:                room.Slug,
			Name:                room.Name,
			Description:         room.Description,
			MaxOccupancy:        int(room.MaxOccupancy),
			Amenities:           room.Amenities,
			Photos:              byRoom[room.ID],
			PlaceholderPhotoURL: availability.PlaceholderPhoto(room.Slug),
			UpdatedAt:           room.UpdatedAt,
		}
		if room.View != nil {
			card.View = *room.View
		}
		if card.Photos == nil {
			card.Photos = []Photo{}
		}
		if cents, ok := from[room.ID]; ok {
			card.FromCents = &cents
		}
		out = append(out, card)
	}
	return out, nil
}

// menu serves GET /api/menu (decision #12).
//
// Available items only. The console sees the sold-out ones so they can be
// turned back on; a guest reading the page tonight should not be shown a dish
// the kitchen has run out of.
func menu(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sections, err := ops.PublicMenu(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sections)
	}
}

// events serves GET /api/events: published, and not already past.
func events(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.PublicEvents(r.Context(), console.Today())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// pageCopy serves GET /api/copy/{slug}, the owner's prose for one page.
//
// A page nobody has written answers 200 with empty strings rather than 404: the
// page exists, it just has nothing in that slot yet, and a 404 would have the
// front end render an error where a missing paragraph belongs.
func pageCopy(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := ops.PageFor(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// attractions serves GET /api/attractions, the local-area page's nearby list.
func attractions(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.Attractions(r.Context())
		if err != nil {
			consoleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// policyTerms is the machine-readable half of the policies page.
//
// Every figure here is read from settings or from the pricing package rather
// than written into the copy, which is the whole point: the page a guest is
// asked to agree to states what the code will actually do to their money. A
// deposit rule typed into a text box drifts from `pricing.Quote` the first time
// somebody changes one and not the other, and the guest has the old one in
// writing.
type policyTerms struct {
	MinStayNights int `json:"minStayNights"`
	MaxStayNights int `json:"maxStayNights"`

	CheckinTime  string `json:"checkinTime"`
	CheckoutTime string `json:"checkoutTime"`

	HoldMinutes int `json:"holdMinutes"`

	// Percentages as written, e.g. 8.5 — formatting is the page's job, but the
	// conversion out of the scaled integer is not something a browser should be
	// doing to a tax rate.
	TaxRatePercent           string `json:"taxRatePercent"`
	RefundProcessingPercent  string `json:"refundProcessingPercent"`
	DepositPercent           int    `json:"depositPercent"`
	BalanceLeadDays          int    `json:"balanceLeadDays"`
	ShortNoticeDays          int    `json:"shortNoticeDays"`
	FreeCancellationLeadDays int    `json:"freeCancellationLeadDays"`
}

// policies serves GET /api/policies.
func policies(q *db.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		terms, err := policyTermsFor(r.Context(), q)
		if err != nil {
			serverError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, terms)
	}
}

// policyTermsFor reads the terms once, for whoever needs them: the endpoint
// above, and the server-rendered policies page, which publishes the figures to
// a reader that never calls it (prerender.go).
func policyTermsFor(ctx context.Context, q *db.Queries) (policyTerms, error) {
	s, err := q.GetSettings(ctx)
	if err != nil {
		return policyTerms{}, err
	}

	return policyTerms{
		MinStayNights: int(s.DefaultMinStay),
		MaxStayNights: int(s.MaxStayNights),
		CheckinTime:   hhmm(s.CheckinTime.Microseconds),
		CheckoutTime:  hhmm(s.CheckoutTime.Microseconds),
		HoldMinutes:   int(s.HoldTtlMinutes),

		TaxRatePercent:          pricing.Rate(s.TaxRateScaled).Percent(),
		RefundProcessingPercent: pricing.Rate(s.RefundProcessingRateScaled).Percent(),

		// Half the all-in total, rounded up — pricing.Quote's own rule.
		DepositPercent:           50,
		BalanceLeadDays:          pricing.BalanceLeadDays,
		ShortNoticeDays:          pricing.ShortNoticeDays,
		FreeCancellationLeadDays: pricing.BalanceLeadDays,
	}, nil
}

// hhmm renders a time-of-day column as "15:00". The console has its own copy
// for its form fields; this one is for the policies payload.
func hhmm(micros int64) string {
	d := time.Duration(micros) * time.Microsecond
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// maxInquiryBody bounds the two public forms.
//
// Its own limit rather than decodeBody's, which is sized for the console: a
// whole menu is half a megabyte, and a message to the inn is a few paragraphs.
// The fields themselves are capped again inside SubmitInquiry; this is what
// stops an anonymous caller making the server read half a megabyte of JSON
// before finding that out.
const maxInquiryBody = 8 << 10

// submitInquiry serves POST /api/inquiries, the events form and the contact
// form.
//
// The one write an anonymous visitor performs on this whole site apart from
// creating a booking, and unlike that one it takes no inventory off sale and
// spends nothing. What it does do is put a row in a table somebody reads and a
// notification on their phone, which is why it has its own allowance in the
// router. It answers 201 with nothing: there is no resource to hand back, and
// the page says thank you.
func submitInquiry(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.NewInquiry
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInquiryBody)).Decode(&in); err != nil {
			badRequest(w, "the message could not be read as JSON")
			return
		}

		if err := ops.SubmitInquiry(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}
