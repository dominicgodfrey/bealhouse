// Package booking turns a room the guest has chosen into a held reservation.
//
// A hold is not a separate mechanism: it is a row in room_occupancy with an
// expiry, written in the same transaction as the booking it belongs to. A guest
// partway through checkout genuinely owns the room, and the exclusion
// constraint is what stops a second guest from owning it too.
//
// No Stripe. Payment is build-order step 4; everything here works without an
// account, and a hold that is never paid for is reclaimed by the sweeper.
package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/availability"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
	"bealhouse/internal/pricing"
)

var (
	// ErrRoomUnavailable means the room did not survive the server's own
	// availability check. It covers every way that can happen — occupied,
	// too small, unpriced, under the minimum stay — because the guest can do
	// exactly one thing about all of them, and enumerating them to an
	// anonymous caller only helps someone probing the calendar.
	ErrRoomUnavailable = errors.New("booking: that room is not available for those dates")

	// ErrPriceChanged means the total the guest was shown is no longer the
	// total the server computes. Nobody is charged a price they did not see.
	ErrPriceChanged = errors.New("booking: the price has changed since the quote")

	ErrGuestNameRequired  = errors.New("booking: a name is required")
	ErrGuestEmailRequired = errors.New("booking: an email address is required")

	// ErrNotFound is an unknown booking code.
	ErrNotFound = errors.New("booking: no such booking")
)

// IsUnavailable reports the two ways a guest can be told "not that room, not
// those dates", which are worth separating internally and not worth
// distinguishing to the person staring at the confirm button.
//
// ErrRoomUnavailable means the room was already gone when we looked.
// ErrRoomTaken means it went while this very request was being written, and the
// exclusion constraint caught it. The first is a search result, the second is a
// race, and both mean "pick again".
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrRoomUnavailable) || errors.Is(err, occupancy.ErrRoomTaken)
}

// Beginner is anything that can start a transaction: a *pgxpool.Pool in
// production, and in tests an already-open transaction, whose nested Begin is a
// savepoint. That is what lets these tests roll back cleanly like the rest of
// the suite.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Guest is who the stay is for. Deliberately the minimum: no account, no
// password, no address (decision #11).
type Guest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone,omitempty"`
}

// Request is a booking as the confirm page submits it.
type Request struct {
	RoomSlug string
	Checkin  time.Time
	Checkout time.Time
	Guests   int
	WithPet  bool
	Guest    Guest

	// ExpectedTotalCents is the all-in total the guest was shown. Zero skips
	// the check.
	//
	// This is not the price validation — the server computes the price itself
	// and never trusts this number. It is the agreement check: if the two
	// disagree, something changed between the quote and the confirm, and the
	// booking should fail loudly rather than charge a surprise.
	ExpectedTotalCents int64
}

// Room is a booked room as the confirmation shows it.
type Room struct {
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	View         string  `json:"view,omitempty"`
	Checkin      string  `json:"checkin"`
	Checkout     string  `json:"checkout"`
	NightlyCents []int64 `json:"nightlyCents"`
}

// Booking is a held reservation as the API returns it.
type Booking struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Checkin  string `json:"checkin"`
	Checkout string `json:"checkout"`
	Nights   int    `json:"nights"`
	Guests   int    `json:"guests"`
	WithPet  bool   `json:"withPet"`
	Rooms    []Room `json:"rooms"`

	// Quote is read back from the booking's own snapshot, never recomputed:
	// what the guest owes was settled when they booked.
	Quote pricing.Quote `json:"quote"`

	// ChargeNowCents is the deposit, or the whole total for a short-notice
	// arrival (decisions #6 and #7). Step 4 collects it; step 3 only says what
	// it will be.
	ChargeNowCents int64 `json:"chargeNowCents"`

	// BalanceChargeOn is empty when the stay is charged in full at booking,
	// which is the same thing the NULL column means.
	BalanceChargeOn string `json:"balanceChargeOn,omitempty"`

	// HoldExpiresAt drives the countdown. Nil means no hold is outstanding:
	// either it lapsed and the sweeper took the room back, or the booking has
	// moved past needing one.
	HoldExpiresAt *time.Time `json:"holdExpiresAt,omitempty"`

	// Guest is populated only on the response to creating a booking, where the
	// caller has just supplied it. Looking a booking up by code deliberately
	// does not return it — a code is an identifier, not an authenticator, and
	// contact details wait for the signed link in decision #19.
	Guest *Guest `json:"guest,omitempty"`
}

