package email

import (
	"context"
	"strings"
	"testing"
)

func renderer(t *testing.T, brand Brand) *Renderer {
	t.Helper()
	r, err := New(brand, nil)
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}
	return r
}

// Every template has to render, or a send fails at the moment it matters most —
// after a guest's card has already been charged.
func TestEveryTemplateRenders(t *testing.T) {
	r := renderer(t, Brand{SiteURL: "https://example.test"})

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			msg, err := r.Render(context.Background(), name, nil)
			if err != nil {
				t.Fatalf("rendering: %v", err)
			}
			if strings.TrimSpace(msg.Subject) == "" {
				t.Error("no subject; it would send with an empty subject line")
			}
			if !strings.Contains(msg.HTML, "<!doctype html>") {
				t.Error("the body was not wrapped in the shared layout")
			}
			if !strings.Contains(msg.HTML, "Beal House") {
				t.Error("the letterhead is missing")
			}
		})
	}
}

// The logo is the owner's asset and does not exist yet. Until it does, the
// letterhead has to be the inn's name in text — never a broken image, which is
// also what a guest with images switched off sees.
func TestLetterheadFallsBackToTextWithoutALogo(t *testing.T) {
	r := renderer(t, Brand{})

	msg, err := r.Render(context.Background(), BookingConfirmation, nil)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if strings.Contains(msg.HTML, "<img") {
		t.Error("an <img> was rendered with no logo configured; it would show as a broken image")
	}
	if !strings.Contains(msg.HTML, "Beal House") {
		t.Error("no text letterhead to fall back on")
	}
}

func TestLogoIsUsedWhenConfigured(t *testing.T) {
	const logo = "https://example.test/brand/logo.png"
	r := renderer(t, Brand{LogoURL: logo, SiteURL: "https://example.test"})

	msg, err := r.Render(context.Background(), BookingConfirmation, nil)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !strings.Contains(msg.HTML, logo) {
		t.Error("the configured logo is not in the message")
	}
	// Alt text matters here: it is what a guest sees before they choose to load
	// images, which for a first email from an unknown sender is most of them.
	if !strings.Contains(msg.HTML, `alt="The Beal House"`) {
		t.Error("the logo has no alt text")
	}
}

func TestUnknownTemplateIsAnError(t *testing.T) {
	r := renderer(t, Brand{})

	if _, err := r.Render(context.Background(), "nonexistent", nil); err == nil {
		t.Error("rendering an unknown template succeeded")
	}
}

// The inn's name is a default rather than a requirement, so a caller that
// forgets it still sends something with a letterhead.
func TestInnNameDefaults(t *testing.T) {
	r := renderer(t, Brand{})
	if r.brand.InnName != "The Beal House" {
		t.Errorf("inn name %q, want the default", r.brand.InnName)
	}

	custom := renderer(t, Brand{InnName: "The Old Rectory"})
	if custom.brand.InnName != "The Old Rectory" {
		t.Errorf("inn name %q, want the supplied one", custom.brand.InnName)
	}
}
