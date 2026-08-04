// Package occupancy guards the single table that decides whether a room is
// free: confirmed bookings, checkout holds, and owner blocks all live there
// together under one exclusion constraint.
package occupancy

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

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

	// SQLSTATE 40P01, deadlock_detected. Several transactions inserting
	// overlapping ranges at once wait on each other's as-yet-uncommitted rows,
	// and Postgres can find a cycle among the waiters and abort one to break it.
	// It means "try again", not "the room is taken", and it is the documented
	// retryable class.
	deadlockDetected = "40P01"
)

// maxCreateAttempts bounds deadlock retries. Contention here is between a
// handful of guests for one of seven rooms, so a cycle that survives four
// retries is a bug worth surfacing rather than hiding behind more retries.
const maxCreateAttempts = 5

// Translate converts an exclusion-constraint violation into ErrRoomTaken and
// passes every other error through untouched.
//
// The distinction matters: losing the race is expected and gets a friendly
// message, while a connection failure or a genuine bug must not be disguised as
// "someone else booked it".
func Translate(err error) error {
	if hasCode(err, exclusionViolation) {
		return ErrRoomTaken
	}
	return err
}

// Create claims a room, retrying when Postgres breaks a deadlock at our
// expense.
//
// Callers should use this rather than the generated query directly. A raw
// insert surfaces deadlocks as opaque failures, which under concurrent checkout
// means a guest sees an error page for what is really "try that again" — and,
// worse, cannot tell it apart from the room genuinely being gone.
func Create(ctx context.Context, q *db.Queries, arg db.CreateOccupancyParams) (int64, error) {
	var err error
	for attempt := range maxCreateAttempts {
		var id int64
		id, err = q.CreateOccupancy(ctx, arg)
		if err == nil {
			return id, nil
		}
		if !hasCode(err, deadlockDetected) {
			return 0, Translate(err)
		}

		// Jittered backoff. Without the jitter, the losers of one deadlock
		// retry in lockstep and deadlock with each other again.
		delay := time.Duration(1<<attempt)*time.Millisecond + time.Duration(rand.IntN(2000))*time.Microsecond
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(delay):
		}
	}
	return 0, err
}

func hasCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
