// Package testdb connects tests to a real Postgres.
//
// The concurrency guarantees this project depends on are properties of Postgres
// itself — an exclusion constraint, a GiST index, MVCC — so there is nothing
// worth testing against a fake. Tests that cannot reach a database skip rather
// than fail, so `go test ./...` still works on a machine without Docker.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultURL = "postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable"

// Connect returns a pool for the local development database, or skips the test
// when none is reachable. The pool is closed when the test finishes.
func Connect(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultURL
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("no test database (%v); run `docker compose up -d postgres`", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("no test database (%v); run `docker compose up -d postgres`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

// Tx starts a transaction that is always rolled back when the test ends.
//
// Tests that need to rewrite reference data — replacing every rate season, say —
// use this so they can do so freely without leaving the developer's database in
// a state that later confuses the running application.
func Tx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("beginning test transaction: %v", err)
	}
	t.Cleanup(func() {
		// Rollback on an already-finished transaction is a no-op error we do
		// not care about.
		_ = tx.Rollback(context.Background())
	})
	return tx
}

// ResetOccupancy empties room_occupancy so a test starts from a known state.
// Rooms and settings are left alone: they are seeded reference data, not
// fixtures.
func ResetOccupancy(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(context.Background(), "DELETE FROM room_occupancy"); err != nil {
		t.Fatalf("resetting occupancy: %v", err)
	}
}
