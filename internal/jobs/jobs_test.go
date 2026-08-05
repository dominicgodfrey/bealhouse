package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// Most tests run inside a rolled-back transaction, so queued jobs vanish with
// the test and never reach a running server's runner.
//
// The queue is emptied inside that transaction first, the way the rates tests
// replace the season table. Without it these tests are only correct on a
// machine where nobody has started the server: `hold.sweep` is a committed row
// that lives in this table forever, and a runner under test would claim it,
// find no handler registered for it, and quietly ruin an assertion about what
// the queue contains. The delete is rolled back, so the real row survives.
func setup(t *testing.T) (context.Context, *db.Queries, pgx.Tx) {
	t.Helper()

	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)

	ctx := context.Background()
	if _, err := tx.Exec(ctx, "DELETE FROM jobs"); err != nil {
		t.Fatalf("clearing the queue: %v", err)
	}
	return ctx, db.New(tx), tx
}

func TestEnqueueDeduplicatesOnUniqueKey(t *testing.T) {
	ctx, q, _ := setup(t)

	added, err := Enqueue(ctx, q, Job{Kind: "test.thing", UniqueKey: "test.thing:1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !added {
		t.Fatal("the first enqueue did not add a job")
	}

	// The scans that feed this table run every tick and keep finding the same
	// work, so this is the ordinary case and not an error.
	added, err = Enqueue(ctx, q, Job{Kind: "test.thing", UniqueKey: "test.thing:1"})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if added {
		t.Error("the same unique key was queued twice")
	}

	// No key means no deduplication: two genuinely separate pieces of work.
	for range 2 {
		if _, err := Enqueue(ctx, q, Job{Kind: "test.thing"}); err != nil {
			t.Fatalf("keyless enqueue: %v", err)
		}
	}

	if n, err := q.CountJobs(ctx); err != nil || n != 3 {
		t.Errorf("queue holds %d jobs (err %v), want 3", n, err)
	}
}

func TestOneShotJobRunsThenDisappears(t *testing.T) {
	ctx, q, _ := setup(t)

	// Decoded rather than compared as bytes: the column is jsonb, so Postgres
	// hands back its own normalised rendering rather than what was sent.
	var got struct {
		Code string `json:"code"`
	}
	r := New(q)
	r.Handle("test.thing", func(_ context.Context, payload []byte) error {
		return json.Unmarshal(payload, &got)
	})

	if _, err := Enqueue(ctx, q, Job{Kind: "test.thing", Payload: map[string]string{"code": "BH-ABC123"}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ran, err := r.Once(ctx)
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	if !ran {
		t.Fatal("the runner found nothing to do")
	}
	if got.Code != "BH-ABC123" {
		t.Errorf("handler saw code %q, want BH-ABC123", got.Code)
	}

	if n, err := q.CountJobs(ctx); err != nil || n != 0 {
		t.Errorf("%d jobs left (err %v); a finished one-shot job should be gone", n, err)
	}

	// And nothing is left to pick up.
	if ran, err := r.Once(ctx); err != nil || ran {
		t.Errorf("ran=%v err=%v, want nothing left", ran, err)
	}
}

// Periodic work keeps its row. The schedule is the table, so it has to survive
// both the job succeeding and the server restarting.
func TestPeriodicJobIsRescheduledRatherThanDeleted(t *testing.T) {
	ctx, q, _ := setup(t)

	var runs atomic.Int64
	r := New(q)
	r.Every("test.tick", time.Minute, func(context.Context, []byte) error {
		runs.Add(1)
		return nil
	})

	if err := r.ensurePeriodic(ctx); err != nil {
		t.Fatalf("scheduling: %v", err)
	}
	// Starting again must not queue a second copy.
	if err := r.ensurePeriodic(ctx); err != nil {
		t.Fatalf("scheduling again: %v", err)
	}
	if n, err := q.CountJobs(ctx); err != nil || n != 1 {
		t.Fatalf("%d periodic rows (err %v), want exactly 1", n, err)
	}

	if ran, err := r.Once(ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v, want a job to have run", ran, err)
	}
	if runs.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", runs.Load())
	}

	if n, err := q.CountJobs(ctx); err != nil || n != 1 {
		t.Errorf("%d jobs after a periodic run (err %v), want the row kept", n, err)
	}

	// ...but not again until its interval is up.
	if ran, err := r.Once(ctx); err != nil || ran {
		t.Errorf("ran=%v err=%v; a rescheduled job ran early", ran, err)
	}
}

func TestFailedJobIsKeptRetriedAndExplains(t *testing.T) {
	ctx, q, tx := setup(t)

	r := New(q)
	r.Handle("test.flaky", func(context.Context, []byte) error {
		return errors.New("the card machine is on fire")
	})

	if _, err := Enqueue(ctx, q, Job{Kind: "test.flaky"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if ran, err := r.Once(ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}

	var attempts int32
	var lastError string
	var runAt time.Time
	if err := tx.QueryRow(ctx,
		"SELECT attempts, last_error, run_at FROM jobs WHERE kind = 'test.flaky'",
	).Scan(&attempts, &lastError, &runAt); err != nil {
		t.Fatalf("reading the job back: %v", err)
	}

	// A failed job that vanished would be money silently not collected.
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if lastError != "the card machine is on fire" {
		t.Errorf("last_error = %q, want the handler's message", lastError)
	}
	if !runAt.After(time.Now()) {
		t.Error("a failed job is runnable again immediately; it should back off")
	}
}

// A panicking handler must cost its job a retry, not the process.
//
// The runner is a goroutine: an unrecovered panic in any handler takes the
// whole binary down, and with it the API serving bookings. This test passing at
// all is most of the point — without the recover the test binary itself dies.
func TestPanickingHandlerFailsTheJobRatherThanTheProcess(t *testing.T) {
	ctx, q, tx := setup(t)

	r := New(q)
	var payload []byte
	r.Handle("test.panics", func(context.Context, []byte) error {
		_ = payload[3] // the sort of thing a Stripe response makes easy
		return nil
	})

	if _, err := Enqueue(ctx, q, Job{Kind: "test.panics"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if ran, err := r.Once(ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}

	// The runner survived, and the next job is still reachable.
	var lastError string
	var runAt time.Time
	if err := tx.QueryRow(ctx,
		"SELECT last_error, run_at FROM jobs WHERE kind = 'test.panics'",
	).Scan(&lastError, &runAt); err != nil {
		t.Fatalf("reading the job back: %v", err)
	}
	if !strings.HasPrefix(lastError, "panic:") {
		t.Errorf("last_error = %q, want it to start with the panic", lastError)
	}
	// The stack is what makes a caught panic findable afterwards.
	if !strings.Contains(lastError, "jobs.run") {
		t.Error("last_error carries no stack; a recovered panic with no trace is hard to chase")
	}
	if !runAt.After(time.Now()) {
		t.Error("a panicking job is runnable again immediately; it should back off")
	}
}

// A job whose kind nobody handles must not spin: it is rescheduled far out and
// says why, so it is visible in the table instead of burning a poll every tick.
func TestUnhandledKindBacksOffInsteadOfSpinning(t *testing.T) {
	ctx, q, tx := setup(t)

	r := New(q)
	if _, err := Enqueue(ctx, q, Job{Kind: "test.nobody"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if ran, err := r.Once(ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}

	var lastError string
	var runAt time.Time
	if err := tx.QueryRow(ctx,
		"SELECT last_error, run_at FROM jobs WHERE kind = 'test.nobody'",
	).Scan(&lastError, &runAt); err != nil {
		t.Fatalf("reading the job back: %v", err)
	}
	if lastError == "" {
		t.Error("an unhandled job kind left no explanation in the row")
	}
	if time.Until(runAt) < 30*time.Minute {
		t.Errorf("an unhandled job retries in %v; it should back off hard", time.Until(runAt))
	}
}

// Claiming leases: a job in flight is invisible to the next caller, which is
// what stops two servers doing the same work.
func TestClaimingLeasesTheJob(t *testing.T) {
	ctx, q, _ := setup(t)

	if _, err := Enqueue(ctx, q, Job{Kind: "test.thing"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := q.ClaimJob(ctx, int32(Lease.Seconds()))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Attempts != 1 {
		t.Errorf("attempts = %d after claiming, want 1", first.Attempts)
	}

	if _, err := q.ClaimJob(ctx, int32(Lease.Seconds())); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a leased job was claimable again: %v", err)
	}
}

// The reason this table exists rather than a ticker: several runners polling at
// once must each get different work, and no job may run twice.
//
// This one commits, so it takes its turn with the other destructive packages.
func TestConcurrentRunnersNeverShareAJob(t *testing.T) {
	pool := testdb.Connect(t)
	testdb.Exclusive(t, pool)
	testdb.ResetJobs(t, pool)
	t.Cleanup(func() { testdb.ResetJobs(t, pool) })

	ctx := context.Background()
	q := db.New(pool)

	const total = 40
	for i := range total {
		if _, err := Enqueue(ctx, q, Job{
			Kind:      "test.contended",
			UniqueKey: fmt.Sprintf("test.contended:%d", i),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	// ran counts handler invocations, which is the duplicate check itself: a
	// job is deleted once it succeeds, so a second runner claiming the same row
	// would push this above the number queued.
	var ran atomic.Int64

	// Every runner drains the queue through the real handler path. Between them
	// they must run each job exactly once: a job claimed twice here is, in
	// production, a card charged twice.
	const runners = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()

			r := New(q)
			r.Handle("test.contended", func(context.Context, []byte) error {
				ran.Add(1)
				return nil
			})

			<-start
			for {
				worked, err := r.Once(ctx)
				if err != nil {
					t.Errorf("running: %v", err)
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := ran.Load(); got != total {
		t.Errorf("%d handler runs for %d jobs; anything above means a job was claimed twice", got, total)
	}

	left, err := q.CountJobs(ctx)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if left != 0 {
		t.Errorf("%d jobs left behind, want 0", left)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if a, b := backoff(1), backoff(4); a >= b {
		t.Errorf("backoff did not grow: attempt 1 = %v, attempt 4 = %v", a, b)
	}
	if d := backoff(40); d > backoffCap+time.Second {
		t.Errorf("backoff at attempt 40 is %v, want it capped near %v", d, backoffCap)
	}
	// Bounded so a large attempt count cannot overflow the shift into a
	// negative duration and make a failing job runnable immediately.
	if d := backoff(64); d <= 0 {
		t.Errorf("backoff overflowed to %v", d)
	}
}
