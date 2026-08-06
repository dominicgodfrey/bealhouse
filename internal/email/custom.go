package email

import (
	"context"
	"errors"
	"fmt"
	"html/template"

	"github.com/jackc/pgx/v5"

	db "bealhouse/internal/db/gen"
)

// The messages a guest reads are the owner's words, not ours.
//
// Room descriptions, photos, rate seasons and the accessibility notice are all
// data the owner edits rather than code somebody deploys, and email copy is no
// different: a sentence that reads badly to the person whose inn it is has to
// be fixable that afternoon. So the seven templates ship blank, and the words
// that replace them live in `email_templates` — a row per message the owner has
// actually written, with the shipped file standing in for every message they
// have not.
//
// Two things are deliberately outside the editor. **The layout**, because it
// holds the letterhead and the table scaffolding that survives Outlook, and one
// bad edit there breaks every message at once rather than the one on screen.
// And **the payload**: what a template can say about a booking is fixed by the
// struct in data.go, so the console can show the owner the exact list of fields
// each message has to work with.

// Store is where owner-edited copy is read from. *db.Queries satisfies it.
//
// An interface rather than the concrete handle so a Renderer can be built
// without a database — which is what a deploy with no DATABASE_URL, and every
// test that only cares about the shipped copy, actually has.
type Store interface {
	GetEmailTemplate(ctx context.Context, name string) (db.EmailTemplate, error)
}

// custom returns the owner's compiled copy for one message, or nil when the
// shipped file is in force.
//
// Read per send rather than cached. A seven-room inn sends a handful of
// messages a day, so the query costs nothing, and what it buys is that saving
// an edit in admin changes the very next message instead of waiting for a
// restart — which is most of the reason the copy is data at all.
//
// A row that will not parse is returned as an error, so the send fails and the
// job retries rather than the guest getting a half-rendered email. It should be
// unreachable: Parse is what the console must call before saving, and this is
// the same function.
func (r *Renderer) custom(ctx context.Context, name string) (*template.Template, error) {
	if r.store == nil {
		return nil, nil
	}

	row, err := r.store.GetEmailTemplate(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		// The ordinary case for a message nobody has edited yet.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("email: loading the saved copy for %q: %w", name, err)
	}

	return Parse(name, row.Subject, row.Body)
}

// Parse compiles one message's subject and body into a renderable set.
//
// **Exported because it is the check the admin console has to run before it
// saves.** Copy that does not compile fails at send time, and send time is
// after a guest's card has been charged: the confirmation then sits in the
// queue failing on backoff, with nothing in front of the owner to say the
// sentence they typed an hour ago is why. Refusing the save is the only place
// that error is cheap.
//
// The two halves are parsed into one set under the names Render looks for, so
// an edit and a shipped file are the same shape by the time anything executes
// them.
func Parse(name, subject, body string) (*template.Template, error) {
	set := template.New(name)

	if _, err := set.New(name + "_subject").Parse(subject); err != nil {
		return nil, fmt.Errorf("email: the subject line for %q does not compile: %w", name, err)
	}
	if _, err := set.New(name + "_body").Parse(body); err != nil {
		return nil, fmt.Errorf("email: the body for %q does not compile: %w", name, err)
	}
	return set, nil
}

// Shipped returns the copy a message ships with: the two halves of its file in
// internal/email/templates.
//
// What the console prefills the editor with for a message nobody has edited,
// and what "reset to the original" shows before the row is deleted. Recovered
// from the parse tree rather than by cutting the file up with string search,
// because the body contains `{{end}}`s of its own and the first one is not the
// one that closes the block.
//
// Template comments do not survive the round trip — the parser drops them. That
// is the right way round: those comments are notes to whoever writes the copy
// next, and they live in the repository where that person is.
func Shipped(name string) (subject, body string, err error) {
	r, err := New(Brand{}, nil)
	if err != nil {
		return "", "", err
	}
	return r.shipped(name)
}

func (r *Renderer) shipped(name string) (subject, body string, err error) {
	for _, part := range []struct {
		block string
		into  *string
	}{
		{name + "_subject", &subject},
		{name + "_body", &body},
	} {
		t := r.templates.Lookup(part.block)
		if t == nil || t.Tree == nil || t.Tree.Root == nil {
			return "", "", fmt.Errorf("email: no shipped template named %q", name)
		}
		*part.into = t.Tree.Root.String()
	}
	return subject, body, nil
}

// Copy is one message as the console should show it: the words in force, and
// whether they are the owner's or the ones that shipped.
type Copy struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Body    string `json:"body"`

	// Edited is false when nothing has been saved for this message and the
	// shipped copy — a placeholder line saying what the message is for — is
	// what guests are currently receiving. The console needs to say that out
	// loud rather than presenting a placeholder as though it were finished.
	Edited bool `json:"edited"`
}

// Current returns the copy in force for every message, edited or not.
//
// The list of messages comes from Names() rather than from the table, because
// which messages exist is a property of the binary: a template added in a
// release has to appear in the editor immediately, with the shipped words in
// it, and not wait for somebody to insert a row.
func (r *Renderer) Current(ctx context.Context) ([]Copy, error) {
	out := make([]Copy, 0, len(Names()))
	for _, name := range Names() {
		subject, body, err := r.shipped(name)
		if err != nil {
			return nil, err
		}
		c := Copy{Name: name, Subject: subject, Body: body}

		if r.store != nil {
			row, err := r.store.GetEmailTemplate(ctx, name)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// Never edited. The shipped words stand.
			case err != nil:
				return nil, fmt.Errorf("email: loading the saved copy for %q: %w", name, err)
			default:
				c.Subject, c.Body, c.Edited = row.Subject, row.Body, true
			}
		}
		out = append(out, c)
	}
	return out, nil
}
