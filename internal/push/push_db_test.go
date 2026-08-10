package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// A rolled-back transaction, and no exclusive lock: push_subscriptions is this
// feature's own table and nothing else in the suite reads or writes it.
func setup(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()

	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	return context.Background(), db.New(tx)
}

// owner makes an account for the subscriptions to hang off, since user_id is
// NOT NULL and cascades.
func owner(t *testing.T, ctx context.Context, q *db.Queries) int64 {
	t.Helper()

	// The handle is the WebAuthn user handle, opaque and unique per account.
	u, err := q.CreateUser(ctx, db.CreateUserParams{
		Handle: []byte("test-owner-handle-for-push-tests!"),
		Name:   "Test owner",
	})
	if err != nil {
		t.Fatalf("creating the owner: %v", err)
	}
	return u.ID
}

func subscribe(t *testing.T, ctx context.Context, q *db.Queries, userID int64, endpoint, label string) {
	t.Helper()

	if err := q.UpsertPushSubscription(ctx, db.UpsertPushSubscriptionParams{
		Endpoint: endpoint,
		UserID:   userID,
		P256dh:   "test-p256dh",
		Auth:     "test-auth",
		Label:    label,
	}); err != nil {
		t.Fatalf("subscribing %s: %v", label, err)
	}
}

// fake records what it was asked to send and answers however the test says.
type fake struct {
	sent map[string]Notification
	fail map[string]error
}

func (f *fake) Send(_ context.Context, sub Subscription, n Notification) error {
	if err := f.fail[sub.Endpoint]; err != nil {
		return err
	}
	if f.sent == nil {
		f.sent = map[string]Notification{}
	}
	f.sent[sub.Endpoint] = n
	return nil
}

func payloadFor(t *testing.T, n Notification) []byte {
	t.Helper()

	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("encoding the notification: %v", err)
	}
	return b
}

// **The property that matters.** A browser that has been cleared or uninstalled
// answers 410 forever, and a handler that treated it as a failure would retry
// it until the job gave up — with every later notification queued behind it.
func TestADeadSubscriptionIsForgottenRatherThanRetried(t *testing.T) {
	ctx, q := setup(t)
	id := owner(t, ctx, q)

	subscribe(t, ctx, q, id, "https://push.example.test/gone", "Old phone")
	subscribe(t, ctx, q, id, "https://push.example.test/live", "Current phone")

	sender := &fake{fail: map[string]error{
		"https://push.example.test/gone": fmt.Errorf("%w: 410", ErrGone),
	}}

	err := Handler(q, sender)(ctx, payloadFor(t, Notification{Title: "New booking"}))
	if err != nil {
		t.Fatalf("the job failed over a dead subscription: %v", err)
	}

	left, err := q.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("listing what is left: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("%d subscriptions left, want 1", len(left))
	}
	if left[0].Endpoint != "https://push.example.test/live" {
		t.Errorf("the wrong subscription survived: %s", left[0].Endpoint)
	}

	// And the live one still heard about it. A dead neighbour must not cost the
	// notification for everybody else.
	if _, ok := sender.sent["https://push.example.test/live"]; !ok {
		t.Error("the live phone was not sent the notification")
	}
}

// A push service having a moment is worth another go, so the job has to fail —
// that is what hands it back to the runner for backoff.
func TestATemporaryFailureIsRetriedAndKeepsTheSubscription(t *testing.T) {
	ctx, q := setup(t)
	id := owner(t, ctx, q)

	subscribe(t, ctx, q, id, "https://push.example.test/flaky", "A phone")

	sender := &fake{fail: map[string]error{
		"https://push.example.test/flaky": errors.New("503 from the push service"),
	}}

	if err := Handler(q, sender)(ctx, payloadFor(t, Notification{Title: "New booking"})); err == nil {
		t.Fatal("a temporary failure was swallowed; the job would never be retried")
	}

	left, err := q.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("listing what is left: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d subscriptions left, want the flaky one kept", len(left))
	}
}

// Nobody subscribed is not a failure. The inn runs on email and this is the
// addition — a job that failed here would retry forever on every booking.
func TestNoSubscribersIsNotAFailure(t *testing.T) {
	ctx, q := setup(t)

	if err := Handler(q, &fake{})(ctx, payloadFor(t, Notification{Title: "New booking"})); err != nil {
		t.Fatalf("no subscribers reported as an error: %v", err)
	}
}

// Re-subscribing is what a browser does on its own, and it hands back the same
// endpoint. It must update rather than accumulate, or one phone gets a
// duplicate for every time notifications were switched on.
func TestResubscribingReplacesRatherThanAccumulates(t *testing.T) {
	ctx, q := setup(t)
	id := owner(t, ctx, q)

	subscribe(t, ctx, q, id, "https://push.example.test/same", "First name")
	subscribe(t, ctx, q, id, "https://push.example.test/same", "Second name")

	left, err := q.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("%d rows, want 1", len(left))
	}
	if left[0].Label != "Second name" {
		t.Errorf("label is %q, want the newer one", left[0].Label)
	}
}
