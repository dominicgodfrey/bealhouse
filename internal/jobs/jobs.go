// Package jobs is the durable background worker: one Postgres table, polled.
//
// ARCHITECTURE.md's job runner, and deliberately not a queue server. The work
// this inn has to do in the background — reclaim a lapsed hold, warn a guest
// their card is about to be charged, charge it — is measured in a handful of
// rows a day. What matters is that none of it is lost when the process
// restarts, and that two servers could run it without doing anything twice.
// A table with FOR UPDATE SKIP LOCKED gives both, and there is nothing else to
// operate.
//
// A claimed job is leased, not deleted: the claim pushes run_at into the future
// so nobody else takes it, and a runner that dies mid-job leaves work that
// comes back on its own when the lease lapses. Every handler therefore has to
// tolerate being run twice, which is the same discipline the webhook path
// already lives under.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "bealhouse/internal/db/gen"
)

const (
	// PollInterval is how often the runner looks for work. The tightest
	// deadline in the table is hold.sweep's minute, and ARCHITECTURE.md sizes
	// the poll to match.
	PollInterval = time.Minute

	// Lease is how long a claimed job is hidden from other runners. Comfortably
	// longer than any handler here should take, and short enough that a crashed
	// runner's work is picked up while the guest is still on the page.
	Lease = 5 * time.Minute

	// backoffCap bounds the retry delay. A job failing for an hour is a problem
	// a person needs to look at, and stretching the interval further only
	// delays their finding out.
	backoffCap = time.Hour
)

// Handler does one job's work.
//
// It must be safe to run twice: leases lapse, processes die between finishing
// the work and recording that they finished, and Postgres is the only thing
// that knows for certain what has already happened.
type Handler func(ctx context.Context, payload []byte) error

type registration struct {
	fn Handler

	// repeat is non-zero for periodic work. A periodic job is rescheduled
	// rather than deleted when it succeeds, so one row lives in the table
	// forever and the schedule survives restarts without anything having to
	// re-create it.
	repeat time.Duration
}

// Runner polls the jobs table and runs what it finds.
type Runner struct {
	q     *db.Queries
	kinds map[string]registration
}

func New(q *db.Queries) *Runner {
	return &Runner{q: q, kinds: make(map[string]registration)}
}

// Handle registers a one-shot job kind. The row is deleted when it succeeds.
func (r *Runner) Handle(kind string, fn Handler) {
	r.kinds[kind] = registration{fn: fn}
}

// Every registers periodic work, which the runner keeps rescheduling.
//
// The row is created once, keyed on the kind, so restarting the server does not
// accumulate duplicates and two servers do not run it twice.
func (r *Runner) Every(kind string, interval time.Duration, fn Handler) {
	r.kinds[kind] = registration{fn: fn, repeat: interval}
}

// Run works the queue until the context is cancelled.
//
// A failure to reach the database is logged and retried on the next tick rather
// than being fatal: Postgres being briefly unreachable is not a reason to bring
// down a server that is otherwise taking bookings.
func (r *Runner) Run(ctx context.Context) {
	if err := r.ensurePeriodic(ctx); err != nil {
		slog.Error("scheduling periodic jobs", "err", err)
	}

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

// drain runs whatever is due right now, one job at a time until the queue is
// empty. Sequential on purpose: this is a seven-room inn, and a runner that
// cannot keep up with its own workload is telling you something a worker pool
// would hide.
func (r *Runner) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := r.Once(ctx)
		if err != nil {
			slog.Error("running a job", "err", err)
			return
		}
		if !ran {
			return
		}
	}
}

