package booking

import (
	"context"
	"log/slog"
	"time"

	db "bealhouse/internal/db/gen"
)

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

// RunSweeper sweeps until the context is cancelled.
//
// This is deliberately not the job runner. ARCHITECTURE.md's durable `jobs`
// table with FOR UPDATE SKIP LOCKED arrives with payments in build-order step
// 4, and hold.sweep becomes one row in it; this is the smallest thing that
// makes step 3 correct on its own, because a hold nobody reclaims takes a room
// off sale permanently.
//
// A failed sweep is logged and retried on the next tick rather than being
// fatal: the database being briefly unreachable is not a reason to bring down
// a server that is otherwise taking bookings.
func RunSweeper(ctx context.Context, q *db.Queries) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			holds, expired, err := Sweep(ctx, q)
			if err != nil {
				slog.Error("sweeping expired holds", "err", err)
				continue
			}
			if holds > 0 || expired > 0 {
				slog.Info("reclaimed abandoned checkouts", "holds", holds, "bookings", expired)
			}
		}
	}
}
