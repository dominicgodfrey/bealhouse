package email

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// A Store the owner has typed into, without a database in the way.
//
// The behaviour worth pinning down here is which words reach the guest, and
// that is decided in Go. The round trip through Postgres is tested separately,
// against a real one, because that is where the constraint on blank copy lives.
type fakeStore map[string]db.EmailTemplate

func (f fakeStore) GetEmailTemplate(_ context.Context, name string) (db.EmailTemplate, error) {
	row, ok := f[name]
	if !ok {
		return db.EmailTemplate{}, pgx.ErrNoRows
	}
	return row, nil
}

func editedRenderer(t *testing.T, store Store) *Renderer {
	t.Helper()
	r, err := New(Brand{SiteURL: "https://example.test"}, store)
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}
	return r
}

// The point of the whole feature: what the owner saved is what the guest gets.
func TestOwnerCopyReplacesTheShippedTemplate(t *testing.T) {
	r := editedRenderer(t, fakeStore{
		CheckoutReminder: {
			Name:    CheckoutReminder,
			Subject: "Safe travels, {{.Data.GuestName}}",
			Body:    `<p>Checkout is at {{.Data.CheckoutTime}}.</p>`,
		},
	})

	msg, err := r.Render(context.Background(), CheckoutReminder, CheckoutReminderData{
		GuestName:    "Ada",
		CheckoutTime: "11:00 AM",
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	if msg.Subject != "Safe travels, Ada" {
		t.Errorf("subject %q, want the owner's", msg.Subject)
	}
	if !strings.Contains(msg.HTML, "Checkout is at 11:00 AM.") {
		t.Error("the owner's body is not in the message")
	}
	if strings.Contains(msg.HTML, "PLACEHOLDER") || strings.Contains(msg.HTML, "not written yet") {
		t.Error("the shipped placeholder is still being sent")
	}
}

// Editing one message must not disturb the others. The store holds a row per
// message the owner has written, and everything else still ships blank.
func TestUneditedMessagesStillShipTheirOwnCopy(t *testing.T) {
	r := editedRenderer(t, fakeStore{
		CheckoutReminder: {Subject: "edited", Body: "<p>edited</p>"},
	})

	msg, err := r.Render(context.Background(), BookingConfirmation, nil)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if strings.Contains(msg.HTML, "edited") {
		t.Error("one message's edit leaked into another")
	}
}

// The letterhead is not the owner's to break from the editor. An edited body
// still arrives inside the shipped layout, because one bad edit there would
// break every message the inn sends rather than the one on screen.
func TestEditedCopyIsStillWrappedInTheSharedLayout(t *testing.T) {
	r := editedRenderer(t, fakeStore{
		BookingConfirmation: {Subject: "Confirmed", Body: "<p>You are booked.</p>"},
	})

	msg, err := r.Render(context.Background(), BookingConfirmation, nil)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(msg.HTML), "<!doctype html>") {
		t.Error("the edited body was not wrapped in the shared layout")
	}
	if !strings.Contains(msg.HTML, "Beal House") {
		t.Error("the letterhead is missing from an edited message")
	}
}

// The check that has to happen before a save, not after.
//
// Copy that does not compile fails at send time, and send time is after the
// card has been charged: the confirmation then retries on backoff forever with
// nothing in front of the owner to connect it to the sentence they typed.
func TestCopyThatDoesNotCompileIsRefused(t *testing.T) {
	for _, c := range []struct {
		name    string
		subject string
		body    string
	}{
		{"unclosed action in the body", "Fine", "<p>{{.Data.GuestName</p>"},
		{"unclosed action in the subject", "{{.Data.Code", "<p>Fine</p>"},
		{"an if with no end", "Fine", "{{if .Data.Code}}<p>x</p>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(BookingConfirmation, c.subject, c.body); err == nil {
				t.Error("copy that will not render was accepted")
			}
		})
	}
}

// Values a guest supplied are escaped in the owner's copy exactly as they are
// in the shipped copy. The owner writes HTML; a guest does not get to.
func TestGuestValuesAreEscapedInEditedCopy(t *testing.T) {
	r := editedRenderer(t, fakeStore{
		BookingConfirmation: {Subject: "Confirmed", Body: "<p>{{.Data.GuestName}}</p>"},
	})

	msg, err := r.Render(context.Background(), BookingConfirmation, BookingConfirmationData{
		GuestName: `<script>alert(1)</script>`,
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if strings.Contains(msg.HTML, "<script>") {
		t.Error("a guest-supplied value was rendered as markup")
	}
}

// What the console prefills the editor with. Every message has to offer one,
// including any added after this test was written.
func TestEveryMessageHasShippedCopyToStartFrom(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			subject, body, err := Shipped(name)
			if err != nil {
				t.Fatalf("reading the shipped copy: %v", err)
			}
			if strings.TrimSpace(subject) == "" {
				t.Error("no subject to prefill the editor with")
			}
			if strings.TrimSpace(body) == "" {
				t.Error("no body to prefill the editor with")
			}
			// It has to be usable as copy, not just non-empty: the editor saves
			// what it shows, and what it shows must be something Parse accepts.
			if _, err := Parse(name, subject, body); err != nil {
				t.Errorf("the shipped copy does not survive a round trip through the editor: %v", err)
			}
		})
	}
}

func TestShippedCopyForAnUnknownMessageIsAnError(t *testing.T) {
	if _, _, err := Shipped("nonexistent"); err == nil {
		t.Error("reading shipped copy for a message that does not exist succeeded")
	}
}

// The list the console renders: every message the binary knows about, with the
// words in force and whether they are the owner's. A message added in a release
// has to appear here immediately rather than waiting for a row.
func TestCurrentListsEveryMessageAndFlagsTheEditedOnes(t *testing.T) {
	r := editedRenderer(t, fakeStore{
		CheckoutReminder: {Subject: "Safe travels", Body: "<p>Goodbye.</p>"},
	})

	copies, err := r.Current(context.Background())
	if err != nil {
		t.Fatalf("listing the copy: %v", err)
	}
	if len(copies) != len(Names()) {
		t.Fatalf("%d messages listed, want %d", len(copies), len(Names()))
	}

	for _, c := range copies {
		switch c.Name {
		case CheckoutReminder:
			if !c.Edited {
				t.Error("an edited message is not flagged as edited")
			}
			if c.Subject != "Safe travels" {
				t.Errorf("subject %q, want the owner's", c.Subject)
			}
		default:
			if c.Edited {
				t.Errorf("%s is flagged as edited and was never touched", c.Name)
			}
			if strings.TrimSpace(c.Subject) == "" {
				t.Errorf("%s has nothing to show in the editor", c.Name)
			}
		}
	}
}

// A Renderer with no store is the deploy that has no database. It must render,
// not fail.
func TestNoStoreMeansTheShippedCopy(t *testing.T) {
	r := editedRenderer(t, nil)

	msg, err := r.Render(context.Background(), CheckoutReminder, nil)
	if err != nil {
		t.Fatalf("rendering without a store: %v", err)
	}
	if strings.TrimSpace(msg.Subject) == "" {
		t.Error("no subject")
	}
}
