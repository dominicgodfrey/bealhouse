package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/rates"
)

// Season is one owner-facing pricing rule (decision #21).
//
// Dates here are INCLUSIVE — EndsOn is a night, not a checkout — because that
// is what an owner means by "Jun 1 to Aug 31", and it is deliberately the
// opposite convention from room_occupancy's half-open spans. Mixing the two up
// is the expensive mistake in this schema, so the field names say which this is
// and the screen labels it "last night".
type Season struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	StartsOn string `json:"startsOn"`
	EndsOn   string `json:"endsOn"`

	// MinStay is null when the season does not override the global default.
	// Zero would be a third state meaning nothing, and one the CHECK constraint
	// refuses anyway.
	MinStay *int `json:"minStay"`

	// Priority resolves overlaps: higher wins. Without it a Thanksgiving season
	// sitting inside a leaf-season range is undefined behaviour, and the symptom
	// is silently wrong prices.
	Priority int `json:"priority"`

	// Prices is the room half of the grid, keyed by room id. A room absent from
	// the map is one this season does not price, and its nights fall through to
	// whatever lower-priority season covers them — or to nothing, which makes
	// the room unsellable then.
	Prices map[int64]int64 `json:"prices"`
}

// RateBoard is the whole rate editor in one response: the grid, the rooms it is
// across, and the default the blank min-stay boxes fall back to.
type RateBoard struct {
	Seasons []Season   `json:"seasons"`
	Rooms   []RoomCard `json:"rooms"`

	DefaultMinStay int `json:"defaultMinStay"`
	MaxStayNights  int `json:"maxStayNights"`

	// Horizon is how far forward the calendar is currently generated. Shown
	// because the failure mode of the rebuild job stopping is silent: the
	// horizon creeps closer until a guest planning next autumn finds no price
	// and the room drops out of the search with no error anywhere.
	Horizon string `json:"horizon,omitempty"`
}

// RoomCard is the minimum of a room needed to label a column.
type RoomCard struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Rates loads the season grid.
func (o *Ops) Rates(ctx context.Context) (RateBoard, error) {
	seasons, err := o.q.ListRateSeasons(ctx)
	if err != nil {
		return RateBoard{}, fmt.Errorf("console: loading seasons: %w", err)
	}
	prices, err := o.q.ListSeasonPrices(ctx)
	if err != nil {
		return RateBoard{}, fmt.Errorf("console: loading season prices: %w", err)
	}
	rooms, err := o.q.ListRooms(ctx)
	if err != nil {
		return RateBoard{}, fmt.Errorf("console: loading rooms: %w", err)
	}
	settings, err := o.q.GetSettings(ctx)
	if err != nil {
		return RateBoard{}, fmt.Errorf("console: loading settings: %w", err)
	}

	grid := make(map[int64]map[int64]int64, len(seasons))
	for _, p := range prices {
		if grid[p.SeasonID] == nil {
			grid[p.SeasonID] = map[int64]int64{}
		}
		grid[p.SeasonID][p.RoomID] = int64(p.PriceCents)
	}

	out := RateBoard{
		Seasons:        make([]Season, 0, len(seasons)),
		Rooms:          make([]RoomCard, 0, len(rooms)),
		DefaultMinStay: int(settings.DefaultMinStay),
		MaxStayNights:  int(settings.MaxStayNights),
	}
	for _, s := range seasons {
		season := Season{
			ID:       s.ID,
			Name:     s.Name,
			StartsOn: day(s.StartsOn),
			EndsOn:   day(s.EndsOn),
			Priority: int(s.Priority),
			Prices:   grid[s.ID],
		}
		if season.Prices == nil {
			season.Prices = map[int64]int64{}
		}
		if s.MinStay != nil {
			min := int(*s.MinStay)
			season.MinStay = &min
		}
		out.Seasons = append(out.Seasons, season)
	}
	for _, r := range rooms {
		out.Rooms = append(out.Rooms, RoomCard{ID: r.ID, Slug: r.Slug, Name: r.Name})
	}

	horizon, err := o.q.GetRateHorizon(ctx)
	if err != nil {
		return RateBoard{}, fmt.Errorf("console: reading the rate horizon: %w", err)
	}
	out.Horizon = day(horizon)

	return out, nil
}

