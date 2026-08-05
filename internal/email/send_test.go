package email

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/jobs"
	"bealhouse/internal/testdb"
)

// Queueing tests run inside a rolled-back transaction, with the queue emptied
// first so a running server's periodic rows cannot be mistaken for ours.
//
// They take the exclusive lock as well, which a rolled-back test usually does
// not need. These are the exception: they run a real job runner against the
// shared jobs table, and the jobs package's own concurrency test commits rows
// into it. Without taking turns, this runner claims one of those, finds no
// handler for it, and the assertion about what was sent becomes a coin flip.
func setupQueue(t *testing.T) (context.Context, *db.Queries, pgx.Tx) {
	t.Helper()

	pool := testdb.Connect(t)
	testdb.Exclusive(t, pool)
	tx := testdb.Tx(t, pool)

	ctx := context.Background()
	if _, err := tx.Exec(ctx, "DELETE FROM jobs"); err != nil {
		t.Fatalf("clearing the queue: %v", err)
	}
	return ctx, db.New(tx), tx
}

// queued counts only this package's messages, so a stray row of somebody else's
// kind cannot be read as one of ours.
func queued(t *testing.T, ctx context.Context, tx pgx.Tx) int {
	t.Helper()

	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = $1", JobKind).Scan(&n); err != nil {
		t.Fatalf("counting queued messages: %v", err)
	}
	return n
}

// recorder is a Sender that remembers instead of sending.
type recorder struct {
	sent []Message
	to   []string
	err  error
}

func (r *recorder) Send(_ context.Context, to string, msg Message) error {
	if r.err != nil {
		return r.err
	}
	r.to = append(r.to, to)
	r.sent = append(r.sent, msg)
	return nil
}

// The path a confirmation actually takes: queued by whatever confirmed the
// booking, rendered and sent later by the runner.
func TestQueuedMessageIsRenderedAndSentByTheRunner(t *testing.T) {
	ctx, q, tx := setupQueue(t)

	if err := Queue(ctx, q, Envelope{To: "ada@example.com", Template: BookingConfirmation}); err != nil {
		t.Fatalf("queueing: %v", err)
	}

	// Nothing has been sent yet: queueing is a database write, not a delivery.
	sender := &recorder{}
	r := renderer(t, Brand{})
	if len(sender.sent) != 0 {
		t.Fatal("queueing sent the message inline")
	}

	runner := jobs.New(q)
	runner.Handle(JobKind, r.Handler(sender))

	ran, err := runner.Once(ctx)
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	if !ran {
		t.Fatal("the runner found nothing queued")
	}

	if len(sender.sent) != 1 {
		t.Fatalf("%d messages sent, want 1", len(sender.sent))
	}
	if sender.to[0] != "ada@example.com" {
		t.Errorf("sent to %q", sender.to[0])
	}
	if sender.sent[0].Subject == "" {
		t.Error("sent with an empty subject")
	}

	// A delivered message leaves the queue.
	if n := queued(t, ctx, tx); n != 0 {
		t.Errorf("%d messages left queued, want 0", n)
	}
}

// A provider having a bad afternoon must cost a delay, never a lost
// confirmation — the guest has already been charged by the time most of these
// are queued.
func TestASendThatFailsStaysQueuedForRetry(t *testing.T) {
	ctx, q, tx := setupQueue(t)

	if err := Queue(ctx, q, Envelope{To: "ada@example.com", Template: BookingConfirmation}); err != nil {
		t.Fatalf("queueing: %v", err)
	}

	sender := &recorder{err: errors.New("resend is down")}
	r := renderer(t, Brand{})

	runner := jobs.New(q)
	runner.Handle(JobKind, r.Handler(sender))

	if _, err := runner.Once(ctx); err != nil {
		t.Fatalf("running: %v", err)
	}

	var lastError string
	if err := tx.QueryRow(ctx,
		"SELECT last_error FROM jobs WHERE kind = $1", JobKind,
	).Scan(&lastError); err != nil {
		t.Fatalf("the message was dropped rather than kept for retry: %v", err)
	}
	if lastError == "" {
		t.Error("the failure left no explanation on the job")
	}
}

func TestQueueRejectsAnIncompleteEnvelope(t *testing.T) {
	ctx, q, tx := setupQueue(t)

	if err := Queue(ctx, q, Envelope{Template: BookingConfirmation}); err == nil {
		t.Error("queued a message with no recipient")
	}
	if err := Queue(ctx, q, Envelope{To: "ada@example.com"}); err == nil {
		t.Error("queued a message with no template")
	}

	if n := queued(t, ctx, tx); n != 0 {
		t.Errorf("%d messages queued, want none", n)
	}
}

// An unknown template must not be silently discarded: somebody is waiting for
// the message, so it stays queued and keeps complaining.
func TestAnUnknownTemplateKeepsFailingRatherThanVanishing(t *testing.T) {
	ctx, q, tx := setupQueue(t)

	if err := Queue(ctx, q, Envelope{To: "ada@example.com", Template: "no_such_template"}); err != nil {
		t.Fatalf("queueing: %v", err)
	}

	r := renderer(t, Brand{})
	runner := jobs.New(q)
	runner.Handle(JobKind, r.Handler(&recorder{}))

	if _, err := runner.Once(ctx); err != nil {
		t.Fatalf("running: %v", err)
	}

	if n := queued(t, ctx, tx); n != 1 {
		t.Errorf("%d messages left queued, want the message kept", n)
	}
}
