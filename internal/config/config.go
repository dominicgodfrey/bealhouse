// Package config reads process configuration from the environment.
package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Addr        string
	DatabaseURL string
	Env         string

	// Stripe. Absent rather than invalid when the account is not set up yet:
	// the server still boots and the booking flow still holds rooms, exactly
	// the way it does without a DATABASE_URL. Only the endpoints that move
	// money refuse.
	StripeSecretKey      string
	StripeWebhookSecret  string
	StripePublishableKey string

	// StripeFake substitutes a processor that mints ids and takes no money, so
	// the whole booking journey can be walked through before the account
	// exists.
	//
	// Opt-in and nothing else: it is never implied by ENV, because ENV defaults
	// to "dev" and an unconfigured production deploy would otherwise be
	// indistinguishable from a laptop. gateway.New refuses it alongside any
	// real Stripe setting and outside dev; this field only says it was asked
	// for.
	StripeFake bool

	// SiteURL is the public origin, used to build links that leave the site —
	// chiefly the ones in emails, which cannot be relative.
	SiteURL string

	// BookingLinkSecret signs the manage-booking capability in decision #19.
	//
	// Empty leaves confirmation emails without the link and the endpoints behind
	// it refusing every request. Deliberately not defaulted to anything: a
	// generated-at-boot secret would invalidate every outstanding link on each
	// restart, and a compiled-in one would let anyone holding this source cancel
	// any guest's stay.
	BookingLinkSecret string

	// Resend, the mail provider (decision #17). Both halves are required
	// together and neither is useful alone: the key authenticates and the from
	// address has to be one Resend will accept for this domain.
	//
	// Absent rather than invalid when the account is not set up yet, the same
	// way Stripe is. Messages are still rendered and still queued; they are
	// logged instead of delivered.
	ResendAPIKey string
	EmailFrom    string

	// OwnerEmail gets the inn's own copy of every confirmed booking. Empty
	// sends none, which is today's state.
	//
	// Environment rather than a settings column, for now: every other email
	// setting here is, and the admin console that would edit it is build-order
	// step 6. It belongs in settings once that exists — an owner should not
	// need a deploy to change where their notifications go.
	OwnerEmail string

	// EmailLogoURL is the letterhead image. Absolute and publicly reachable, or
	// empty: mail clients do not resolve relative paths, and an <img> pointing
	// at nothing is worse than the inn's name in text, which is what the
	// templates fall back to.
	//
	// Defaulted from SiteURL rather than left to be set by hand, because the
	// asset ships in the bundle this same binary serves — see emailLogoURL. It
	// stays overridable for the day the logo lives on a CDN.
	EmailLogoURL string

	// Web push, so a booking or an inquiry reaches the owner's phone and not
	// only their inbox.
	//
	// Both halves are required together and neither is useful alone: the public
	// key is what the browser subscribes with and the private key is what signs
	// the delivery, and a browser subscribed against one server's public key
	// cannot be reached by another's. Absent rather than invalid when nobody
	// has generated a pair — `bealhouse vapid` prints one — and then
	// notifications are logged instead of sent, exactly as email is without
	// Resend.
	//
	// Keeping these out of the database is deliberate. Rotating them
	// invalidates every existing subscription, so they are deployment
	// configuration rather than a setting somebody can change from a phone and
	// wonder why the notifications stopped.
	PushVAPIDPublicKey  string
	PushVAPIDPrivateKey string

	// PushSubject identifies whoever is responsible for these notifications to
	// the push services, as a mailto: or https: URL. Required by the spec, and
	// in practice how an operator hears they are sending badly before they are
	// blocked for it. Defaulted from OWNER_EMAIL or SITE_URL rather than left
	// to be typed again — see pushSubject.
	PushSubject string

	// SentryDSN is where errors are reported, and empty means nowhere — the log
	// on the box is then the only copy, which is where this started.
	//
	// The key inside a DSN is not a secret: it identifies a project and is
	// designed to be shipped in browser bundles. It is still configuration
	// rather than a constant, because it names which project the inn's errors
	// land in, and a laptop pointed at the live one is noise in the place
	// somebody looks during an outage. Environment tells them apart.
	SentryDSN string

	// MediaDir is where photographs the owner uploads are stored (decision #16).
	//
	// Relative by default, which is right for a laptop and wrong for a server —
	// on the VPS this belongs somewhere that survives a deploy, because the
	// binary is replaced by `scp` and the photographs are not in it. Somewhere
	// like /var/lib/bealhouse/media, and included in the nightly backup, since a
	// `pg_dump` restores the paths and not the files they name.
	MediaDir string

	// BehindProxy says a trusted reverse proxy sits in front of this server and
	// terminates TLS — Caddy, in the deployed shape (decision #2).
	//
	// It decides whether X-Forwarded-For is believed, and so who the rate
	// limiter thinks a request is from. Off by default because the safe
	// mistake is to under-trust: with it wrongly on, anyone can pick their own
	// address and walk around the booking limit; with it wrongly off, everyone
	// behind the proxy shares one bucket and the limit is merely too strict.
	BehindProxy bool
}