// RateChange is the diff shown before publishing (decision #21).
type RateChange struct {
	Nights int64 `json:"nights"`
	Rooms  int64 `json:"rooms"`

	// The two cases a bare "142 nights change" would hide, and the two an owner
	// most needs to see: nights that gain a price become sellable, and nights
	// that lose one silently drop the room out of every search.
	NightsGainingAPrice   int64 `json:"nightsGainingAPrice"`
	NightsLosingTheirPice int64 `json:"nightsLosingTheirPrice"`

	FirstNight string `json:"firstNight,omitempty"`
	LastNight  string `json:"lastNight,omitempty"`

	// ConfirmedBookings is how many confirmed stays fall in the affected range.
	// It is reported so the screen can say plainly that they are *not* touched:
	// their nightly prices and tax rate were snapshotted when the guest booked
	// and no rebuild can reach them. Saying nothing at all about them would
	// leave the owner to assume the worst and never publish.
	ConfirmedBookings int64 `json:"confirmedBookings"`
}

// SaveSeason is one season as the editor submits it.
type SaveSeason struct {
	// ID is zero for a new season.
	ID       int64            `json:"id"`
	Name     string           `json:"name"`
	StartsOn string           `json:"startsOn"`
	EndsOn   string           `json:"endsOn"`
	MinStay  *int             `json:"minStay"`
	Priority int              `json:"priority"`
	Prices   map[string]int64 `json:"prices"`
}

// PreviewSeason says what saving would do, without doing it.
//
// The edit is applied inside a transaction, the diff is taken against the live
// calendar, and the transaction is rolled back. That is what makes the number
// honest: it is not an estimate from a second implementation of the season
// resolution rule, it is the resolution rule itself, run against the edited
// data. Nothing else could account for a lower-priority season underneath the
// one being edited.
func (o *Ops) PreviewSeason(ctx context.Context, in SaveSeason) (RateChange, error) {
	var out RateChange
	err := o.tx(ctx, func(q *db.Queries) error {
		from, to, err := applySeason(ctx, q, in)
		if err != nil {
			return err
		}
		out, err = summarise(ctx, q, from, to)
		if err != nil {
			return err
		}
		// Never committed. The rollback is the point.
		return errPreviewDone
	})
	if err != nil && !errors.Is(err, errPreviewDone) {
		return RateChange{}, err
	}
	return out, nil
}

// errPreviewDone unwinds the preview transaction without pretending something
// failed. Ops.tx rolls back on any error, so this is how "I am finished and I
// want none of it kept" is expressed.
var errPreviewDone = errors.New("console: preview complete")

// SaveSeasonAndRebuild writes the season and regenerates the calendar behind
// it.
//
// One transaction, so the calendar can never be left priced from a season that
// was not saved, or a season saved with no prices generated from it. The
// rebuild is future-only and non-destructive by construction — the SQL function
// deletes from today forward — so it cannot re-price a confirmed stay, whose
// nightly prices and tax rate are snapshotted on its own rows.
func (o *Ops) SaveSeasonAndRebuild(ctx context.Context, in SaveSeason) (RateChange, error) {
	var out RateChange
	err := o.tx(ctx, func(q *db.Queries) error {
		from, to, err := applySeason(ctx, q, in)
		if err != nil {
			return err
		}
		if out, err = summarise(ctx, q, from, to); err != nil {
			return err
		}
		// The whole horizon, not just this season's span: raising one season's
		// priority changes which season wins on nights outside it, and a
		// rebuild scoped to the edited dates would leave those stale.
		_, err = rates.RebuildHorizon(ctx, q)
		return err
	})
	if err != nil {
		return RateChange{}, err
	}
	return out, nil
}

// DeleteSeason removes a season and regenerates the calendar without it.
//
// Nights it was the only cover for lose their price and stop being sellable,
// which is the correct outcome and is why the response carries the same diff a
// save does: an owner deleting an old season needs to see if they have just
// taken next spring off sale.
func (o *Ops) DeleteSeason(ctx context.Context, id int64) (RateChange, error) {
	var out RateChange
	err := o.tx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteRateSeason(ctx, id)
		if err != nil {
			return fmt.Errorf("console: deleting season %d: %w", id, err)
		}
		if n == 0 {
			return ErrNotFound
		}

		from, to := rebuildWindow()
		if out, err = summarise(ctx, q, from, to); err != nil {
			return err
		}
		_, err = rates.RebuildHorizon(ctx, q)
		return err
	})
	if err != nil {
		return RateChange{}, err
	}
	return out, nil
}

