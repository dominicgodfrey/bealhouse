package config

import "testing"

// The letterhead URL is the one setting whose wrong answer is invisible here and
// obvious in a guest's inbox: a relative path renders as a broken image in every
// mail client. So the rule under test is that this function returns an absolute
// URL or nothing at all — never a path.
func TestEmailLogoURL(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		siteURL    string
		want       string
	}{
		{
			name:    "defaults to the bundled asset on the site's own origin",
			siteURL: "https://bealhouse.com",
			want:    "https://bealhouse.com/logo-email.png",
		},
		{
			name:       "an explicit setting wins, for the day it moves to a CDN",
			configured: "https://cdn.example.com/beal/letterhead.png",
			siteURL:    "https://bealhouse.com",
			want:       "https://cdn.example.com/beal/letterhead.png",
		},
		{
			// The templates fall back to the inn's name in text, which reads.
			// A bare "/logo-email.png" would not.
			name: "no origin means no image rather than a relative one",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := emailLogoURL(c.configured, c.siteURL); got != c.want {
				t.Errorf("emailLogoURL(%q, %q) = %q, want %q",
					c.configured, c.siteURL, got, c.want)
			}
		})
	}
}

// SITE_URL is typed by hand into a deploy, so it arrives with a trailing slash
// often enough that joining it naively would produce "…com//logo-email.png".
func TestLoadTrimsTheSiteURLsTrailingSlash(t *testing.T) {
	t.Setenv("SITE_URL", "https://bealhouse.com/")
	t.Setenv("EMAIL_LOGO_URL", "")

	cfg := Load()

	if cfg.SiteURL != "https://bealhouse.com" {
		t.Errorf("SiteURL = %q, want the slash trimmed", cfg.SiteURL)
	}
	if want := "https://bealhouse.com/logo-email.png"; cfg.EmailLogoURL != want {
		t.Errorf("EmailLogoURL = %q, want %q", cfg.EmailLogoURL, want)
	}
}
