package email

import (
	"strings"
	"testing"
)

// A preview has to render every message, or the console offers a button that
// fails on the copy somebody is in the middle of writing.
//
// Against Sample rather than nil: the bodies are guarded on .Data so that the
// nil smoke test in TestEveryTemplateRenders can reach them at all, so a nil
// preview would prove only that the layout works.
func TestEveryMessagePreviews(t *testing.T) {
	r := renderer(t, Brand{InnName: "The Beal House", SiteURL: "https://example.test"})

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			subject, body, err := r.shipped(name)
			if err != nil {
				t.Fatalf("reading the shipped copy: %v", err)
			}

			msg, err := r.Preview(name, subject, body)
			if err != nil {
				t.Fatalf("previewing: %v", err)
			}
			if strings.TrimSpace(msg.Subject) == "" {
				t.Error("the preview has no subject")
			}
			if !strings.Contains(msg.HTML, "<!doctype html>") {
				t.Error("the preview was not wrapped in the shared layout")
			}
			if !strings.Contains(msg.HTML, "Sample Guest") &&
				!strings.Contains(msg.HTML, "SAMPLE") {
				t.Error("the preview did not render the sample booking")
			}
		})
	}
}

// Every field of every sample payload is filled, including the optional ones.
//
// An empty ManageURL or PayURL would render the template without its button and
// teach the owner there isn't one — and the branch that only appears when a
// field is set is the interesting half of these templates.
func TestTheSampleFillsTheOptionalFields(t *testing.T) {
	r := renderer(t, Brand{InnName: "The Beal House", SiteURL: "https://example.test"})

	for name, want := range map[string]string{
		BookingConfirmation: "View or cancel your booking",
		PaymentRequest:      "Pay ",
	} {
		subject, body, err := r.shipped(name)
		if err != nil {
			t.Fatalf("%s: reading the shipped copy: %v", name, err)
		}
		msg, err := r.Preview(name, subject, body)
		if err != nil {
			t.Fatalf("%s: previewing: %v", name, err)
		}
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("%s: the preview is missing its button (%q)", name, want)
		}
	}
}

// Copy that will not compile has to fail in front of the person who typed it,
// not at send time — which is after a guest's card has been charged.
func TestAPreviewRefusesCopyThatWillNotRender(t *testing.T) {
	r := renderer(t, Brand{InnName: "The Beal House"})

	if _, err := r.Preview(BookingConfirmation, "Hello", "{{.Data.Code"); err == nil {
		t.Fatal("an unterminated action previewed without an error")
	}
}
