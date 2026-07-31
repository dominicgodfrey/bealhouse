# Beal House — Architecture & System Design

## Context

Beal House is an inn with 7 unique rooms/suites, a restaurant, and an events business. Today it has
only a placeholder website. The goal is one web property doing three jobs:

1. **Direct booking engine** — date/guest search → room results → room detail → confirm → Stripe →
   confirmation page, PDF, and email.
2. **Marketing site** — home page anchored on booking, restaurant/food + live menu, events page with
   gallery and inquiry form, owner's story.
3. **Admin console** (mobile + desktop) — calendar and list views, manual booking management, date
   blocking, rate management, searchable guest history, and payment status at a glance.

Stack constraint: **TypeScript/React + Go**. No launch deadline — one complete release.

> **Action required before build:** the purchased Bluehost shared hosting **cannot run this
> application** (no long-lived processes, no Go, no Postgres). Bluehost keeps the domain, DNS, and
> mailbox email; the app runs on a ~$6–12/mo VPS with DNS repointed. Nothing but the shared-hosting
> fee is wasted.

---

## Decisions

| # | Decision | Choice |
|---|---|---|
| 1 | Distribution | Direct only. Availability rows carry a `source` field so Airbnb/Booking.com sync is a later adapter, not a rewrite |
| 2 | Hosting | VPS (Hetzner/DO) + Caddy for automatic TLS. Bluehost = domain/DNS/email only |
| 3 | Rendering | Vite React SPA embedded in ONE Go binary via `embed.FS`; Go injects per-route meta + JSON-LD with live DB data |
| 4 | Pricing | Seasonal date-range rates + minimum-stay. Guest count is a **capacity filter only**, never a price input |
| 5 | Rate storage | Materialized nightly calendar `(room_id, date, price_cents, min_stay)` |
| 6 | Payment | Deposit at booking; balance auto-charged off-session at **T-7 days** |
| 7 | Short notice | Arrival < 8 days ⇒ charge **full amount** at booking, no deposit split, no scheduled job |
| 8 | Deposit | First night's rate + its 8.5% tax |
| 9 | Cancellation | ≥7 days: full refund. <7 days: refund all but night one. Deposit **is** the penalty |
| 10 | Multi-room | Schema `booking → booking_rooms` from day one; v1 UI single-room |
| 11 | Out of scope | Table reservations, guest accounts, gift certificates, event booking/deposits |
| 12 | Menu | Structured admin editor (name, description/ingredients, price + course grouping) → JSON-LD `Menu` |
| 13 | Tax | NH Meals & Rooms 8.5%, configurable; **each booking snapshots its rate** for audit |
| 14 | Date picker | No flexible ±1 search. Calendar greys out unselectable dates live |
| 15 | Admin auth | Single shared login, but a real `users` table behind it. **TOTP strongly recommended** |
| 16 | Media | Owner uploads in admin → VPS disk, Go generates AVIF/WebP variants, Cloudflare free CDN |
| 17 | Email | Resend. SPF/DKIM/DMARC at Bluehost DNS (SPF must include Resend *and* the mailbox host) |
| 18 | Launch | Placeholder site today → clean cutover. Google Business Profile + Search Console on day one |
| 19 | Guest self-service | Signed expiring link in confirmation email → view booking + **cancel**, refund executes automatically. Date changes go through the owner |
| 20 | **Minimum stay** | **Global default 2 nights**, stored in `settings` (not hardcoded). A season may override it upward (e.g. 3 on holiday weekends) |
| 21 | **Rate administration** | Seasons are **owner-editable in admin** as a room × season price grid; saving regenerates the nightly calendar for **future dates only** |
| 22 | **Accessibility** | Rooms carry `is_accessible` + structured `accessibility_features[]`. Search exposes an accessibility filter; result cards and room pages show the tag |

### Payment lifecycle

```
book (arrival ≥ 8d) → charge deposit (night 1 + tax), save payment method off-session
T-8 days            → email "you will be charged $X in 24 hours"
T-7 days            → off-session charge of balance
   ├─ success       → email receipt
   └─ failure       → email "you still owe $X, contact the inn" + unmissable admin flag
book (arrival < 8d) → charge full amount, no scheduled job
cancel ≥ 7d out     → refund everything paid
cancel < 7d out     → refund everything except night one + its tax
```

---

## The core architectural bet: the database prevents double-booking

Everything that occupies a room — a confirmed booking, a 15-minute checkout hold, an owner's manual
block — writes to **one table** guarded by a Postgres exclusion constraint:

```sql
CREATE TABLE room_occupancy (
  id          bigserial PRIMARY KEY,
  room_id     bigint NOT NULL REFERENCES rooms(id),
  during      daterange NOT NULL,          -- half-open [check-in, check-out)
  kind        text NOT NULL,               -- 'booking' | 'hold' | 'block'
  source      text NOT NULL DEFAULT 'direct',  -- future: 'airbnb', 'booking.com'
  booking_id  bigint REFERENCES bookings(id) ON DELETE CASCADE,
  expires_at  timestamptz,                 -- holds only
  EXCLUDE USING gist (room_id WITH =, during WITH &&)
);
```

This makes overlapping occupancy **structurally impossible** — not merely unlikely. Two concurrent
checkouts for the last room: one commits, the other gets a constraint violation the API translates
into "just taken, please pick again." No application-level locking, no read-then-write race.

Two details this quietly gets right:

- **Half-open ranges** mean a guest checking out Jun 13 and one checking in Jun 13 do *not* conflict.
  `[10,13)` and `[13,15)` don't overlap. Getting this wrong costs you a sellable night per turnover.
- **Holds are just occupancy rows** with `expires_at`. A guest partway through Stripe genuinely owns
  the room, and the sweeper reclaims abandoned checkouts. Postgres is the only referee.

This single constraint is why **Postgres, not SQLite** — SQLite has no exclusion constraints, and
this is a business handling money.

---

## Stack

**Backend (Go)** — `net/http` + `chi` router · **sqlc** (type-safe Go generated from real SQL, no
ORM) · `goose` migrations · `pgx` · Stripe Go SDK · `go-pdf/fpdf` for confirmations · `slog` logging.

**Frontend (TS/React)** — Vite · React Router · TanStack Query · Tailwind · `react-hook-form` + zod.
Public site and admin are one bundle, admin behind a guarded route (responsive, works on a phone).

**Type safety across the boundary** — Go handlers annotated → OpenAPI spec → `openapi-typescript`
generates the TS client. One binary, one contract, no drift.

**Everything else** — Caddy (auto-TLS, reverse proxy) · Cloudflare free tier (CDN + DNS) · Sentry ·
nightly `pg_dump` + uploads to Backblaze B2 with a *tested* restore.

### Why one binary

`go build` produces a single artifact containing the API, the React build, and the job runner.
Deploy is `scp` + `systemctl restart`. No CORS, no split pipelines, no version skew between
frontend and backend, and the whole system can be run locally with one command.

---

## The job runner

An in-process goroutine polling a durable `jobs` table every 60s with `FOR UPDATE SKIP LOCKED`.
Survives restarts, idempotent by design, no external scheduler.

| Job | Trigger |
|---|---|
| `hold.sweep` | every minute — delete expired `kind='hold'` rows |
| `balance.warn` | T-8 days — "charged in 24 hours" email |
| `balance.charge` | T-7 days — off-session PaymentIntent; on failure mark `payment_failed` + notify |
| `email.send` | queued sends with exponential backoff retry |
| `rates.rebuild` | on season save, and monthly — regenerate the nightly calendar 24 months forward |
| `backup.verify` | weekly — assert last night's dump is non-empty and restorable |

**Email is never sent inline in a request** — it is always queued, so a Resend outage delays
confirmations rather than failing bookings.

---

## Rates & minimum stay (decisions #20, #21)

Two layers: **owner-facing rules** that the admin edits, and a **materialized calendar** the
availability query reads. The owner never touches the calendar directly.

```
settings            default_min_stay = 2, tax_rate = 0.085, hold_ttl_minutes = 15, ...
rate_seasons        id, name, starts_on, ends_on, min_stay (nullable), priority
rate_season_prices  season_id, room_id, price_cents        -- the 7-room × season grid
rate_calendar       room_id, date, price_cents, min_stay   -- generated; what search reads
```

- **Per-room pricing is required, not optional.** The seven rooms are genuinely different, so a
  season sets a price *per room*, not one price for the property. Admin renders this as a grid:
  seasons down, rooms across.
- **`priority` resolves overlaps.** A Thanksgiving season can sit inside a Leaf Season range; the
  higher priority wins for those dates. Without this, overlapping seasons are undefined behaviour
  and the owner gets silently wrong prices.
- **`min_stay` falls back to `settings.default_min_stay` (2)** when a season leaves it null. Seasons
  may raise it; nothing lowers it below the global default without changing that setting.
- **Rebuild is future-only and non-destructive.** `rates.rebuild` regenerates `rate_calendar` from
  today forward. It **cannot** change what a guest already owes: `booking_rooms.nightly_prices`
  snapshots the per-night prices at the moment of booking, and `bookings.tax_rate_snapshot` does the
  same for tax. Editing a season never re-prices a confirmed stay.
