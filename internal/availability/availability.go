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
	"bealhouse/internal/media"
	"bealhouse/internal/pricing"
)

var (
	ErrCheckoutNotAfterCheckin = errors.New("availability: check-out must be after check-in")
	ErrCheckinInPast           = errors.New("availability: check-in is in the past")
	ErrGuestsOutOfRange        = errors.New("availability: at least one guest is required")

	// ErrStayTooLong means the stay is longer than the engine sells
	// (decision #27). Unlike the other three this one has somewhere to send the
	// guest — a month-plus stay is a conversation with the owner — so the API
	// says so rather than just refusing.
	ErrStayTooLong = errors.New("availability: that stay is longer than can be booked online")
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

type Photo struct {
	URL string `json:"url"`
	Alt string `json:"alt"`

	// The other sizes the same photograph is stored at, derived from URL by a
	// pure rule (media.Sources) rather than by listing a directory or carrying
	// a column. Empty for a photograph stored before the ladder existed, which
	// renders the plain URL and is exactly right.
	media.Ladder
}

// PlaceholderPhoto is the bundled stand-in for a room with no uploaded photos.
//
// Deliberately a fallback rather than seeded rows: a placeholder that lives in
// the database is one the owner has to find and delete later, and one that can
// reach the live site by being forgotten. This one disappears the moment a real
// photo exists.
func PlaceholderPhoto(slug string) string {
	return "/placeholders/" + slug + ".svg"
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
	Photos        []Photo  `json:"photos"`
	IsPetFriendly bool     `json:"isPetFriendly"`

	// PlaceholderPhotoURL is what to render while Photos is empty, so a room
	// page has the right shape before the owner has uploaded anything.
	PlaceholderPhotoURL string `json:"placeholderPhotoUrl"`

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
	// Settings first: the longest sellable stay is owner-configurable, so the
	// date checks cannot be answered without them.
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("availability: loading settings: %w", err)
	}

	if err := validate(req, int(settings.MaxStayNights)); err != nil {
		return Result{}, err
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

	ids := roomIDs(rows)

	beds, err := bedsByRoom(ctx, q, ids)
	if err != nil {
		return Result{}, err
	}
	photos, err := photosByRoom(ctx, q, ids)
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
			NightlyCents:        nightly,
			Quote: pricing.Compute(pricing.Input{
				NightlyCents: nightly,
				PetFeeCents:  petFee,
				TaxRate:      pricing.Rate(settings.TaxRateScaled),
			}),
		})
	}

	return result, nil
}

func validate(req Request, maxStayNights int) error {
	if !req.Checkout.After(req.Checkin) {
		return ErrCheckoutNotAfterCheckin
	}
	if req.Checkin.Before(civil.Today()) {
		return ErrCheckinInPast
	}
	if req.Guests < 1 {
		return ErrGuestsOutOfRange
	}
	// Checked here rather than only in the picker, for the same reason the
	// minimum stay is: a client-side calendar is a convenience, not a security
	// boundary, and booking.Create re-runs this very function.
	if maxStayNights > 0 && civil.Nights(req.Checkin, req.Checkout) > maxStayNights {
		return ErrStayTooLong
	}
	return nil
}

func roomIDs(rows []db.SearchAvailabilityRow) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func photosByRoom(ctx context.Context, q *db.Queries, ids []int64) (map[int64][]Photo, error) {
	byRoom := make(map[int64][]Photo, len(ids))
	// Seeded empty so a room with nothing uploaded yet serialises as [] rather
	// than null. Until the owner adds photos that is every room, and an API
	// that answers null for "none" makes every caller defend against it.
	for _, id := range ids {
		byRoom[id] = []Photo{}
	}
	if len(ids) == 0 {
		return byRoom, nil
	}

	photos, err := q.ListPhotosForRooms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("availability: loading photos: %w", err)
	}
	for _, p := range photos {
		// p.Path is already the served URL — media.Save returns "/media/<name>"
		// and the console stores exactly that. This used to prepend the prefix
		// a second time, which made every photograph on the search results and
		// the room page a broken image; it went unnoticed because no
		// photographs have been uploaded yet. The rooms index has always used
		// the column as it stands, which is what it is for.
		byRoom[p.RoomID] = append(byRoom[p.RoomID], Photo{
			URL:    p.Path,
			Alt:    p.AltText,
			Ladder: media.Sources(p.Path),
		})
	}
	return byRoom, nil
}

func bedsByRoom(ctx context.Context, q *db.Queries, ids []int64) (map[int64][]Bed, error) {
	byRoom := make(map[int64][]Bed, len(ids))
	for _, id := range ids {
		byRoom[id] = []Bed{}
	}
	if len(ids) == 0 {
		return byRoom, nil
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
