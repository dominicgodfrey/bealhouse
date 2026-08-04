# Working on Beal House

Direct booking engine, marketing site, and admin console for a 7-room inn.
[ARCHITECTURE.md](ARCHITECTURE.md) is the design and the numbered decisions;
[README.md](README.md) is how to run it. Read the decisions table before changing
anything about money, dates, or availability — several were revised after the
document was first written and the revisions are marked.

**Build-order steps 1, 2 and 3 are done. Step 4, payments, is next.**

## Local setup

Postgres runs in Docker; the app does not.

```bash
docker compose up -d postgres
go tool goose -dir internal/db/migrations postgres "postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable" up
```

Then load the seed — **tests fail without it**, since they assert against the
seven real rooms and the placeholder rate season:

```bash
docker compose exec -T postgres psql -U bealhouse -d bealhouse -v ON_ERROR_STOP=1 -f - < internal/db/seed/rooms.sql
```

...and the same for `internal/db/seed/rates.sql`.

Build and run:

```bash
cd web && npm install && npm run build && cd .. && go build -o bin/bealhouse ./cmd/server
```

The binary needs `DATABASE_URL` set (or a `.env`) for `/api/availability`; without
it the server still boots and reports `db: not_configured`.

## Environment quirks on this machine

- **`make` is not installed.** The Makefile is accurate but you must run the
  underlying commands, or install make.
- **Go and Docker may be missing from a shell's PATH** even though both are on the
  persisted PATH — shells started before installation carry a stale copy. Prefix
  with `C:\Program Files\Go\bin` and
  `C:\Program Files\Docker\Docker\resources\bin` if a command is not found.
- **`go test -race` does not work** — it needs cgo and there is no C compiler here.
  Stress concurrency with `-count=N` instead.
- **Do not rewrite Go or SQL files with PowerShell string replacement.** It mangles
  UTF-8; em-dashes in comments came back as mojibake. Use the Edit tool, and run
  `gofmt -l .` afterwards either way.

## Conventions that matter

- **Money is integer cents. Never floats, anywhere.** The tax rate crosses the
  database boundary pre-scaled to hundred-thousandths (`pricing.Rate`) so no
  numeric is ever decoded into a float64.
- **Two different date conventions, deliberately.** `room_occupancy.during` is
  half-open `[check-in, check-out)`, so the checkout date is not a night and
  same-day turnovers do not collide. Rate seasons use **inclusive** `ends_on`,
  because that is what an owner means by "Jun 1 to Aug 31". Every date in the
  rates schema is a night. Mixing these up is the expensive mistake here.
- **Civil dates, not instants.** Use `internal/civil` for anything calendar-shaped;
  it resolves in America/New_York with embedded tzdata.
- **The database enforces the invariants**, not the handlers: the exclusion
  constraint, the accessibility honesty rule, the pet-fee rule, hold expiry
  agreement. Add new invariants there too where possible.
- **Claiming a room goes through `occupancy.Create`**, never `q.CreateOccupancy`
  directly. It takes the per-room advisory lock that stops deadlocks and
  translates `23P01` into `ErrRoomTaken`. See the concurrency section in
  ARCHITECTURE.md for why this is not optional.

## After editing SQL

```bash
go tool sqlc generate
```

Queries live in `internal/db/queries/`, migrations in `internal/db/migrations/`,
and generated Go lands in `internal/db/gen/` — never edit the generated files.
Range types are built and unpacked inside SQL so Go only ever passes plain dates.

New migration:

```bash
go tool goose -dir internal/db/migrations -s create some_name sql
```

## Testing

Tests hit a real Postgres and skip cleanly when one is not reachable — the
guarantees being tested are properties of Postgres, so there is nothing worth
faking. Tests that rewrite reference data run inside a rolled-back transaction
(`testdb.Tx`), so `go test ./...` never leaves the dev database altered.

Concurrency tests are the reason step 2 was built before any UI. They found a real
deadlock. If you touch `room_occupancy`, `occupancy.Create` or `booking.Create`,
re-run them hard:

```bash
go test ./internal/occupancy/ ./internal/booking/ -count=100 -timeout 20m
```

**Two rules keep the packages from tripping over each other.** `go test ./...` runs
them in parallel against one shared database, so:

- A test that **commits** rows takes `testdb.Exclusive` first. Otherwise another
  package's `DELETE FROM room_occupancy` lands mid-race and turns a concurrency
  assertion into a coin flip.
- Each package books in **its own stretch of calendar** — occupancy uses fixed 2027
  and 2028 dates, availability uses today+120 to +150, booking uses today+200. A
  committed booking inside another package's window silently breaks that package.

## Content is the owner's, not ours

Room descriptions, photos, amenities, and rate seasons are all managed through the
admin console once it exists. What is in the seed is placeholder: descriptions are
marked `PLACEHOLDER`, amenities are empty, there is one flat rate season, and
`web/public/placeholders/*.svg` stands in for photos as a **UI fallback** rather
than seeded rows — a placeholder in the database is one somebody has to remember
to delete. Do not invent content, and do not seed guesses.

## The booking flow, as built

Search → results → room page → confirm → hold, with no Stripe anywhere in it.

- **A booking and its hold are written in one transaction** by `booking.Create`.
  The hold is a `room_occupancy` row with an expiry, so the exclusion constraint
  guards a checkout in progress exactly the way it guards a confirmed stay.
- **The booking path re-runs the availability query rather than trusting the
  client.** Capacity, pets, occupancy, rate coverage and min-stay are therefore
  re-checked by the same SQL that produced the search results — one rule set, not
  two that drift. A hand-crafted one-night payload is refused.
- That check is not what claims the room. A concurrent booker can pass it a moment
  later; the exclusion constraint still decides at the insert. `booking.IsUnavailable`
  covers both outcomes, which are different internally and identical to a guest.
- **`GET /api/calendar` is what the date picker greys from**: unbroken runs of
  sellable nights per room, each night carrying its minimum stay. Not free nights —
  with seven rooms their union contains stays no single room covers (decision #14).
- **Frontend dates are `YYYY-MM-DD` strings, never `Date` objects**, for the same
  reason the server uses `internal/civil`. See `web/src/lib/dates.ts`.
- The API returns `[]` and never `null` for an empty list. A room with no photos
  crashed the results page once already; there is a test that fails if a null
  reaches the wire.
- **`booking.RunSweeper` is a plain ticker, not the job runner.** It reclaims
  lapsed holds so an abandoned checkout cannot take a room off sale forever. Step 4
  folds it into the durable `jobs` table.

## Step 4: payments

- The `bookings` money columns are **snapshots, not a ledger**: `deposit_cents` and
  `balance_due_cents` describe how the quote splits and never change again. What is
  still collectable comes from `amount_paid_cents` and `status`. Two CHECK
  constraints depend on that staying true, so record payments rather than rewriting
  the split.
- `balance_charge_at` being NULL is the short-notice flag (decision #7), not a
  missing value: those stays are charged in full at booking and have no T-7 job.
- `pricing.ChargeAtBooking`, `Penalty` and `Refund` are already written and tested,
  including the case where the T-7 charge failed and the refund must not go
  negative.
- Promotion from hold to booking belongs to the `payment_intent.succeeded`
  **webhook**, not the browser redirect (decision, step 6 of the booking flow).
