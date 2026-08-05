package booking

import (
	"context"
	"log/slog"
	"time"

	db "bealhouse/internal/db/gen"
)

// SweepJobKind is the durable job that runs Sweep. Registered by the server
// with the rest of the background work.
const SweepJobKind = "hold.sweep"

// SweepInterval matches the cadence ARCHITECTURE.md gives the hold.sweep job.
// A hold outliving its TTL by up to a minute costs nothing: the room is not
// double-booked, it is briefly still held by someone who has gone.
const SweepInterval = time.Minute

// Sweep reclaims abandoned checkouts.
//
// Two statements in one order that matters. Deleting the hold frees the room —
// the exclusion constraint stops enforcing something nobody is waiting on
// anymore — and then the booking behind it is marked expired, so a guest
// returning to their confirmation link is told what happened rather than shown
// a pending booking that will never complete.
//
// Both are idempotent, which is what makes it safe to run this on a timer, on
// two servers, or twice in the same second.
func Sweep(ctx context.Context, q *db.Queries) (holds int64, expired int64, err error) {
	holds, err = q.SweepExpiredHolds(ctx)
	if err != nil {
		return 0, 0, err
	}

	expired, err = q.ExpireAbandonedBookings(ctx)
	if err != nil {
		return holds, 0, err
	}
	return holds, expired, nil
}

// SweepJob adapts Sweep to the job runner's handler signature.
//
// Step 3 ran this on a plain ticker, which was the smallest thing that made
// that step correct on its own — a hold nobody reclaims takes a room off sale
// permanently. It is a durable job now, as ARCHITECTURE.md always intended:
// the schedule survives a restart, and two servers running it do not sweep
// twice.
//
// A failed sweep is reported to the runner, which retries it with backoff. The
// work is idempotent, so a retry costs nothing.
func SweepJob(q *db.Queries) func(context.Context, []byte) error {
	return func(ctx context.Context, _ []byte) error {
		holds, expired, err := Sweep(ctx, q)
		if err != nil {
			return err
		}
		if holds > 0 || expired > 0 {
			slog.Info("reclaimed abandoned checkouts", "holds", holds, "bookings", expired)
		}
		return nil
	}
}
