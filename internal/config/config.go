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

	// SiteURL is the public origin, used to build links that leave the site —
	// chiefly the ones in emails, which cannot be relative.
	SiteURL string

	// EmailLogoURL is the letterhead image. Absolute and publicly reachable, or
	// empty: mail clients do not resolve relative paths, and an <img> pointing
	// at nothing is worse than the inn's name in text, which is what the
	// templates fall back to. No logo has been supplied yet.
	EmailLogoURL string

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

	return Config{
		Addr:        env("ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", ""),
		Env:         env("ENV", "dev"),

		StripeSecretKey:      env("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:  env("STRIPE_WEBHOOK_SECRET", ""),
		StripePublishableKey: env("STRIPE_PUBLISHABLE_KEY", ""),

		SiteURL:      env("SITE_URL", ""),
		EmailLogoURL: env("EMAIL_LOGO_URL", ""),

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