// Rebuild regenerates the nightly calendar on demand.
//
// The same rates.rebuild the monthly job runs. Here as a button because the job
// failing is invisible: nothing breaks on the day it stops, the horizon just
// creeps closer, and the first symptom is a guest planning next autumn finding
// no price at all.
func (o *Ops) Rebuild(ctx context.Context) (int64, error) {
	return rates.RebuildHorizon(ctx, o.q)
}

// applySeason writes one season inside an open transaction and returns the
// window a rebuild would have to cover.
func applySeason(ctx context.Context, q *db.Queries, in SaveSeason) (time.Time, time.Time, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return time.Time{}, time.Time{}, badf("the season needs a name")
	}

	starts, err := parseDay(in.StartsOn)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	ends, err := parseDay(in.EndsOn)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	// Inclusive, so a one-night season has ends == starts. The CHECK constraint
	// says the same thing; this says it in a sentence first.
	if ends.Before(starts) {
		return time.Time{}, time.Time{}, badf("the season cannot end before it starts")
	}

	var minStay *int32
	if in.MinStay != nil {
		if *in.MinStay < 1 {
			return time.Time{}, time.Time{}, badf("a minimum stay of fewer than one night is not a minimum stay")
		}
		n := int32(*in.MinStay)
		minStay = &n
	}

	id := in.ID
	if id == 0 {
		id, err = q.CreateRateSeason(ctx, db.CreateRateSeasonParams{
			Name:     name,
			StartsOn: dateOf(starts),
			EndsOn:   dateOf(ends),
			MinStay:  minStay,
			Priority: int32(in.Priority),
		})
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("console: creating season: %w", err)
		}
	} else {
		n, err := q.UpdateRateSeason(ctx, db.UpdateRateSeasonParams{
			ID:       id,
			Name:     name,
			StartsOn: dateOf(starts),
			EndsOn:   dateOf(ends),
			MinStay:  minStay,
			Priority: int32(in.Priority),
		})
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("console: updating season %d: %w", id, err)
		}
		if n == 0 {
			return time.Time{}, time.Time{}, ErrNotFound
		}
	}

	// Wholesale replacement rather than a merge, so a room the owner took out of
	// the season actually leaves it. Inside the same transaction, so there is no
	// instant at which the season has no prices.
	if err := q.ClearSeasonPrices(ctx, id); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("console: clearing season prices: %w", err)
	}
	for roomKey, cents := range in.Prices {
		roomID, err := parseID(roomKey)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		// Zero means "this season does not price this room", which is the same
		// thing as the row being absent — and the CHECK constraint refuses a
		// zero price anyway, so writing one would be a database error rather
		// than an empty box.
		if cents <= 0 {
			continue
		}
		if err := q.SetSeasonPrice(ctx, db.SetSeasonPriceParams{
			SeasonID:   id,
			RoomID:     roomID,
			PriceCents: int32(cents),
		}); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("console: pricing room %d: %w", roomID, err)
		}
	}

	from, to := rebuildWindow()
	return from, to, nil
}

// summarise reads the diff between the live calendar and what the seasons now
// in this transaction would generate.
func summarise(ctx context.Context, q *db.Queries, from, to time.Time) (RateChange, error) {
	row, err := q.SummariseRateChanges(ctx, db.SummariseRateChangesParams{
		FromDate: dateOf(from),
		ToDate:   dateOf(to),
	})
	if err != nil {
		return RateChange{}, fmt.Errorf("console: summarising rate changes: %w", err)
	}

	bookings, err := q.CountConfirmedBookingsBetween(ctx, db.CountConfirmedBookingsBetweenParams{
		FromDate: dateOf(from),
		ToDate:   dateOf(to),
	})
	if err != nil {
		return RateChange{}, fmt.Errorf("console: counting affected bookings: %w", err)
	}

	return RateChange{
		Nights:                row.Nights,
		Rooms:                 row.Rooms,
		NightsGainingAPrice:   row.NightsGainingAPrice,
		NightsLosingTheirPice: row.NightsLosingTheirPrice,
		FirstNight:            day(row.FirstNight),
		LastNight:             day(row.LastNight),
		ConfirmedBookings:     bookings,
	}, nil
}

// rebuildWindow is the span a rebuild covers: today forward to the horizon.
//
// Today rather than the season's own start, because the rebuild is future-only
// and a diff that included the past would report changes that will never be
// made.
func rebuildWindow() (time.Time, time.Time) {
	today := civil.Today()
	return today, civil.AddMonths(today, rates.HorizonMonths)
}
