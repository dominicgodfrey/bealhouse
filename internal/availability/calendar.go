package availability

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/rates"
)

// DefaultCalendarMonths is how much of the calendar a picker asks for when it
// does not say. A year covers any stay a guest is realistically planning while
// keeping the response small enough to fetch on page load.
const DefaultCalendarMonths = 12

// CalendarRequest is a window of the calendar, filtered the same way a search
// is. The filters matter: a picker that greys dates for the whole property
// would happily offer a party of four a date only the two-person rooms are free
// on.
type CalendarRequest struct {
	From    time.Time
	To      time.Time
	Guests  int
	WithPet bool
}

// Span is an unbroken run of sellable nights in one room.
//
// MinStays holds the minimum stay in force on each night of the run, in order,
// so its length is the number of nights and Start plus that length is the last
// checkout the run allows. Encoding it this way is what lets the picker answer
// "can the guest leave on this date" without another round trip.
type Span struct {
	Start    string `json:"start"`
	MinStays []int  `json:"minStays"`
}

// RoomSpans is one room's sellable calendar.
type RoomSpans struct {
	Slug  string `json:"slug"`
	Spans []Span `json:"spans"`
}

// Calendar is what the date picker greys dates from.
//
// Per room rather than for the property as a whole, because with seven rooms
// the union of free nights is not a set of bookable stays: room A can be free
// early and room B free late with no single room covering the span between
// them. A picker built on the union would let a guest select that range and
// then find nothing for sale (decision #14).
type Calendar struct {
	From    string      `json:"from"`
	To      string      `json:"to"`
	Guests  int         `json:"guests"`
	WithPet bool        `json:"withPet"`
	Rooms   []RoomSpans `json:"rooms"`
}

// Spans returns the sellable spans for a window.
//
// The window is clamped rather than rejected: a picker showing the current
// month legitimately asks for dates that have already passed, and there is
// nothing beyond the generated rate horizon to describe.
func Spans(ctx context.Context, q *db.Queries, req CalendarRequest) (Calendar, error) {
	today := civil.Today()
	from, to := clamp(req.From, req.To, today)

	guests := req.Guests
	if guests < 1 {
		guests = 1
	}

	rows, err := q.ListSellableNights(ctx, db.ListSellableNightsParams{
		FromDate: pgtype.Date{Time: from, Valid: true},
		ToDate:   pgtype.Date{Time: to, Valid: true},
		Guests:   int32(guests),
		WithPet:  req.WithPet,
	})
	if err != nil {
		return Calendar{}, fmt.Errorf("availability: listing sellable nights: %w", err)
	}

	return Calendar{
		From:    from.Format(time.DateOnly),
		To:      to.Format(time.DateOnly),
		Guests:  guests,
		WithPet: req.WithPet,
		Rooms:   group(rows),
	}, nil
}

// group stitches the ordered nights into runs, breaking a run whenever the room
// changes or a night is missing — a night is missing exactly when it is
// occupied or unpriced, which is what makes a run a genuinely bookable stretch
// rather than a convenient summary.
func group(rows []db.ListSellableNightsRow) []RoomSpans {
	out := make([]RoomSpans, 0, 8)

	var (
		roomIdx = -1
		prev    time.Time
	)
	for _, row := range rows {
		night := row.Date.Time

		newRoom := roomIdx < 0 || out[roomIdx].Slug != row.Slug
		if newRoom {
			out = append(out, RoomSpans{Slug: row.Slug, Spans: []Span{}})
			roomIdx = len(out) - 1
		}

		room := &out[roomIdx]
		if newRoom || !night.Equal(civil.AddDays(prev, 1)) {
			room.Spans = append(room.Spans, Span{
				Start:    night.Format(time.DateOnly),
				MinStays: []int{},
			})
		}

		span := &room.Spans[len(room.Spans)-1]
		span.MinStays = append(span.MinStays, int(row.MinStay))
		prev = night
	}

	return out
}

// clamp holds the window inside what the calendar can honestly describe: no
// earlier than today, no later than the generated rate horizon, and never
// inverted.
func clamp(from, to, today time.Time) (time.Time, time.Time) {
	if from.IsZero() || from.Before(today) {
		from = today
	}

	horizon := civil.AddMonths(today, rates.HorizonMonths)
	if to.IsZero() {
		to = civil.AddMonths(from, DefaultCalendarMonths)
	}
	if to.After(horizon) {
		to = horizon
	}
	if !to.After(from) {
		to = civil.AddDays(from, 1)
	}
	return from, to
}