- **Admin shows a diff before saving** — "142 nights change price, 0 confirmed bookings affected" —
  so the owner can never silently republish the wrong rate across a season.

The 2-night minimum has teeth in three places: the availability query rejects short spans, the date
picker refuses to let the guest select a 1-night checkout at all (decision #14), and the API
re-validates on `POST /api/bookings` because a client-side calendar is not a security boundary.

---

## Accessibility (decision #22)

```
rooms.is_accessible           boolean   -- drives the filter and the tag
rooms.accessibility_features  text[]    -- 'step_free_entry', 'ground_floor',
                                        -- 'roll_in_shower', 'grab_bars', 'wide_doorway'
```

A bare "accessible" boolean is a **promise a guest plans a trip around**, and a wheelchair user who
arrives to find three steps at the entrance has been genuinely harmed. So the flag drives search and
the tag, while the structured feature list is what actually renders on the room page — a guest can
verify the specific thing they need rather than trusting one word.

- **Search:** `GET /api/availability?...&accessible=true` filters to `is_accessible = true`.
  Presented as a checkbox beside dates and guests, not buried in an "advanced" panel.
- **Results & room page:** accessible rooms carry a visible tag; the room page lists the specific
  features. Fed into JSON-LD via `amenityFeature` so it surfaces in search.
- **Honesty rule:** admin should only let `is_accessible` be set when at least one feature is
  present, and the room page should state what is *not* available where relevant.

---

## Data model (essentials)

```
rooms            id, slug, name, description, max_occupancy, view, amenities[],
                 is_accessible, accessibility_features[], sort_order
room_beds        room_id, bed_type, count
room_photos      room_id, path, alt_text, sort_order
rate_seasons     id, name, starts_on, ends_on, min_stay, priority
rate_season_prices season_id, room_id, price_cents
rate_calendar    room_id, date, price_cents, min_stay      -- materialized from seasons
room_occupancy   (above — bookings, holds, and blocks together)
bookings         code, guest_id, status, checkin, checkout, subtotal_cents, tax_cents,
                 tax_rate_snapshot, deposit_cents, amount_paid_cents, balance_due_cents,
                 balance_charge_at, stripe_customer_id, stripe_payment_method_id
booking_rooms    booking_id, room_id, checkin, checkout, nightly_prices jsonb
payments         booking_id, stripe_payment_intent_id, kind (deposit|balance|refund), amount, status
guests           email, name, phone
guest_notes      guest_id, author_user_id, body, created_at
menu_sections    name, sort_order
menu_items       section_id, name, description, price_cents, is_available
events           title, date, description, photos[]
event_inquiries  name, email, phone, event_date, party_size, message, status
jobs / email_log / users / sessions / settings
```

**Invariants:** money is **integer cents**, never floats. Check-in/check-out are **civil `date`s**,
never timestamps — "is it T-7 yet" resolves in `America/New_York`. Every booking stores the tax rate
and nightly prices *in force when it was made*, so history never shifts under later rate edits.

---

## Booking flow (mapped to the brief)

1. **Search** — `GET /api/availability?checkin&checkout&guests&accessible`. Rooms filtered by
   `max_occupancy >= guests` and optionally `is_accessible`, joined against `room_occupancy` and
   `rate_calendar`. Min-stay is evaluated at the **arrival night**.
2. **Results** — beds, photos, amenities, view, accessibility tag, and **all-in price**
   (`Σ nightly × 1.085`) with the tax broken out beneath. One clear CTA per card.
3. **Room page** — `/rooms/:slug?checkin&checkout`, full gallery + long description, accessibility
   feature list, server-injected `HotelRoom` JSON-LD, book CTA carrying the dates through.
4. **Confirm** — `POST /api/bookings` creates a `pending` booking **and its hold** in one
   transaction, re-validating dates, capacity, min-stay and price server-side. Guest sees room,
   dates, per-night breakdown, tax, deposit due now, and balance due T-7. Countdown shows the hold.
5. **Pay** — Stripe **Payment Element** on our own branded page. PaymentIntent with
   `setup_future_usage: 'off_session'` so the balance can be charged later. Card data never touches
   our server (PCI SAQ-A).
6. **Confirm for real** — the `payment_intent.succeeded` **webhook** promotes hold → booking, not the
   browser redirect. Signature-verified, idempotent by event ID, safe against a guest closing the
   tab. Then: confirmation page with PDF download, guest email with signed manage-booking link, and
   owner notification.

**Date picker (decision #14):** greying out is computed from **valid whole spans per room**, not
free nights, and honours the 2-night minimum. With 7 rooms it would otherwise be possible to select
a range where room A is free early and room B free late but *no single room* covers the stay.
Correct and cheap at this size.

---

## Admin console

Same React bundle under `/admin`, responsive-first so it's genuinely usable one-handed on a phone.

- **Today** — arrivals, departures, in-house.
- **Upcoming reservations** *(explicit requirement)* — every booking with **paid vs. total**, failed
  charges flagged in unmissable red, one-click "send Stripe payment request."
- **Calendar** — 7-row × date-column grid; drag to block, tap a booking to open it. List view
  filterable by room and date range.
- **Booking editor** — manual create (no payment required), modify, cancel with refund preview
  showing the exact computed amount before confirming.
- **Rates** *(decision #21)* — season CRUD with a room × season price grid, per-season min-stay
  override, overlap priority, and a change preview before publishing.
- **Blocking** — paint date ranges per room with a reason.
- **Guests** — searchable by name, email, room, date, length of stay; notes with author and history.
- **Content** — menu editor, room photos/descriptions + accessibility features, events gallery,
  inquiry inbox.
- **Settings** — tax rate, default minimum stay, hold TTL, check-in/out times.

---

## Build order

Dependency-ordered, not deadline-driven (single launch).

1. **Foundation** — repo, Docker Compose Postgres, migrations, sqlc, one Go binary serving Vite,
   Caddy, deploy script.
2. **Domain core** — rooms (incl. accessibility), settings, rate seasons → calendar generator,
   `room_occupancy` + exclusion constraint, availability query. *Concurrency tests here, before any UI.*
3. **Booking flow** — search → results → room page → confirm → hold. No payment yet.
4. **Payments** — Stripe Payment Element, webhooks, deposit/full logic, job runner, T-8/T-7 jobs,
   refunds.
5. **Comms** — Resend, email templates, PDF generation, signed manage-booking link + self-service
   cancel.
6. **Admin** — auth, upcoming/paid-vs-owed view, calendar, list, manual CRUD, rate editor, blocking,
   guest search.
7. **Content & marketing** — home, restaurant + menu editor, events + inquiry form, about, image
   pipeline, JSON-LD injection.
8. **Launch** — backups + restore drill, Sentry, uptime monitoring, DNS cutover, Search Console,
   Google Business Profile, Stripe live keys.

---

## Verification

**Correctness (the parts that cost real money if wrong)**

- **Double-booking:** fire N concurrent `POST /api/bookings` at the last available room; assert
  exactly one succeeds and the rest get a clean "just taken" response.
- **Turnover:** book Jun 10–13 and Jun 13–15 on the same room; both must succeed.
- **Min-stay:** a 1-night query returns nothing anywhere on the calendar (global default is 2), and
  a 2-night query against a 3-night holiday season also returns nothing.
- **Min-stay bypass:** `POST /api/bookings` with a hand-crafted 1-night payload must be rejected
  server-side, not merely hidden by the date picker.
- **Accessibility filter:** `accessible=true` returns only accessible rooms; the tag renders on both
  the result card and the room page.
- **Rate rebuild safety:** confirm a booking, edit the season covering its dates, rebuild, and assert
  the booking's total, nightly prices, and balance are **unchanged**.
- **Hold expiry:** create a hold, advance past TTL, confirm the sweeper frees the room.
- **Money:** assert cents arithmetic against hand-computed totals — 3 nights × $189 + 8.5% tax,
  deposit = night one + its tax, and each refund branch.

**Payments** — Stripe CLI (`stripe listen --forward-to localhost:8080/webhooks/stripe`) for the full
matrix: success, decline, 3DS required, duplicate webhook delivery (must be idempotent), out-of-order
delivery. Critically, use **Stripe Test Clocks** to fast-forward a real test booking through T-8 and
T-7 and verify the warning email, the off-session charge, and the failure path — this is the only
honest way to test decision #6 without waiting a week.

**End-to-end** — Playwright over the full guest journey: search → room → confirm → test card → webhook
→ confirmation page → PDF downloads → email queued. Then the self-service cancel link, asserting the
refund amount matches policy on both sides of the 7-day boundary.

**Manual** — admin console driven entirely on a real phone; run the whole booking flow on mobile;
Lighthouse ≥ 90 on home and room pages; validate JSON-LD in Google's Rich Results Test; confirm
SPF/DKIM/DMARC pass and that confirmations land in a real Gmail inbox, not spam.
