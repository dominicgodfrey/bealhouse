# Beal House

Booking engine, marketing site, and admin console for a 7-room inn. One Go binary
serves the JSON API and an embedded React SPA. See [ARCHITECTURE.md](ARCHITECTURE.md)
for the design and the build order this repo follows.

**Status:** steps 1–3 are done and 4–7 are built. What is left is not code:
a Stripe account and the verification matrix that needs one, a Resend account
and its DNS, and the owner's own words and photographs. `STRIPE_FAKE=true`
walks the whole booking journey today without either account.

| Working today | Where |
|---|---|
| The booking flow, search → hold → pay → confirmed | [web/src/routes](web/src/routes) |
| Double-booking prevention | [internal/occupancy](internal/occupancy/occupancy.go) |
| `POST /api/bookings` — books and holds, revalidating server-side | [internal/booking](internal/booking/booking.go) |
| Sellable spans per room, for the date picker | [internal/availability](internal/availability/calendar.go) |
| Deposits, tax, pet fee, refunds | [internal/pricing](internal/pricing/pricing.go) |
| Payments, the ledger and the webhook | [internal/payments](internal/payments) · [internal/gateway](internal/gateway) |
| The eight emails, queued not sent inline | [internal/email](internal/email) |
| Passkey sign-in, no password anywhere | [internal/admin](internal/admin) |
| Everything the owner does once signed in | [internal/console](internal/console) |
| Photograph upload and serving | [internal/media](internal/media) |
| Per-route `<head>`, JSON-LD, sitemap | [internal/httpx/meta.go](internal/httpx/meta.go) |
| Deploying, backups and the restore drill | [deploy/](deploy/README.md) |

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
| Apply migrations | `go tool goose … up`, or `./bin/bealhouse migrate up` — same files |
| Regenerate queries | `go tool sqlc generate` |
| Concurrency, hard | `go test ./internal/occupancy/ ./internal/booking/ ./internal/console/ -count=100` |
| Deploy | `BEAL_HOST=inn@… ./deploy/deploy.sh` — see [deploy/README.md](deploy/README.md) |

The binary carries the migrations as well as the SPA, so `bealhouse migrate up`
brings a database up to the shape that exact binary expects with nothing else
installed beside it. `go tool goose` reads the same files and stays the
convenient thing locally; there is one history, not two.

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs gofmt, vet, the
generated-code check, and the full suite against a real Postgres on every push —
under `-race`, which is the one place it can run, since the development machine
has no C compiler. The 100× concurrency suite runs nightly.

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

Step 8, launch, and most of what is left there is an account or a decision
rather than code — see the checklist at the end of
[deploy/README.md](deploy/README.md). The deploy layer itself is written and the
restore drill has been run.

What genuinely needs an account: `gateway.Stripe` and `email.Resend` are both
written and neither has ever made a request. Add the keys and they are used
automatically, and then the Stripe verification matrix in ARCHITECTURE.md —
test cards, 3-D Secure, `stripe listen`, Test Clocks — which cannot be faked.

What needs the owner: the eight email templates, room descriptions, photographs,
the menu, and the page prose. All of it is editable in the console and all of it
renders as *nothing* until written, rather than as a placeholder somebody has to
remember to delete.

Also outstanding: AVIF (decision #16). Photographs already ship as a ladder of
four widths in JPEG and WebP; AVIF is feasible with no cgo and deferred because
it would move the encoding into a background job for the last slice of the
saving.
