package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// resendEndpoint is the whole of Resend's surface that this system needs.
const resendEndpoint = "https://api.resend.com/emails"

// Resend delivers through Resend (decision #17).
//
// Written against net/http rather than the vendored SDK: one endpoint, one JSON
// body, one bearer token. The SDK would be a dependency to track and update for
// forty lines it does not save. That is the opposite call from internal/gateway,
// and deliberately — Stripe's SDK earns its place carrying webhook signature
// verification and a large typed object graph, and none of that is in play here.
//
// Like gateway.Stripe, this is written and has never made a request: the
// account is not set up yet. Everything around it — rendering, queueing, retry —
// is exercised against LogSender.
type Resend struct {
	apiKey string
	from   string
	client *http.Client

	// endpoint is resendEndpoint everywhere but the tests, which point it at an
	// httptest server. This code cannot be exercised against the real thing
	// until the account exists, and the request it builds — the bearer header,
	// the body shape, what it does with a rejection — is exactly what wants
	// checking before then.
	endpoint string
}

// NewResend builds a sender.
//
// from must be a verified sender on a domain whose DNS carries Resend's DKIM,
// and reads best in display form: `Beal House <stay@bealhouse.com>`. Resend
// rejects anything else, which is a 422 on the first send rather than something
// that fails quietly later.
func NewResend(apiKey, from string) *Resend {
	return &Resend{
		apiKey:   apiKey,
		from:     from,
		endpoint: resendEndpoint,

		// Bounded on its own rather than left to the caller's context. This runs
		// inside a job the runner is waiting on, and a provider that accepts a
		// connection and then says nothing must cost this message a retry rather
		// than block the queue behind it.
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// resendRequest is Resend's create-email body.
type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// Send delivers one rendered message.
//
// Every failure is returned, so the runner retries with backoff. That is right
// for the ones worth retrying — a timeout, a 429, a bad afternoon at Resend —
// and it is also what happens to a permanently rejected message, which will
// fail forever with its reason in the job's last_error. That is the same stance
// Handler already takes on an unknown template, and for the same reason:
// discarding a message a guest is waiting for is the worse failure, and a job
// stuck loudly is one somebody can see.
func (r *Resend) Send(ctx context.Context, to string, msg Message) error {
	body, err := json.Marshal(resendRequest{
		From:    r.from,
		To:      []string{to},
		Subject: msg.Subject,
		HTML:    msg.HTML,
	})
	if err != nil {
		return fmt.Errorf("email: encoding the request for %s: %w", to, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("email: building the request for %s: %w", to, err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("email: sending to %s: %w", to, err)
	}
	defer resp.Body.Close()

	// Capped, and read whether or not it is wanted. Draining lets the connection
	// go back to the pool, and the cap is because an error body is the one thing
	// here whose size nothing on this side controls.
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body carries Resend's own reason — an unverified domain, a
		// malformed from, a rate limit. It is worth more than the status alone
		// and contains nothing secret; the key travels in a header, not here.
		return fmt.Errorf("email: sending to %s: resend returned %s: %s",
			to, resp.Status, bytes.TrimSpace(payload))
	}

	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(payload, &out)

	// Logged because it is the only handle on a delivery afterwards: an owner
	// asking whether a guest was really emailed is answered by this id in
	// Resend's dashboard.
	slog.Info("email sent", "to", to, "subject", msg.Subject, "id", out.ID)
	return nil
}
