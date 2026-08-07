package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/occupancy"
)

// Occupancy is one thing sitting in one room over one span, whatever put it
// there.
//
// Bookings, holds and blocks come back to the console together because they are
// stored together — that is the whole architectural bet — and because the grid
// has to draw all three. The owner needs to know a room is unsellable, and
// then, immediately, *why*: somebody paid, somebody is halfway through a card
// form, or they blocked it themselves last month and forgot.
type Occupancy struct {
	ID     int64 `json:"id"`
	RoomID int64 `json:"roomId"`

	// Half-open, like the daterange it came from: EndsOn is the checkout, not a
	// night. The grid shades [StartsOn, EndsOn), so a turnover shows one room
	// changing hands on a day rather than two rooms overlapping on it.
	StartsOn string `json:"startsOn"`
	EndsOn   string `json:"endsOn"`

	Kind   string `json:"kind"`
	Source string `json:"source"`

	// Reason is the owner's own note on a block. Empty on everything else.
	Reason string `json:"reason,omitempty"`

	// ExpiresAt is set on holds alone, and is why a hold is drawn differently: a
	// room shaded by one is a room that is probably about to come back.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	BookingCode   string `json:"bookingCode,omitempty"`
	BookingStatus string `json:"bookingStatus,omitempty"`
	GuestName     string `json:"guestName,omitempty"`
}

// CalendarRoom is one row of the grid.
type CalendarRoom struct {
	ID        int64       `json:"id"`
	Slug      string      `json:"slug"`
	Name      string      `json:"name"`
	Occupancy []Occupancy `json:"occupancy"`
}

// Calendar is the 7-row × date-column grid.
type Calendar struct {
	From  string         `json:"from"`
	To    string         `json:"to"`
	Rooms []CalendarRoom `json:"rooms"`
}

// maxCalendarWindow bounds what one request can ask for.
//
// Not for the server's sake — a year of seven rooms is a few hundred rows — but
// because the grid renders a column per night, and a request for a decade is a
// phone browser laying out four thousand columns and locking up. The screen
// pages by month; this is the backstop under it.
const maxCalendarWindow = 400 * 24 * time.Hour

// Grid reads every occupancy row touching a window, grouped by room.
//
// Rooms with nothing in them come back as empty rows rather than being left
// out. A calendar is a shape the eye reads across, and a room silently missing
// from it is a room the owner stops thinking about — which is the one that was
// free the whole time.
func (o *Ops) Grid(ctx context.Context, from, to time.Time) (Calendar, error) {
	if !to.After(from) {
		return Calendar{}, badf("the calendar has to end after it starts")
	}
	if to.Sub(from) > maxCalendarWindow {
		return Calendar{}, badf("that is more calendar than one request can carry; ask for a shorter range")
	}

	rooms, err := o.q.ListRooms(ctx)
	if err != nil {
		return Calendar{}, fmt.Errorf("console: loading rooms: %w", err)
	}

	rows, err := o.q.ListOccupancyBetween(ctx, db.ListOccupancyBetweenParams{
		FromDate: dateOf(from),
		ToDate:   dateOf(to),
	})
	if err != nil {
		return Calendar{}, fmt.Errorf("console: loading occupancy: %w", err)
	}

	byRoom := make(map[int64][]Occupancy, len(rooms))
	for _, r := range rows {
		byRoom[r.RoomID] = append(byRoom[r.RoomID], Occupancy{
			ID:            r.ID,
			RoomID:        r.RoomID,
			StartsOn:      day(r.StartsOn),
			EndsOn:        day(r.EndsOn),
			Kind:          r.Kind,
			Source:        r.Source,
			Reason:        r.Reason,
			ExpiresAt:     instant(r.ExpiresAt),
			BookingCode:   r.BookingCode,
			BookingStatus: r.BookingStatus,
			GuestName:     r.GuestName,
		})
	}

	out := Calendar{
		From:  from.Format(time.DateOnly),
		To:    to.Format(time.DateOnly),
		Rooms: make([]CalendarRoom, 0, len(rooms)),
	}
	for _, room := range rooms {
		spans := byRoom[room.ID]
		if spans == nil {
			spans = []Occupancy{}
		}
		out.Rooms = append(out.Rooms, CalendarRoom{
			ID:        room.ID,
			Slug:      room.Slug,
			Name:      room.Name,
			Occupancy: spans,
		})
	}
	return out, nil
}

// NewBlock is the owner painting a room out of the calendar.
type NewBlock struct {
	RoomID int64  `json:"roomId"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Block takes a room off sale for a span.
//
// Through occupancy.Create, never q.CreateOccupancy — so it takes the per-room
// advisory lock and gets ErrRoomTaken rather than a raw 23P01 if it collides.
// That collision is a real case here and not a theoretical one: the owner
// blocking next weekend for a family visit at the same moment a guest is paying
// for it is exactly the race the exclusion constraint exists for, and the owner
// has to be told the room is spoken for rather than shown a database error.
//
// The dates are half-open like every other span in this system: To is the day
// the room comes back on sale, not the last night blocked. The screen says so.
func (o *Ops) Block(ctx context.Context, in NewBlock) (int64, error) {
	from, err := parseDay(in.From)
	if err != nil {
		return 0, err
	}
	to, err := parseDay(in.To)
	if err != nil {
		return 0, err
	}
	if !to.After(from) {
		return 0, badf("a block has to cover at least one night")
	}
	if in.RoomID == 0 {
		return 0, badf("which room?")
	}

	id, err := occupancy.Create(ctx, o.q, db.CreateOccupancyParams{
		RoomID:   in.RoomID,
		Checkin:  dateOf(from),
		Checkout: dateOf(to),
		Kind:     "block",
		Source:   "direct",
		Reason:   strings.TrimSpace(in.Reason),
		// No expiry: a block is the owner's decision and stands until they undo
		// it. The CHECK constraint asserts that correspondence — only a hold
		// carries an expiry — so getting this wrong is a database error rather
		// than a room that quietly comes back on sale.
		ExpiresAt: pgtype.Timestamptz{},
	})
	if errors.Is(err, occupancy.ErrRoomTaken) {
		return 0, badf("something already has that room for part of those dates")
	}
	if err != nil {
		return 0, fmt.Errorf("console: blocking room %d: %w", in.RoomID, err)
	}
	return id, nil
}

// Unblock puts a blocked room back on sale.
//
// The kind is checked in SQL rather than here, so an id naming a confirmed
// booking's occupancy row matches nothing instead of releasing a room with a
// guest still arriving. No rows affected is the refusal, and it is reported as
// "not found" because from the owner's side there is no block by that id.
func (o *Ops) Unblock(ctx context.Context, id int64) error {
	n, err := o.q.DeleteBlock(ctx, id)
	if err != nil {
		return fmt.Errorf("console: removing block %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DefaultCalendarWindow is what the grid opens on when the caller names no
// dates: from today, a month out. A month fits a phone screen scrolled
// sideways, and "today" is where an owner starts.
func DefaultCalendarWindow() (time.Time, time.Time) {
	today := civil.Today()
	return today, civil.AddMonths(today, 1)
}
