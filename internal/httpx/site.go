package httpx

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/availability"
	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
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

	IsPetFriendly bool  `json:"isPetFriendly"`
	PetFeeCents   int64 `json:"petFeeCents,omitempty"`

	FromCents *int64 `json:"fromCents,omitempty"`
}

// Photo mirrors the guest-side shape the results page already uses.
type Photo struct {
	URL string `json:"url"`
	Alt string `json:"alt"`
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
		byRoom[p.RoomID] = append(byRoom[p.RoomID], Photo{URL: p.Path, Alt: p.AltText})
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
			IsPetFriendly:       room.IsPetFriendly,
			PetFeeCents:         int64(room.PetFeeCents),
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

// submitInquiry serves POST /api/inquiries, the events form.
//
// The one write an anonymous visitor performs on this whole site apart from
// creating a booking, and unlike that one it takes no inventory off sale and
// spends nothing — so it shares the readers' allowance rather than needing its
// own. What it does do is put a row in a table somebody reads, which is why it
// answers 201 with nothing: there is no resource to hand back, and the page
// says thank you.
func submitInquiry(ops *console.Ops) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in console.NewInquiry
		if err := decodeBody(w, r, &in); err != nil {
			consoleError(w, r, err)
			return
		}

		if err := ops.SubmitInquiry(r.Context(), in); err != nil {
			consoleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}