// Create books a room and holds it, in one transaction.
//
// Every rule the search applies is applied again here, by running the same
// query rather than a second implementation of it: capacity, pets, occupancy,
// full rate coverage, and the minimum stay. A hand-crafted one-night payload
// fails here even though the date picker would never have offered it.
//
// The check is not what makes the room ours — a concurrent booker can pass the
// same check a moment later. The exclusion constraint decides, at the insert,
// and its loser gets ErrRoomTaken.
func Create(ctx context.Context, beginner Beginner, req Request) (Booking, error) {
	if err := req.validate(); err != nil {
		return Booking{}, err
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return Booking{}, fmt.Errorf("booking: beginning transaction: %w", err)
	}
	// WithoutCancel so an abandoned request still releases its locks rather
	// than leaving the transaction to time out.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := db.New(tx)

	settings, err := q.GetSettings(ctx)
	if err != nil {
		return Booking{}, fmt.Errorf("booking: loading settings: %w", err)
	}

	found, err := availability.Search(ctx, q, availability.Request{
		Checkin:  req.Checkin,
		Checkout: req.Checkout,
		Guests:   req.Guests,
		WithPet:  req.WithPet,
	})
	if err != nil {
		return Booking{}, err
	}

	room, ok := pick(found, req.RoomSlug)
	if !ok {
		return Booking{}, ErrRoomUnavailable
	}
	if req.ExpectedTotalCents != 0 && req.ExpectedTotalCents != room.Quote.TotalCents {
		return Booking{}, ErrPriceChanged
	}

	guestID, err := q.UpsertGuest(ctx, db.UpsertGuestParams{
		Email: strings.TrimSpace(req.Guest.Email),
		Name:  strings.TrimSpace(req.Guest.Name),
		Phone: strings.TrimSpace(req.Guest.Phone),
	})
	if err != nil {
		return Booking{}, fmt.Errorf("booking: recording the guest: %w", err)
	}

	shortNotice := pricing.IsShortNotice(civil.Today(), req.Checkin)
	chargeAt := pgtype.Date{}
	if !shortNotice {
		chargeAt = pgtype.Date{Time: pricing.BalanceChargeDate(req.Checkin), Valid: true}
	}

	code, bookingID, err := insertBooking(ctx, q, req, room, settings, guestID, chargeAt)
	if err != nil {
		return Booking{}, err
	}

	nightly, err := json.Marshal(room.NightlyCents)
	if err != nil {
		return Booking{}, fmt.Errorf("booking: encoding nightly prices: %w", err)
	}
	if err := q.CreateBookingRoom(ctx, db.CreateBookingRoomParams{
		BookingID:     bookingID,
		RoomID:        room.ID,
		Checkin:       pgtype.Date{Time: req.Checkin, Valid: true},
		Checkout:      pgtype.Date{Time: req.Checkout, Valid: true},
		NightlyPrices: nightly,
	}); err != nil {
		return Booking{}, fmt.Errorf("booking: recording the room: %w", err)
	}

	expires := time.Now().Add(time.Duration(settings.HoldTtlMinutes) * time.Minute)
	if _, err := occupancy.Create(ctx, q, db.CreateOccupancyParams{
		RoomID:    room.ID,
		Checkin:   pgtype.Date{Time: req.Checkin, Valid: true},
		Checkout:  pgtype.Date{Time: req.Checkout, Valid: true},
		Kind:      "hold",
		Source:    "direct",
		BookingID: &bookingID,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		// ErrRoomTaken passes through untouched: losing this race is an
		// ordinary outcome, and the caller has something specific to say
		// about it.
		return Booking{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Booking{}, occupancy.Translate(err)
	}

	out := view(req, room, code, expires)
	out.Guest = &req.Guest
	return out, nil
}

// Get reads a booking back by its code.
//
// Everything comes from the stored snapshot rather than being recomputed, so a
// season edited after the booking was made cannot change what this reports.
func Get(ctx context.Context, q *db.Queries, code string) (Booking, error) {
	row, err := q.GetBookingByCode(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("booking: loading %q: %w", code, err)
	}

	rooms, err := q.ListBookingRooms(ctx, row.ID)
	if err != nil {
		return Booking{}, fmt.Errorf("booking: loading rooms: %w", err)
	}

	out := Booking{
		Code:     row.Code,
		Status:   row.Status,
		Checkin:  row.Checkin.Time.Format(time.DateOnly),
		Checkout: row.Checkout.Time.Format(time.DateOnly),
		Nights:   civil.Nights(row.Checkin.Time, row.Checkout.Time),
		Guests:   int(row.Guests),
		WithPet:  row.WithPet,
		Rooms:    make([]Room, 0, len(rooms)),
		Quote: pricing.Quote{
			Nights:            civil.Nights(row.Checkin.Time, row.Checkout.Time),
			RoomSubtotalCents: row.RoomSubtotalCents,
			PetFeeCents:       row.PetFeeCents,
			TaxableCents:      row.RoomSubtotalCents + row.PetFeeCents,
			TaxCents:          row.TaxCents,
			TotalCents:        row.TotalCents,
			DepositCents:      row.DepositCents,
			BalanceCents:      row.BalanceDueCents,
		},
	}

	// A NULL balance_charge_at is the short-notice flag, so the amount due at
	// booking follows from the column rather than from re-deriving the window
	// against today's date — which would give a different answer once the
	// arrival got closer.
	out.ChargeNowCents = out.Quote.ChargeAtBooking(!row.BalanceChargeAt.Valid)
	if row.BalanceChargeAt.Valid {
		out.BalanceChargeOn = row.BalanceChargeAt.Time.Format(time.DateOnly)
	}
	if row.HoldExpiresAt.Valid {
		expires := row.HoldExpiresAt.Time
		out.HoldExpiresAt = &expires
	}

	for _, r := range rooms {
		var nightly []int64
		if err := json.Unmarshal(r.NightlyPrices, &nightly); err != nil {
			return Booking{}, fmt.Errorf("booking: decoding nightly prices: %w", err)
		}
		out.Rooms = append(out.Rooms, Room{
			Slug:         r.Slug,
			Name:         r.Name,
			View:         derefString(r.View),
			Checkin:      r.Checkin.Time.Format(time.DateOnly),
			Checkout:     r.Checkout.Time.Format(time.DateOnly),
			NightlyCents: nightly,
		})
	}

	return out, nil
}

func insertBooking(
	ctx context.Context,
	q *db.Queries,
	req Request,
	room availability.Room,
	settings db.GetSettingsRow,
	guestID int64,
	chargeAt pgtype.Date,
) (string, int64, error) {
	// A code collision is a 1-in-a-billion event, so retrying beats widening
	// the code or asking the guest to submit again.
	var err error
	for range codeAttempts {
		var code string
		code, err = newCode()
		if err != nil {
			return "", 0, err
		}

		var id int64
		id, err = q.CreateBooking(ctx, db.CreateBookingParams{
			Code:              code,
			GuestID:           guestID,
			Status:            StatusPending,
			Checkin:           pgtype.Date{Time: req.Checkin, Valid: true},
			Checkout:          pgtype.Date{Time: req.Checkout, Valid: true},
			Guests:            int32(req.Guests),
			WithPet:           req.WithPet,
			RoomSubtotalCents: room.Quote.RoomSubtotalCents,
			PetFeeCents:       room.Quote.PetFeeCents,
			TaxCents:          room.Quote.TaxCents,
			TaxRateScaled:     settings.TaxRateScaled,
			TotalCents:        room.Quote.TotalCents,
			DepositCents:      room.Quote.DepositCents,
			BalanceDueCents:   room.Quote.BalanceCents,
			BalanceChargeAt:   chargeAt,
		})
		if err == nil {
			return code, id, nil
		}
		if !isDuplicateCode(err) {
			return "", 0, fmt.Errorf("booking: recording the booking: %w", err)
		}
	}
	return "", 0, fmt.Errorf("booking: could not allocate a unique code: %w", err)
}

// view assembles the response for a booking just created, from the same values
// that were written.
func view(req Request, room availability.Room, code string, expires time.Time) Booking {
	shortNotice := pricing.IsShortNotice(civil.Today(), req.Checkin)

	out := Booking{
		Code:     code,
		Status:   StatusPending,
		Checkin:  req.Checkin.Format(time.DateOnly),
		Checkout: req.Checkout.Format(time.DateOnly),
		Nights:   civil.Nights(req.Checkin, req.Checkout),
		Guests:   req.Guests,
		WithPet:  req.WithPet,
		Rooms: []Room{{
			Slug:         room.Slug,
			Name:         room.Name,
			View:         room.View,
			Checkin:      req.Checkin.Format(time.DateOnly),
			Checkout:     req.Checkout.Format(time.DateOnly),
			NightlyCents: room.NightlyCents,
		}},
		Quote:          room.Quote,
		ChargeNowCents: room.Quote.ChargeAtBooking(shortNotice),
		HoldExpiresAt:  &expires,
	}
	if !shortNotice {
		out.BalanceChargeOn = pricing.BalanceChargeDate(req.Checkin).Format(time.DateOnly)
	}
	return out
}

func (r Request) validate() error {
	if strings.TrimSpace(r.Guest.Name) == "" {
		return ErrGuestNameRequired
	}
	// Deliberately not a full address check: the only test that means anything
	// is whether the confirmation arrives, and that is step 5's job. This
	// catches the empty box and the obvious typo.
	email := strings.TrimSpace(r.Guest.Email)
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return ErrGuestEmailRequired
	}
	return nil
}

func pick(res availability.Result, slug string) (availability.Room, bool) {
	for _, room := range res.Rooms {
		if room.Slug == slug {
			return room, true
		}
	}
	return availability.Room{}, false
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
