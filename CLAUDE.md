# Working on Beal House

Direct booking engine, marketing site, and admin console for a 7-room inn.
[ARCHITECTURE.md](ARCHITECTURE.md) is the design and the numbered decisions;
[README.md](README.md) is how to run it. Read the decisions table before changing
anything about money, dates, or availability — several were revised after the
document was first written and the revisions are marked.

**Build-order steps 1 and 2 are done. Step 3, the booking flow, is next.**

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
deadlock. If you touch `room_occupancy` or `occupancy.Create`, re-run them hard:

```bash
go test ./internal/occupancy/ -count=100 -timeout 20m
```

## Content is the owner's, not ours

Room descriptions, photos, amenities, and rate seasons are all managed through the
admin console once it exists. What is in the seed is placeholder: descriptions are
marked `PLACEHOLDER`, amenities are empty, there is one flat rate season, and
`web/public/placeholders/*.svg` stands in for photos as a **UI fallback** rather
than seeded rows — a placeholder in the database is one somebody has to remember
to delete. Do not invent content, and do not seed guesses.

## Step 3: the booking flow

Search → results → room page → confirm → hold. Notes before starting:

- **No Stripe account is needed for any of it.** The hold is a `room_occupancy` row
  with an expiry; payment is step 4. The owner wants Stripe deferred as long as
  reasonably possible, so keep step 3 free of it.
- `GET /api/availability` already returns everything a result card needs: beds,
  amenities, photos with a placeholder fallback, per-night prices, and a full
  quote with the pet fee as its own field so the price preview can show the $50
  explicitly.
- The date picker must grey out dates from **valid whole spans per room**, not
  free nights — with seven rooms it is otherwise possible to select a range where
  no single room covers the whole stay (decision #14).
- `POST /api/bookings` must re-validate dates, capacity, min-stay and price
  server-side. The date picker is a convenience, not a security boundary.
- With placeholder content the pages will look skeletal. That is expected; build
  the layout so real content drops in.
