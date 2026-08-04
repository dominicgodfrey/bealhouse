# Beal House

Booking engine, marketing site, and admin console for a 7-room inn. One Go binary
serves the JSON API and an embedded React SPA. See [ARCHITECTURE.md](ARCHITECTURE.md)
for the design and the build order this repo follows.

**Status:** build-order step 1 (foundation) complete. No domain logic yet.

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
cmd/server/           entrypoint: config, DB pool, HTTP server, graceful shutdown
internal/config/      environment + .env loading
internal/httpx/       chi router, /api/health, SPA serving with history fallback
internal/db/migrations/  goose SQL migrations
internal/db/queries/  hand-written SQL; sqlc generates Go into internal/db/gen
web/                  Vite + React + Tailwind SPA
web/embed.go          embeds web/dist into the binary
```

`web/dist/` is committed with an empty `.gitkeep` because `//go:embed all:dist`
will not compile against a missing directory. Vite's `emptyOutDir` deletes it on
every build, so a small plugin in `vite.config.ts` writes it back.

## Not yet built

Steps 2–8 of the build order: rooms and rates, the occupancy table and its
exclusion constraint, availability, booking, Stripe, email, admin, and content.
Step 2 is next, and its concurrency tests come before any UI.
