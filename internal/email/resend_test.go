package email

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resendStub stands in for the API and hands back whatever it was told to.
func resendStub(t *testing.T, status int, body string, seen *http.Request, gotBody *[]byte) *Resend {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		b, _ := io.ReadAll(r.Body)
		*gotBody = b

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	sender := NewResend("re_test_key", "Beal House <stay@bealhouse.com>")
	sender.endpoint = srv.URL
	return sender
}

func TestResendSendsTheMessageItWasGiven(t *testing.T) {
	var req http.Request
	var body []byte
	sender := resendStub(t, http.StatusOK, `{"id":"a1b2c3"}`, &req, &body)

	msg := Message{Subject: "Your stay is confirmed", HTML: "<p>Hello</p>"}
	if err := sender.Send(context.Background(), "guest@example.com", msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The key travels in a header and nowhere else. A send that forgot it would
	// fail against the real API and pass against any stub that did not look.
	if got := req.Header.Get("Authorization"); got != "Bearer re_test_key" {
		t.Errorf("Authorization = %q, want the bearer key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}

	var sent resendRequest
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding what was sent: %v", err)
	}
	if sent.From != "Beal House <stay@bealhouse.com>" {
		t.Errorf("from = %q", sent.From)
	}
	// A single recipient, in the array Resend expects — not the bare string,
	// which it rejects.
	if len(sent.To) != 1 || sent.To[0] != "guest@example.com" {
		t.Errorf("to = %v, want one recipient", sent.To)
	}
	if sent.Subject != msg.Subject {
		t.Errorf("subject = %q, want %q", sent.Subject, msg.Subject)
	}
	if sent.HTML != msg.HTML {
		t.Errorf("html = %q, want %q", sent.HTML, msg.HTML)
	}
}

// A rejection has to come back as an error, because that is what hands the job
// back to the runner. Swallowing it would mark the message delivered and leave a
// guest with no confirmation and nothing anywhere saying so.
func TestResendReturnsAnErrorCarryingTheProvidersReason(t *testing.T) {
	var req http.Request
	var body []byte
	sender := resendStub(t, http.StatusUnprocessableEntity,
		`{"message":"The bealhouse.com domain is not verified"}`, &req, &body)

	err := sender.Send(context.Background(), "guest@example.com",
		Message{Subject: "s", HTML: "<p>h</p>"})
	if err == nil {
		t.Fatal("Send returned nil for a 422")
	}

	// The reason is the whole value of the failure: it is what an operator reads
	// out of the job's last_error at three in the morning.
	if !strings.Contains(err.Error(), "not verified") {
		t.Errorf("error = %v, want it to carry the provider's message", err)
	}
	if !strings.Contains(err.Error(), "guest@example.com") {
		t.Errorf("error = %v, want it to name the recipient", err)
	}
}

// The API key must never reach a log or an error string, both of which end up in
// the jobs table.
func TestResendKeepsTheKeyOutOfItsErrors(t *testing.T) {
	var req http.Request
	var body []byte
	sender := resendStub(t, http.StatusUnauthorized, `{"message":"Invalid API key"}`, &req, &body)

	err := sender.Send(context.Background(), "guest@example.com",
		Message{Subject: "s", HTML: "<p>h</p>"})
	if err == nil {
		t.Fatal("Send returned nil for a 401")
	}
	if strings.Contains(err.Error(), "re_test_key") {
		t.Errorf("error leaks the API key: %v", err)
	}
}
