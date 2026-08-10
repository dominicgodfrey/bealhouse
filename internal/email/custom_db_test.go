package email

import (
	"context"
	"strings"
	"testing"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// The round trip through the table the console writes to.
//
// Rolled back, and needing no exclusive lock: email_templates is this feature's
// own table and nothing else in the suite reads or writes it.
func setupCopy(t *testing.T) (context.Context, *db.Queries) {
	t.Helper()

	pool := testdb.Connect(t)
	tx := testdb.Tx(t, pool)
	return context.Background(), db.New(tx)
}

func TestSavedCopyIsWhatTheNextMessageUses(t *testing.T) {
	ctx, q := setupCopy(t)

	if err := q.UpsertEmailTemplate(ctx, db.UpsertEmailTemplateParams{
		Name:    CheckoutReminder,
		Subject: "Safe travels, {{.Data.GuestName}}",
		Body:    `<p>Checkout is at {{.Data.CheckoutTime}}. Thank you for staying.</p>`,
	}); err != nil {
		t.Fatalf("saving the copy: %v", err)
	}

	r, err := New(Brand{SiteURL: "https://example.test"}, q)
	if err != nil {
		t.Fatalf("building the renderer: %v", err)
	}

	msg, err := r.Render(ctx, CheckoutReminder, CheckoutReminderData{
		GuestName:    "Ada",
		CheckoutTime: "11:00 AM",
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if msg.Subject != "Safe travels, Ada" {
		t.Errorf("subject %q, want the saved one", msg.Subject)
	}
	if !strings.Contains(msg.HTML, "Thank you for staying.") {
		t.Error("the saved body did not reach the message")
	}
}

// No cache anywhere between the save and the send. An owner who fixes a typo
// has to see it fixed on the next message, not after a restart.
func TestASecondSaveTakesEffectImmediately(t *testing.T) {
	ctx, q := setupCopy(t)

	r, err := New(Brand{}, q)
	if err != nil {
		t.Fatalf("building the renderer: %v", err)
	}

	for _, subject := range []string{"First wording", "Second wording"} {
		if err := q.UpsertEmailTemplate(ctx, db.UpsertEmailTemplateParams{
			Name:    BalanceReceipt,
			Subject: subject,
			Body:    "<p>Received.</p>",
		}); err != nil {
			t.Fatalf("saving %q: %v", subject, err)
		}

		msg, err := r.Render(ctx, BalanceReceipt, nil)
		if err != nil {
			t.Fatalf("rendering after saving %q: %v", subject, err)
		}
		if msg.Subject != subject {
			t.Errorf("subject %q, want %q", msg.Subject, subject)
		}
	}
}

// "Reset to the original" is a delete, not a copy of the shipped words into the
// row. Copying them would hand the owner a snapshot that quietly drifts from
// the file it came from.
func TestResettingGoesBackToTheShippedCopy(t *testing.T) {
	ctx, q := setupCopy(t)

	if err := q.UpsertEmailTemplate(ctx, db.UpsertEmailTemplateParams{
		Name:    CancellationRefund,
		Subject: "Your cancellation",
		Body:    "<p>Sorry to see you go.</p>",
	}); err != nil {
		t.Fatalf("saving the copy: %v", err)
	}

	removed, err := q.DeleteEmailTemplate(ctx, CancellationRefund)
	if err != nil {
		t.Fatalf("resetting: %v", err)
	}
	if removed != 1 {
		t.Fatalf("%d rows removed, want 1", removed)
	}

	r, err := New(Brand{}, q)
	if err != nil {
		t.Fatalf("building the renderer: %v", err)
	}
	// Rendered against the sample payload rather than nil: every body is
	// guarded on .Data so that Names() can smoke-test them, which means a nil
	// render produces the layout and nothing to tell the two copies apart.
	msg, err := r.Render(ctx, CancellationRefund, Sample(CancellationRefund))
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if strings.Contains(msg.HTML, "Sorry to see you go.") {
		t.Error("the deleted copy is still being sent")
	}
	if !strings.Contains(msg.HTML, "We are sorry not to be seeing you") {
		t.Error("the shipped copy did not come back")
	}
}

// Blank is not an edit, it is a broken message — and a whitespace subject line
// is what a spam filter looks for. The database refuses it, so it cannot get in
// through some other write path later.
// A fresh transaction per case, deliberately: a constraint violation aborts the
// one it happened in, so sharing would leave the later cases failing on "current
// transaction is aborted" and passing for the wrong reason.
func TestBlankCopyIsRefusedByTheDatabase(t *testing.T) {
	for _, c := range []struct {
		name    string
		subject string
		body    string
	}{
		{"no subject", "", "<p>Something.</p>"},
		{"whitespace subject", "   \n ", "<p>Something.</p>"},
		{"no body", "Something", ""},
		{"whitespace body", "Something", "  "},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, q := setupCopy(t)

			err := q.UpsertEmailTemplate(ctx, db.UpsertEmailTemplateParams{
				Name:    BookingConfirmation,
				Subject: c.subject,
				Body:    c.body,
			})
			if err == nil {
				t.Error("blank copy was accepted")
			}
		})
	}
}
