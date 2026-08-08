// Package occupancy guards the single table that decides whether a room is
// free: confirmed bookings, checkout holds, and owner blocks all live there
// together under one exclusion constraint.
package occupancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	db "bealhouse/internal/db/gen"
)

// ErrRoomTaken reports that another transaction committed overlapping occupancy
// first. This is an ordinary outcome of two guests wanting the same room, not a
// failure: the caller should tell the guest to pick again, not retry blindly.
var ErrRoomTaken = errors.New("occupancy: room is already taken for those dates")

const (
	// SQLSTATE 23P01, exclusion_violation, raised by the EXCLUDE constraint on
	// room_occupancy. This is the clean "someone got there first" outcome.
	exclusionViolation = "23P01"

	// SQLSTATE 40P01, deadlock_detected. Transactions waiting on each other's
	// locks form a cycle and Postgres aborts one to break it. The advisory lock
	// in CreateOccupancy is what stops claims doing this to each other; what is
	// left is a transaction that claims more than one room, which nothing in the
	// application does. It means "run the whole transaction again", not "the
	// room is taken".
	deadlockDetected = "40P01"

	// SQLSTATE 25P02, in_failed_sql_transaction. Not a failure of this
	// statement at all: the transaction was already aborted, so Postgres
	// refused to run anything more in it. The error worth reading is the
	// earlier one that aborted it.
	inFailedTransaction = "25P02"
)

// Translate converts Postgres's account of a failed claim into this package's.
//
// Losing the race is expected and gets a friendly message; a connection failure
// or a genuine bug must not be disguised as "someone else booked it". The two
// deadlock-shaped codes are passed on carrying a sentence about what they mean,
// because both were read as the wrong thing once already: a deadlock names
// itself as retryable, and an aborted transaction says out loud that the cause
// is upstream of here.
func Translate(err error) error {
	switch {
	case hasCode(err, exclusionViolation):
		return ErrRoomTaken
	case hasCode(err, deadlockDetected):
		return fmt.Errorf("occupancy: deadlocked claiming the room, so the whole transaction has to run again: %w", err)
	case hasCode(err, inFailedTransaction):
		return fmt.Errorf("occupancy: the transaction was already aborted before this claim ran, so an earlier statement failed and that error is the one to read (a deadlock, 40P01, is the usual one): %w", err)
	}
	return err
}

// Create claims a room.
//
// Callers should use this rather than the generated query directly: the query
// takes the per-room advisory lock that stops concurrent claims deadlocking,
// and Translate turns the exclusion violation its loser gets into ErrRoomTaken
// rather than an opaque failure a guest cannot tell apart from the room being
// genuinely gone.
//
// It does not retry a deadlock, and that is deliberate. It used to, from before
// the advisory lock existed and two claims on one room could still deadlock over
// each other. Once the lock made that impossible, the retry had nowhere left to
// work:
//
//   - Inside a transaction it is a no-op that hides the cause, and that is where
//     the claims that matter happen — booking.Create writes the booking and its
//     hold in one, payments re-claims inside a savepoint. A deadlock has already
//     aborted the transaction, so the retry could only ever come back 25P02, and
//     the caller was handed "current transaction is aborted" in place of the
//     deadlock that caused it. That reads as some unrelated statement having
//     failed earlier, and it sent the diagnosis of a test-suite flake a long way
//     in the wrong direction.
//   - Outside one there is nothing left for it to fix. The console's block is
//     this statement on its own connection, and the advisory lock is what keeps
//     concurrent claims on a room out of each other's way — it is the fix the
//     retry was superseded by, not a companion to it.
//
// Making the first case work would mean a SAVEPOINT around every claim, and the
// retry inside it would only wait out deadlock_timeout again against a lock the
// transaction it deadlocked with is still holding. So a deadlock goes back
// whole, to whoever owns the transaction, since out there is the only place the
// work can run again — and everyone out there can: the jobs runner re-runs its
// job, a guest's request is one they can send again, and the owner blocking a
// room is a person looking at a button.
func Create(ctx context.Context, q *db.Queries, arg db.CreateOccupancyParams) (int64, error) {
	id, err := q.CreateOccupancy(ctx, arg)
	if err != nil {
		return 0, Translate(err)
	}
	return id, nil
}

func hasCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
