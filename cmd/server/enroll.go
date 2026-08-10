package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/admin"
	"bealhouse/internal/config"
	db "bealhouse/internal/db/gen"
)

// enroll prints a single-use link that enrols one phone in the admin console.
//
// **This is the bootstrap, and it is deliberately the only one.** A console
// with no passkeys cannot be opened by anybody, which is the correct state for
// something guarding every guest's details and the ability to move money — so
// the first way in has to prove something that is not a password. It proves
// shell access to the server: you have to be able to run this binary, on that
// box, against that database.
//
// Every enrollment after the first can be minted from an already-signed-in
// console instead. This stays for the case that matters most and is easiest to
// get wrong in a panic: both phones lost at once.
//
// The token is printed to stdout and nowhere else. It is not logged, because a
// log line is a copy of a credential that outlives the fifteen minutes the
// token is good for.
func enroll(args []string) error {
	label := strings.TrimSpace(strings.Join(args, " "))
	if label == "" {
		label = "New phone"
	}

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("enroll: DATABASE_URL is not set, and the invitation is a row in the database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The relying party is not needed to mint an invitation, but building it
	// here refuses early on a deployment whose SITE_URL would make the console
	// unopenable — better than handing somebody a link that cannot work.
	rp, err := admin.NewRP(cfg.SiteURL, cfg.IsDev())
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}

	console, err := admin.New(rp, db.New(pool), pool)
	if err != nil {
		return err
	}

	invitation, err := console.MintEnrollment(ctx, label)
	if err != nil {
		return err
	}

	site := cfg.SiteURL
	if site == "" {
		site = "http://localhost" + cfg.Addr
	}

	fmt.Fprintf(os.Stdout, `
Open this on the phone you are enrolling, within %s:

  %s

It works once. Label: %s
`, admin.EnrollmentLifetime, admin.EnrollURL(site, invitation.Token), invitation.Label)

	return nil
}