// Load reads .env (if present) into the environment, then builds a Config.
// Real environment variables always win over .env, so production deploys can
// set them without a file.
func Load() Config {
	loadDotEnv(".env")

	siteURL := strings.TrimRight(env("SITE_URL", ""), "/")

	return Config{
		Addr:        env("ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", ""),
		Env:         env("ENV", "dev"),

		StripeSecretKey:      env("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:  env("STRIPE_WEBHOOK_SECRET", ""),
		StripePublishableKey: env("STRIPE_PUBLISHABLE_KEY", ""),
		StripeFake:           env("STRIPE_FAKE", "") == "true",

		ResendAPIKey: env("RESEND_API_KEY", ""),
		EmailFrom:    env("EMAIL_FROM", ""),

		SiteURL:           siteURL,
		BookingLinkSecret: env("BOOKING_LINK_SECRET", ""),

		OwnerEmail:   env("OWNER_EMAIL", ""),
		EmailLogoURL: emailLogoURL(env("EMAIL_LOGO_URL", ""), siteURL),

		PushVAPIDPublicKey:  env("PUSH_VAPID_PUBLIC_KEY", ""),
		PushVAPIDPrivateKey: env("PUSH_VAPID_PRIVATE_KEY", ""),
		PushSubject: pushSubject(
			env("PUSH_SUBJECT", ""), env("OWNER_EMAIL", ""), siteURL),

		SentryDSN: env("SENTRY_DSN", ""),

		MediaDir: env("MEDIA_DIR", "media"),

		BehindProxy: env("BEHIND_PROXY", "") == "true",
	}
}

func (c Config) IsDev() bool { return c.Env == "dev" }

// StripeConfigured reports whether money can actually be moved.
//
// Both halves are required and neither is useful alone: the secret key creates
// PaymentIntents, and without the webhook secret nothing could verify that a
// callback claiming one succeeded really came from Stripe. Treating a partial
// configuration as "on" would mean promoting bookings on unverified requests,
// so it is deliberately all or nothing.
func (c Config) StripeConfigured() bool {
	return c.StripeSecretKey != "" && c.StripeWebhookSecret != ""
}

// EmailConfigured reports whether a message can actually leave the building.
//
// All or nothing for the same reason Stripe is: a key with no verified from
// address is rejected on every send, and half a configuration is a mistake to
// report at boot rather than one to discover from a guest who never got their
// confirmation.
func (c Config) EmailConfigured() bool {
	return c.ResendAPIKey != "" && c.EmailFrom != ""
}

// PushConfigured reports whether a notification can actually reach a handset.
//
// All or nothing, like Stripe and Resend: a browser subscribes against the
// public key and only the matching private key can sign a delivery it will
// accept, so half a pair is a mistake to report at boot rather than one to
// discover from an owner who missed a booking.
func (c Config) PushConfigured() bool {
	return c.PushVAPIDPublicKey != "" && c.PushVAPIDPrivateKey != ""
}

// pushSubject resolves who the push services should complain to.
//
// An explicit PUSH_SUBJECT wins. Otherwise the inn's own notification address,
// which is already configured and already a person who reads mail about this
// system; failing that the site itself, which the spec allows and which at
// least resolves to somebody. Empty only on a deployment with none of the
// three, and then push is not configured anyway.
func pushSubject(configured, ownerEmail, siteURL string) string {
	switch {
	case configured != "":
		return configured
	case ownerEmail != "":
		return "mailto:" + ownerEmail
	case siteURL != "":
		return siteURL
	default:
		return ""
	}
}

// EmailLogoPath is where the letterhead image sits in the SPA bundle this
// binary serves. A PNG rather than the SVG the site uses: mail clients do not
// render SVG, and several strip the tag rather than showing a broken image.
const EmailLogoPath = "/logo-email.png"

// emailLogoURL resolves the letterhead image.
//
// An explicit EMAIL_LOGO_URL always wins — the logo may one day be served from
// the CDN rather than from here. Otherwise it is built from SiteURL, because
// the asset is in the bundle this binary serves and making an operator retype
// their own origin is a way to get a typo into every email the inn sends.
//
// Without a SiteURL there is nothing to make absolute, so it stays empty and
// the templates fall back to the inn's name in text. A relative path would
// render as a broken image in every client, which is worse than no image.
func emailLogoURL(configured, siteURL string) string {
	if configured != "" {
		return configured
	}
	if siteURL == "" {
		return ""
	}
	return siteURL + EmailLogoPath
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv parses KEY=value lines. Missing file is not an error.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
