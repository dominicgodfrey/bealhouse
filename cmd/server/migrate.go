package main

import (
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver, for goose only
	"github.com/pressly/goose/v3"

	"bealhouse/internal/config"
	"bealhouse/internal/db/migrations"
)

// migrate runs the embedded migrations against DATABASE_URL.
//
// The same files `go tool goose` reads locally, so there is one history and not
// two. What this adds is that they travel *inside* the binary: a deploy copies
// one file to the server and that file knows how to bring the database up to
// the shape it expects. Nothing else has to be installed there, and the code
// and the schema cannot come from different commits.
//
// It is a separate command rather than something `run` does at startup. A
// migration is a change to shared state that can fail halfway, take a lock, or
// need somebody watching it — and a server that migrates on boot does all of
// that during a restart, with the site down and the output going wherever the
// logs go. Worse, two instances starting together would both try.
func migrate(args []string) error {
	action := "up"
	if len(args) > 0 {
		action = args[0]
	}

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		return errors.New("migrate: DATABASE_URL is not set")
	}

	// database/sql purely because that is goose's interface. Everything else in
	// this binary talks to Postgres through pgx directly; the stdlib adapter is
	// the same driver underneath.
	conn, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrate: opening the database: %w", err)
	}
	defer conn.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	switch action {
	case "up":
		err = goose.Up(conn, ".")
	case "status":
		err = goose.Status(conn, ".")
	case "version":
		err = goose.Version(conn, ".")
	case "down":
		// One step, and never in a deploy script. Rolling back a migration on a
		// database with real bookings in it is a decision somebody makes at a
		// prompt, having read what the down step actually drops.
		err = goose.Down(conn, ".")
	default:
		return fmt.Errorf("migrate: unknown action %q; want up, down, status or version", action)
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", action, err)
	}
	return nil
}
