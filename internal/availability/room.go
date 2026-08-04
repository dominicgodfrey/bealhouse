package availability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
)

// ErrRoomNotFound is an unknown slug.
var ErrRoomNotFound = errors.New("availability: no such room")

// Detail is a room page: the content the owner manages, plus — when the guest
// arrived carrying dates — whether it is actually free then and what it costs.
//
// The two halves are deliberately separable. A room page reached from the
// marketing site has no dates and should still describe the room; one reached
// from search results has them and should not make the guest re-enter them.
type Detail struct {
	Room

	// Accessibility is reported rather than filtered on (decision #22). Every
	// room requires stairs, so no room sets the flag, and the notice is what a
	// guest with mobility needs actually has to read.
	IsAccessible          bool     `json:"isAccessible"`
	AccessibilityFeatures []string `json:"accessibilityFeatures"`
	AccessibilityNotice   string   `json:"accessibilityNotice"`
	PetFeeCentsPerStay    int64    `json:"petFeeCentsPerStay,omitempty"`

	// HasDates says whether the fields below mean anything. Without it an
	// unavailable room and a room nobody asked about look identical.
	HasDates bool `json:"hasDates"`

	Available bool   `json:"available"`
	Checkin   string `json:"checkin,omitempty"`
	Checkout  string `json:"checkout,omitempty"`
	Nights    int    `json:"nights,omitempty"`
	Guests    int    `json:"guests,omitempty"`
	WithPet   bool   `json:"withPet,omitempty"`
}

// Lookup builds a room page. Pass a nil request for the dateless version.
//
// Availability is answered by running the ordinary search and looking for this
// room in the results, rather than by a second query that asks the question a
// slightly different way. The room page and the results page can then never
// disagree about whether something is for sale.
func Lookup(ctx context.Context, q *db.Queries, slug string, req *Request) (Detail, error) {
	row, err := q.GetRoomBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrRoomNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("availability: loading room %q: %w", slug, err)
	}

	settings, err := q.GetSettings(ctx)
	if err != nil {
		return Detail{}, fmt.Errorf("availability: loading settings: %w", err)
	}

	ids := []int64{row.ID}
	beds, err := bedsByRoom(ctx, q, ids)
	if err != nil {
		return Detail{}, err
	}
	photos, err := photosByRoom(ctx, q, ids)
	if err != nil {
		return Detail{}, err
	}

	detail := Detail{
		Room: Room{
			ID:                  row.ID,
			Slug:                row.Slug,
			Name:                row.Name,
			Description:         row.Description,
			View:                derefString(row.View),
			MaxOccupancy:        int(row.MaxOccupancy),
			Amenities:           row.Amenities,
			Beds:                beds[row.ID],
			Photos:              photos[row.ID],
			PlaceholderPhotoURL: PlaceholderPhoto(row.Slug),
			IsPetFriendly:       row.IsPetFriendly,
			NightlyCents:        []int64{},
		},
		IsAccessible:          row.IsAccessible,
		AccessibilityFeatures: row.AccessibilityFeatures,
		AccessibilityNotice:   settings.AccessibilityNotice,
	}
	if row.IsPetFriendly {
		detail.PetFeeCentsPerStay = int64(row.PetFeeCents)
	}

	if req == nil {
		return detail, nil
	}

	detail.HasDates = true
	detail.Checkin = req.Checkin.Format(time.DateOnly)
	detail.Checkout = req.Checkout.Format(time.DateOnly)
	detail.Nights = civil.Nights(req.Checkin, req.Checkout)
	detail.Guests = req.Guests
	detail.WithPet = req.WithPet

	res, err := Search(ctx, q, *req)
	if err != nil {
		return Detail{}, err
	}
	for _, offered := range res.Rooms {
		if offered.Slug != slug {
			continue
		}
		// The priced version of the room wins: same content, plus the quote.
		offered.Beds = detail.Room.Beds
		offered.Photos = detail.Room.Photos
		detail.Room = offered
		detail.Available = true
		break
	}

	return detail, nil
}
