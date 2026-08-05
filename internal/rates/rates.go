// Package rates turns the owner's seasons into the nightly calendar that
// availability reads.
//
// Two layers, deliberately: the owner edits seasons and a room x season price
// grid, and a generated calendar answers "what does this room cost on this
// night" in one index lookup. The owner never edits the calendar directly.
package rates

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
)

// HorizonMonths is how far ahead the calendar is generated. Long enough that a
// guest can book next season, short enough that the table stays small: seven
// rooms over two years is about five thousand rows.
const HorizonMonths = 24

// Rebuild regenerates the calendar for [from, to], leaving earlier nights
// untouched.
//
// It cannot change what a guest already owes. Confirmed bookings snapshot their
// nightly prices and tax rate at the moment of booking, so editing a season and
// rebuilding never re-prices a stay that has already been sold.
func Rebuild(ctx context.Context, q *db.Queries, from, to time.Time) (int64, error) {
	return q.RebuildRateCalendar(ctx, db.RebuildRateCalendarParams{
		FromDate: pgtype.Date{Time: from, Valid: true},
		ToDate:   pgtype.Date{Time: to, Valid: true},
	})
}

// RebuildHorizon regenerates from today at the inn out to the horizon. This is
// what the monthly job and the admin's "save season" both call.
func RebuildHorizon(ctx context.Context, q *db.Queries) (int64, error) {
	today := civil.Today()
	return Rebuild(ctx, q, today, civil.AddMonths(today, HorizonMonths))
}

// RebuildJobKind is the durable job that rolls the calendar forward.
const RebuildJobKind = "rates.rebuild"

// RebuildInterval is the monthly cadence ARCHITECTURE.md gives the job.
//
// The calendar would go stale slowly rather than suddenly without it: nothing
// breaks on the day it stops being regenerated, the far edge of the horizon
// just creeps closer until a guest planning next autumn finds no price and the
// room silently drops out of the search. Saving a season in admin rebuilds
// immediately; this is what covers the months nobody edits anything.
const RebuildInterval = 30 * 24 * time.Hour

// RebuildJob adapts RebuildHorizon to the runner's handler signature.
//
// Safe to run twice, like every handler here: the rebuild replaces a date range
// wholesale rather than appending to it, and it is future-only, so it cannot
// reach a night somebody has already been sold.
func RebuildJob(q *db.Queries) func(context.Context, []byte) error {
	return func(ctx context.Context, _ []byte) error {
		nights, err := RebuildHorizon(ctx, q)
		if err != nil {
			return err
		}
		slog.Info("rebuilt the nightly rate calendar", "nights", nights)
		return nil
	}
}
