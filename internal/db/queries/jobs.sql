-- The durable job queue. One table, polled; no external scheduler.

-- Enqueue work, at most once per unique_key.
--
-- The scans that feed this table run every tick and keep finding the same
-- booking until its charge lands, so enqueueing the same job repeatedly is the
-- normal case rather than a bug to be avoided upstream. Zero rows affected
-- means it is already queued.
--
-- The conflict target repeats the partial index's predicate because that is
-- what makes Postgres infer that index; a job with a NULL unique_key is never
-- deduplicated and can be queued as many times as asked.
-- A NULL run_at means "as soon as possible", and resolves to the database's
-- clock rather than the caller's. One clock has to own scheduling: the claim
-- below compares against now(), and a caller stamping its own timestamp makes
-- "due immediately" depend on the skew between two machines. Inside a
-- transaction it is not even skew — now() is the transaction's start time, so a
-- job stamped a microsecond later by the application is never due at all.
--
-- A caller scheduling genuinely future work — the T-7 charge — passes an
-- absolute time, which is what that argument is for.
-- name: EnqueueJob :execrows
INSERT INTO jobs (kind, run_at, payload, unique_key)
VALUES (
  sqlc.arg(kind),
  coalesce(sqlc.narg(run_at)::timestamptz, now()),
  sqlc.arg(payload),
  sqlc.narg(unique_key)
)
ON CONFLICT (unique_key) WHERE unique_key IS NOT NULL DO NOTHING;

-- Take the next runnable job, leaving it leased rather than deleted.
--
-- FOR UPDATE SKIP LOCKED is what lets several runners poll the same table
-- without coordinating: each claims a different row instead of queueing behind
-- the same one. The row lock lasts only for this statement, so the lease is
-- carried by run_at instead — pushed far enough into the future that nobody
-- else picks the job up while it runs, and near enough that a runner which dies
-- mid-job leaves work that comes back on its own.
--
-- attempts increments here rather than on failure, so a job that panics or
-- takes the process down with it still counts towards its own retry limit.
-- name: ClaimJob :one
UPDATE jobs
SET run_at   = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
  SELECT id FROM jobs
  WHERE run_at <= now()
  ORDER BY run_at
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING id, kind, payload, attempts, unique_key;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = sqlc.arg(id);

-- Push a failed job out to its next attempt and remember why.
-- name: RescheduleJob :exec
UPDATE jobs
SET run_at     = now() + (sqlc.arg(delay_seconds)::int * interval '1 second'),
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id);

-- name: CountJobs :one
SELECT count(*) FROM jobs;
