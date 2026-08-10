// Package push delivers notifications to the browsers the owner has signed the
// console in on.
//
// It sits beside internal/email and is deliberately shaped the same way: a
// durable job on internal/jobs, a Sender interface with a real implementation
// and a logging one, and nothing sent inline. The reason is the reason email
// has it — a booking is confirmed by a database transaction, and a push service
// having a bad afternoon must delay a notification rather than fail the booking
// that earned it.
//
// What it adds over email is that a subscription can die. A browser that is
// uninstalled, cleared, or simply revoked answers 404 or 410 forever, and the
// only correct response is to forget it. That is the one piece of state this
// package owns.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/jobs"
)

// JobKind is the durable job that delivers one notification to every browser.
const JobKind = "push.send"

// ttl is how long a push service should hold a message for a handset that is
// off or out of signal.
//
// Twelve hours: a booking that arrived overnight is still worth telling the
// owner about at breakfast, and one older than that they will have seen in the
// console before the notification could add anything.
const ttl = 12 * 60 * 60

// Notification is what a push.send job carries, and what the service worker
// receives as JSON.
//
// Deliberately small. It is a nudge towards a screen that has the real
// information on it, not a delivery mechanism for the information itself: the
// console is one tap away and always current, and a notification that tried to
// carry a booking would be a second read model to keep in step.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`

	// URL is where a tap lands, as a path on this site.
	URL string `json:"url"`

	// Tag collapses notifications that supersede one another, so a phone that
	// was off does not present six identical taps. Distinct per subject —
	// per booking code, per inquiry — rather than per kind, because two
	// different bookings are two things the owner needs to see.
	Tag string `json:"tag"`
}

// Subscription is one browser's delivery address, as the database holds it.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
	Label    string
}

// ErrGone reports a subscription the push service will never deliver to again.
//
// Its own error because it is the one failure that must not be retried: the
// browser is gone, and a job that kept trying would fail forever while the
// notifications that matter queue up behind it.
var ErrGone = errors.New("push: the subscription is gone")

// Sender delivers one notification to one browser.
type Sender interface {
	Send(ctx context.Context, sub Subscription, n Notification) error
}

// LogSender writes notifications to the log instead of sending them.
//
// The default until VAPID keys are configured, and loud rather than silent for
// the reason email.LogSender is: an inn that believes it is being notified and
// is not finds out by missing a booking.
type LogSender struct{}

func (LogSender) Send(_ context.Context, sub Subscription, n Notification) error {
	slog.Info("push NOT sent (no VAPID keys configured)",
		"to", sub.Label, "title", n.Title)
	return nil
}

// VAPID is the real sender.
//
// The keys identify this server to the push services, and are what lets a
// browser accept a message claiming to come from it. The subject is a mailto:
// or https: URL the push service can use to contact whoever is responsible —
// required by the spec, and in practice how an operator hears that they are
// sending badly before they get blocked.
type VAPID struct {
	Public  string
	Private string
	Subject string
}

func (v VAPID) Send(ctx context.Context, sub Subscription, n Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("push: encoding the notification: %w", err)
	}

	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      v.Subject,
		VAPIDPublicKey:  v.Public,
		VAPIDPrivateKey: v.Private,
		TTL:             ttl,
		Topic:           topic(n.Tag),
	})
	if err != nil {
		return fmt.Errorf("push: sending to %s: %w", sub.Label, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The browser is gone for good. Named rather than merely failed, so the
		// handler can forget the row instead of retrying it until the end of
		// time.
		return fmt.Errorf("%w: %s answered %d", ErrGone, sub.Label, resp.StatusCode)

	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil

	default:
		// Everything else is worth another go: rate limits, a push service
		// having a moment, a network that came back.
		return fmt.Errorf("push: %s answered %d", sub.Label, resp.StatusCode)
	}
}

// topic is the collapse key a push service understands.
//
// It must be short and base64url-safe, and an over-long or malformed one is
// rejected by some services with a 400 — which would turn a cosmetic nicety
// into a message nobody receives. Empty is always valid, so anything that does
// not obviously qualify becomes empty.
func topic(tag string) string {
	if len(tag) == 0 || len(tag) > 32 {
		return ""
	}
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ""
		}
	}
	return tag
}

// Queue schedules a notification for every subscribed browser. It never sends
// inline.
//
// Takes a *db.Queries so it can be called with an open transaction, which is
// how it ends up committing with the booking that caused it: the queue is a
// table, so the notification and the fact it announces commit together and a
// crash between them cannot lose either.
func Queue(ctx context.Context, q *db.Queries, n Notification) error {
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("push: a notification needs a title")
	}
	if _, err := jobs.Enqueue(ctx, q, jobs.Job{Kind: JobKind, Payload: n}); err != nil {
		return fmt.Errorf("push: queueing %q: %w", n.Title, err)
	}
	return nil
}

// Handler delivers one queued notification to every browser.
//
// Every browser rather than one job per browser, because the fan-out is small —
// an inn with two handsets — and because the thing being retried is the
// notification, not the delivery: a phone that was unreachable when the other
// succeeded should get it on the retry without the other being sent twice. That
// is not free, and the comment below says what it costs.
//
// A dead subscription is deleted rather than returned as a failure. Anything
// else is collected and returned, which hands the job back to the runner for
// backoff.
func Handler(q *db.Queries, sender Sender) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var n Notification
		if err := json.Unmarshal(payload, &n); err != nil {
			return fmt.Errorf("push: decoding the notification: %w", err)
		}

		subs, err := q.ListPushSubscriptions(ctx)
		if err != nil {
			return fmt.Errorf("push: listing subscriptions: %w", err)
		}
		if len(subs) == 0 {
			// Nobody has turned notifications on. Not an error: the inn runs on
			// email and this is the addition.
			return nil
		}

		// A retry re-sends to the browsers that already had it. The alternative
		// — a job per subscription, or marking each one delivered — is a row per
		// handset per notification to carry a duplicate an owner would read as
		// one notification arriving twice on one phone. With two handsets that
		// trade is the right way round; with two hundred it would not be.
		var failed []error
		for _, s := range subs {
			sub := Subscription{
				Endpoint: s.Endpoint,
				P256dh:   s.P256dh,
				Auth:     s.Auth,
				Label:    s.Label,
			}

			switch err := sender.Send(ctx, sub, n); {
			case err == nil:
				// Best effort: a subscription that delivered but could not be
				// stamped is not worth failing the job over.
				if err := q.TouchPushSubscription(ctx, s.Endpoint); err != nil {
					slog.Warn("could not stamp a push subscription", "err", err)
				}

			case errors.Is(err, ErrGone):
				slog.Info("forgetting a dead push subscription", "label", s.Label)
				if _, err := q.DeletePushSubscription(ctx, s.Endpoint); err != nil {
					// Worth retrying: leaving it behind means failing here
					// again next time, which is noise rather than harm.
					failed = append(failed, fmt.Errorf("push: forgetting %s: %w", s.Label, err))
				}

			default:
				failed = append(failed, err)
			}
		}

		return errors.Join(failed...)
	}
}
