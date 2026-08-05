# Beal House

Booking engine, marketing site, and admin console for a 7-room inn. One Go binary
serves the JSON API and an embedded React SPA. See [ARCHITECTURE.md](ARCHITECTURE.md)
for the design and the build order this repo follows.

**Status:** build-order steps 1–3 complete — foundation, the domain core, and
the booking flow end to end: search, results, room page, confirm, and a real
hold on the room. No payment yet, so none of it needs a Stripe account.

| Working today | Where |
|---|---|
| The booking flow, search → hold | [web/src/routes](web/src/routes) |
| `GET /api/availability` · `/calendar` · `/rooms/{slug}` | [internal/httpx](internal/httpx) |
| `POST /api/bookings` — books and holds, revalidating server-side | [internal/booking](internal/booking/booking.go) |
| Double-booking prevention | [internal/occupancy](internal/occupancy/occupancy.go) |
| Sellable spans per room, for the date picker | [internal/availability](internal/availability/calendar.go) |
| Seasons → nightly calendar | [internal/rates](internal/rates/rates.go) |
| Deposits, tax, pet fee, refunds | [internal/pricing](internal/pricing/pricing.go) |

## Prerequisites

| Tool | Notes |
|---|---|
| Go 1.26+ | `goose` and `sqlc` come from `go tool`, so nothing extra to install |
| Node 22+ | for the Vite build |
| Docker | local Postgres via `docker compose` |
| GNU make | optional — every target is a one-line command, listed below |

## First run

```bash
cd web && npm install && npm run build && cd ..
go build -o bin/bealhouse ./cmd/server
./bin/bealhouse
```

Then open <http://localhost:8080>. `/api/health` reports whether the database is
reachable; it stays `200 OK` either way so the binary is diagnosable when
Postgres is down.

With make: `make dev`.

## Database

```bash
docker compose up -d postgres
cp .env.example .env
go tool goose -dir internal/db/migrations postgres "$DATABASE_URL" up
```

The app boots without `DATABASE_URL` — health just reports `db: not_configured`.
That is deliberate, so frontend work does not require Postgres running.

**Postgres, not SQLite**, because `room_occupancy` relies on a GiST exclusion
constraint to make double-booking structurally impossible. Migration 00001
installs `btree_gist` for exactly that reason.

## Behind a proxy

`POST /api/bookings` is rate limited per client address, because it is anonymous
and every call takes a room off sale for the hold TTL. **Deployed behind Caddy,
set `BEHIND_PROXY=true`** — otherwise every guest looks like the proxy, shares one
bucket, and legitimate traffic starts seeing 429s. Locally it stays unset: the
header it enables is only trustworthy when something trusted sets it.

## Day-to-day

| Task | Command |
|---|---|
| Frontend with HMR | `cd web && npm run dev` → <http://localhost:5173> (proxies `/api` to :8080) |
| Full binary | `make dev` |
| Go tests | `go test ./...` |
| New migration | `go tool goose -dir internal/db/migrations -s create <name> sql` |
| Regenerate queries | `go tool sqlc generate` — errors until step 2 adds the first `.sql` query |

For frontend work, run the Go binary and `npm run dev` side by side. For anything
touching the API, rebuild the binary — the SPA it serves is the one embedded at
compile time.

## Layout

```
cmd/server/              entrypoint: config, DB pool, HTTP server, hold sweeper
internal/config/         environment + .env loading
internal/httpx/          chi router, JSON API, SPA serving with history fallback
internal/availability/   the search: capacity, pets, occupancy, rates, min stay;
                         and the sellable spans the date picker greys from
internal/booking/        a booking and its hold, in one transaction
internal/occupancy/      the exclusion constraint and its error translation
internal/rates/          seasons to nightly calendar
internal/pricing/        integer-cent money: deposits, tax, pet fee, refunds
internal/civil/          the inn's calendar in America/New_York
internal/testdb/         test helpers: real Postgres, rolled-back transactions
internal/db/migrations/  goose SQL migrations
internal/db/queries/     hand-written SQL; sqlc generates Go into internal/db/gen
internal/db/seed/        the seven rooms and a placeholder rate season
web/src/lib/             the API client, civil dates, money, span rules
web/src/routes/          search, results, room, confirm, held
web/embed.go             embeds web/dist into the binary
```

Dates in the frontend are `YYYY-MM-DD` strings, never `Date` objects. A `Date`
is an instant, and the moment one is used for a check-in the guest's browser
timezone starts deciding what day it is — someone in Los Angeles opening the
picker at 9pm would be offered yesterday. `web/src/lib/dates.ts` mirrors
`internal/civil` on the server.

Tests need Postgres running and seeded; they skip cleanly when it is not
reachable. Tests that rewrite reference data run inside a rolled-back
transaction, so `go test ./...` never leaves the dev database altered.

`web/dist/` is committed with an empty `.gitkeep` because `//go:embed all:dist`
will not compile against a missing directory. Vite's `emptyOutDir` deletes it on
every build, so a small plugin in `vite.config.ts` writes it back.

## Content ownership

Room descriptions, photos, amenities, and rate seasons are all owner-managed
through the admin console. What is in the seed is placeholder: descriptions are
marked as such, amenities are empty, and there is a single flat rate season.
None of it should be edited in SQL once admin exists.

## Not yet built

Steps 4–8 of the build order: Stripe, email and PDFs, the admin console,
marketing content, and launch.

The booking flow stops at a held room. The hold is real — the exclusion
constraint enforces it against everyone else, and an in-process sweeper
reclaims it a minute after it lapses — but there is no way to pay for it yet,
which is exactly where step 4 begins.
