package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/jobs"
)

// JobKind is the durable job that renders and sends one message.
const JobKind = "email.send"

// Sender delivers a rendered message. Resend implements this; so does LogSender.
//
// An interface rather than a concrete client because the thing that must be
// testable is the queue-and-retry behaviour around sending, not the HTTP call
// itself — and because a provider outage should be a swapped implementation,
// not a rewrite.
type Sender interface {
	Send(ctx context.Context, to string, msg Message) error
}

// LogSender writes messages to the log instead of sending them.
//
// The default until Resend is configured. Deliberately loud rather than a
// silent no-op: an inn that thinks it is emailing guests and is not would find
// out from a guest, and the log line is what makes that visible in dev.
type LogSender struct{}

func (LogSender) Send(_ context.Context, to string, msg Message) error {
	slog.Info("email NOT sent (no provider configured)",
		"to", to, "subject", msg.Subject)
	return nil
}

// Envelope is what an email.send job carries.
//
// The template name and its data rather than rendered HTML: a message that sits
// in the queue through a deploy should go out in the shape the current
// templates describe, not the one they had when it was queued.
type Envelope struct {
	To       string `json:"to"`
	Template string `json:"template"`
	Data     any    `json:"data,omitempty"`
}

// Queue schedules a message. It never sends inline.
//
// Which is the whole point (ARCHITECTURE.md): a booking is confirmed by a
// database transaction, and an email provider having a bad afternoon must delay
// the confirmation rather than fail the booking that earned it.
func Queue(ctx context.Context, q *db.Queries, env Envelope) error {
	if strings.TrimSpace(env.To) == "" {
		return fmt.Errorf("email: no recipient for %q", env.Template)
	}
	if env.Template == "" {
		return fmt.Errorf("email: no template for %q", env.To)
	}

	// No unique key: two confirmations for two different bookings are two
	// genuinely separate sends, and the callers that must not double-send —
	// the webhook path — are already idempotent upstream of here.
	if _, err := jobs.Enqueue(ctx, q, jobs.Job{Kind: JobKind, Payload: env}); err != nil {
		return fmt.Errorf("email: queueing %q: %w", env.Template, err)
	}
	return nil
}

// Handler renders and sends one queued message.
//
// Returning an error hands the job back to the runner, which retries it with
// backoff — so a provider that is briefly down costs a delay rather than a lost
// confirmation. An unknown template is returned as an error too and will keep
// failing; that is correct, because the alternative is discarding a message a
// guest is waiting for.
func (r *Renderer) Handler(sender Sender) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var env Envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return fmt.Errorf("email: decoding the envelope: %w", err)
		}

		msg, err := r.Render(env.Template, env.Data)
		if err != nil {
			return err
		}
		if err := sender.Send(ctx, env.To, msg); err != nil {
			return fmt.Errorf("email: sending %q to %s: %w", env.Template, env.To, err)
		}
		return nil
	}
}
