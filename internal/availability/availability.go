// Package availability answers the only question the booking engine really
// asks: which rooms can this guest actually book for these dates, and what will
// they cost all-in.
package availability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/pricing"
)

var (
	ErrCheckoutNotAfterCheckin = errors.New("availability: check-out must be after check-in")
	ErrCheckinInPast           = errors.New("availability: check-in is in the past")
	ErrGuestsOutOfRange        = errors.New("availability: at least one guest is required")
)

// Request is a search as the guest expressed it.
type Request struct {
	Checkin  time.Time
	Checkout time.Time
	Guests   int

	// WithPet does double duty, matching how the search form reads: it
	// restricts results to pet-friendly rooms and adds the pet fee to their
	// quotes. Leaving it false does not hide pet-friendly rooms — Back Lavender
	// is an ordinary room that happens to accept pets.
	WithPet bool
}

type Bed struct {
	Type     string `json:"type"`
	Count    int    `json:"count"`
	Location string `json:"location,omitempty"`
}

// Room is one sellable result, priced.
type Room struct {
	ID            int64    `json:"id"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	View          string   `json:"view,omitempty"`
	MaxOccupancy  int      `json:"maxOccupancy"`
	Amenities     []string `json:"amenities"`
	Beds          []Bed    `json:"beds"`
	IsPetFriendly bool     `json:"isPetFriendly"`

	// NightlyCents is the per-night breakdown, in order, so the UI can show
	// what a stay is actually made of rather than one opaque total.
	NightlyCents []int64 `json:"nightlyCents"`

	// Quote keeps the pet fee as its own field rather than folding it into the
	// subtotal, so the price preview can show the guest exactly what the extra
	// $50 is for.
	Quote pricing.Quote `json:"quote"`
}

// Result is a whole search.
type Result struct {
	Checkin  string `json:"checkin"`
	Checkout string `json:"checkout"`
	Nights   int    `json:"nights"`
	Guests   int    `json:"guests"`
	WithPet  bool   `json:"withPet"`
	Rooms    []Room `json:"rooms"`

	// Shown alongside results because every room requires stairs and a guest
	// with mobility needs should learn that here, not on arrival.
	AccessibilityNotice string `json:"accessibilityNotice"`
}

// Search returns the sellable rooms for a request, priced.
//
// Validation happens here and is repeated on the booking endpoint: a date
// picker that greys out impossible selections is a convenience, not a security
// boundary.
func Search(ctx context.Context, q *db.Queries, req Request) (Result, error) {
	if err := validate(req); err != nil {
		return Result{}, err
	}

	settings, err := q.GetSettings(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("availability: loading settings: %w", err)
	}

	rows, err := q.SearchAvailability(ctx, db.SearchAvailabilityParams{
		Checkin:  pgtype.Date{Time: req.Checkin, Valid: true},
		Checkout: pgtype.Date{Time: req.Checkout, Valid: true},
		Guests:   int32(req.Guests),
		WithPet:  req.WithPet,
	})
	if err != nil {
		return Result{}, fmt.Errorf("availability: searching: %w", err)
	}

	beds, err := bedsByRoom(ctx, q, rows)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Checkin:             req.Checkin.Format(time.DateOnly),
		Checkout:            req.Checkout.Format(time.DateOnly),
		Nights:              civil.Nights(req.Checkin, req.Checkout),
		Guests:              req.Guests,
		WithPet:             req.WithPet,
		Rooms:               make([]Room, 0, len(rows)),
		AccessibilityNotice: settings.AccessibilityNotice,
	}

	for _, row := range rows {
		nightly := make([]int64, len(row.NightlyPrices))
		for i, cents := range row.NightlyPrices {
			nightly[i] = int64(cents)
		}

		// The fee applies only when the guest said they are bringing a pet. A
		// pet-friendly room shown to everyone else is priced as an ordinary
		// room.
		var petFee int64
		if req.WithPet && row.IsPetFriendly {
			petFee = int64(row.PetFeeCents)
		}

		result.Rooms = append(result.Rooms, Room{
			ID:            row.ID,
			Slug:          row.Slug,
			Name:          row.Name,
			Description:   row.Description,
			View:          derefString(row.View),
			MaxOccupancy:  int(row.MaxOccupancy),
			Amenities:     row.Amenities,
			Beds:          beds[row.ID],
			IsPetFriendly: row.IsPetFriendly,
			NightlyCents:  nightly,
			Quote: pricing.Compute(pricing.Input{
				NightlyCents: nightly,
				PetFeeCents:  petFee,
				TaxRate:      pricing.Rate(settings.TaxRateScaled),
			}),
		})
	}

	return result, nil
}

func validate(req Request) error {
	if !req.Checkout.After(req.Checkin) {
		return ErrCheckoutNotAfterCheckin
	}
	if req.Checkin.Before(civil.Today()) {
		return ErrCheckinInPast
	}
	if req.Guests < 1 {
		return ErrGuestsOutOfRange
	}
	return nil
}

func bedsByRoom(ctx context.Context, q *db.Queries, rows []db.SearchAvailabilityRow) (map[int64][]Bed, error) {
	byRoom := make(map[int64][]Bed, len(rows))
	if len(rows) == 0 {
		return byRoom, nil
	}

	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	beds, err := q.ListBedsForRooms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("availability: loading beds: %w", err)
	}
	for _, b := range beds {
		byRoom[b.RoomID] = append(byRoom[b.RoomID], Bed{
			Type:     b.BedType,
			Count:    int(b.Count),
			Location: b.Location,
		})
	}
	return byRoom, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
