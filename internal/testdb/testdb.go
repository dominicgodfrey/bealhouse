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

// exclusiveKey namespaces the lock Exclusive takes. Distinct from the 4771 the
// per-room booking lock uses, so the two can never be confused for each other.
const exclusiveKey = 4772

// Exclusive makes destructive test packages take turns.
//
// `go test ./...` runs packages in parallel, and they share one development
// database. Two packages that both empty room_occupancy will otherwise delete
// each other's rows mid-test, which turns a concurrency assertion — the whole
// reason this suite exists — into a coin flip. Tests that write committed rows
// take this first; tests that run inside a rolled-back transaction do not need
// it, because nobody else can see their rows anyway.
//
// A session advisory lock on a dedicated connection, held until the test ends.
func Exclusive(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquiring a connection for the exclusive lock: %v", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", exclusiveKey); err != nil {
		conn.Release()
		t.Fatalf("taking the exclusive lock: %v", err)
	}

	t.Cleanup(func() {
		// Releasing the connection would drop the lock anyway once the pool
		// recycles it, but that is a timing detail to rely on rather than a
		// guarantee, and the next package is waiting.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", exclusiveKey)
		conn.Release()
	})
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

// ResetJobs empties the job queue. The runner's periodic rows are re-created on
// the next start, so this leaves nothing that needed keeping.
func ResetJobs(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(context.Background(), "DELETE FROM jobs"); err != nil {
		t.Fatalf("resetting jobs: %v", err)
	}
}

// ResetBookings removes every booking and the guests left with nothing to their
// name, so a test that commits real bookings does not accumulate them in the
// developer's database.
//
// Deleting a booking cascades to its rooms and to the occupancy holding them,
// which is the direction that keeps this to one statement.
func ResetBookings(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DELETE FROM bookings"); err != nil {
		t.Fatalf("resetting bookings: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"DELETE FROM guests g WHERE NOT EXISTS (SELECT 1 FROM bookings b WHERE b.guest_id = g.id)",
	); err != nil {
		t.Fatalf("resetting guests: %v", err)
	}
}