// Once claims and runs a single job, reporting whether there was one.
//
// Exported so tests can drive the runner a step at a time rather than waiting
// on a ticker.
func (r *Runner) Once(ctx context.Context) (bool, error) {
	job, err := r.q.ClaimJob(ctx, int32(Lease.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("jobs: claiming: %w", err)
	}

	reg, ok := r.kinds[job.Kind]
	if !ok {
		// Not an error worth retrying quickly: nothing will change until
		// somebody deploys a handler. Push it out and say so in the row, so it
		// is visible rather than spinning.
		slog.Error("no handler registered for job kind", "kind", job.Kind, "id", job.ID)
		return true, r.fail(ctx, job.ID, backoffCap, "no handler registered for "+job.Kind)
	}

	if err := run(ctx, reg.fn, job.Payload); err != nil {
		slog.Error("job failed",
			"kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "err", err)
		return true, r.fail(ctx, job.ID, backoff(job.Attempts), err.Error())
	}

	if reg.repeat > 0 {
		// Periodic work: put it back rather than deleting it, and clear the
		// error from any previous failure now that it has worked.
		if err := r.q.RescheduleJob(ctx, db.RescheduleJobParams{
			ID:           job.ID,
			DelaySeconds: int32(reg.repeat.Seconds()),
			LastError:    "",
		}); err != nil {
			return true, fmt.Errorf("jobs: rescheduling %s: %w", job.Kind, err)
		}
		return true, nil
	}

	if err := r.q.DeleteJob(ctx, job.ID); err != nil {
		return true, fmt.Errorf("jobs: deleting %s: %w", job.Kind, err)
	}
	return true, nil
}

// run calls a handler and turns a panic into an ordinary job failure.
//
// The runner is a goroutine, so without this a nil map or a short slice
// anywhere in a handler does not fail the job — it takes down the process, and
// with it the API that is serving bookings. Background work misbehaving must
// cost the inn that job and a retry, never the front door.
//
// The stack goes in the error, and so into the row's last_error, because a
// panic that has been caught is otherwise the hardest kind to find later.
func run(ctx context.Context, fn Handler, payload []byte) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return fn(ctx, payload)
}

func (r *Runner) fail(ctx context.Context, id int64, delay time.Duration, reason string) error {
	if err := r.q.RescheduleJob(ctx, db.RescheduleJobParams{
		ID:           id,
		DelaySeconds: int32(delay.Seconds()),
		LastError:    reason,
	}); err != nil {
		return fmt.Errorf("jobs: rescheduling after failure: %w", err)
	}
	return nil
}

// ensurePeriodic makes sure every periodic kind has its one row.
//
// Keyed on the kind, so this is safe to call on every start and from every
// server: the second insert affects nothing.
func (r *Runner) ensurePeriodic(ctx context.Context) error {
	for kind, reg := range r.kinds {
		if reg.repeat == 0 {
			continue
		}
		if _, err := Enqueue(ctx, r.q, Job{Kind: kind, UniqueKey: kind}); err != nil {
			return err
		}
	}
	return nil
}

// Job is work to be queued.
type Job struct {
	Kind string

	// RunAt is when the job becomes runnable. Zero means now.
	RunAt time.Time

	// Payload is optional JSON-encodable detail. Nil becomes an empty object.
	Payload any

	// UniqueKey deduplicates. Two jobs with the same key cannot both be queued,
	// which is what lets a scan enqueue the same work every tick without
	// checking first. Empty means no deduplication.
	UniqueKey string
}

// Enqueue adds work to the table, reporting whether it was actually added.
//
// False means an identical job was already queued, which is an ordinary answer
// rather than a failure: the T-7 scan finds the same booking every minute until
// its charge lands.
func Enqueue(ctx context.Context, q *db.Queries, j Job) (bool, error) {
	payload := []byte("{}")
	if j.Payload != nil {
		encoded, err := json.Marshal(j.Payload)
		if err != nil {
			return false, fmt.Errorf("jobs: encoding the payload for %s: %w", j.Kind, err)
		}
		payload = encoded
	}

	// Left NULL for "as soon as possible", so the database's clock decides what
	// that means. See the query: the claim compares against now(), and stamping
	// this from Go makes due-ness depend on the skew between two machines.
	var runAt pgtype.Timestamptz
	if !j.RunAt.IsZero() {
		runAt = pgtype.Timestamptz{Time: j.RunAt, Valid: true}
	}

	var key *string
	if j.UniqueKey != "" {
		key = &j.UniqueKey
	}

	added, err := q.EnqueueJob(ctx, db.EnqueueJobParams{
		Kind:      j.Kind,
		RunAt:     runAt,
		Payload:   payload,
		UniqueKey: key,
	})
	if err != nil {
		return false, fmt.Errorf("jobs: enqueueing %s: %w", j.Kind, err)
	}
	return added > 0, nil
}

// backoff grows with the attempt count and is jittered, so a batch of jobs that
// failed together for the same reason does not retry in lockstep and fail
// together again.
func backoff(attempts int32) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	// 1<<attempts seconds, capped. attempts is bounded well below the point
	// where this could overflow, because the cap bites within a dozen tries.
	delay := backoffCap
	if attempts < 32 {
		if d := time.Duration(1<<uint(attempts)) * time.Second; d < backoffCap {
			delay = d
		}
	}
	return delay + time.Duration(rand.IntN(1000))*time.Millisecond
}
