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
| 2 | Hosting | **Hetzner Cloud CX22, Ashburn VA (~$5/mo)** + Caddy for automatic TLS. Bluehost = domain/DNS/email only. See *VPS* below |
| 3 | Rendering | Vite React SPA embedded in ONE Go binary via `embed.FS`; Go injects per-route meta + JSON-LD with live DB data |
| 4 | Pricing | Seasonal date-range rates + minimum-stay. Guest count is a **capacity filter only**, never a price input |
| 5 | Rate storage | Materialized nightly calendar `(room_id, date, price_cents, min_stay)` |
| 6 | Payment | Deposit at booking; balance auto-charged off-session at **T-7 days** |
| 7 | Short notice | Arrival < 8 days ⇒ charge **full amount** at booking, no deposit split, no scheduled job |
| 8 | Deposit | **50% of the all-in total** (room + pet fee + tax), rounded up. Balance = total − deposit, so the two always reconcile *(revised; was first night + tax)* |
| 9 | Cancellation | ≥7 days: full refund **less the card processor's cut** (see #26). <7 days: **50% refund** — the forfeit is exactly the deposit *(revised twice)* |
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
| 22 | **Accessibility** | **Filter switched off.** Every room requires stairs, including the two the owner considers most accessible, so no room sets `is_accessible`. The schema and its constraint remain; `settings.accessibility_notice` carries a stairs disclaimer shown with every search *(revised)* |
| 23 | **Pet fee** | Back Lavender only: **$50 per stay**, taxed with the room charge, refundable on the same terms. The search checkbox does double duty — it filters to pet-friendly rooms *and* adds the fee. Unchecked, Back Lavender still appears at no fee |
| 24 | **Payment after the room is gone** | A charge that lands once the hold has lapsed re-claims the room through the exclusion constraint. If it is still free the stay is confirmed; if it was resold the booking is **cancelled and the whole amount refunded**, penalty-free — the guest did not change their mind, so decision #9 does not apply |
| 25 | **The ledger vs. the snapshot** | `bookings.amount_paid_cents` is the **gross** collected and only ever grows; refunds are rows in `payments`, never a subtraction. `pricing.Refund` derives from what was collected, so reducing it would make a second cancellation compute a smaller refund off an already-reduced figure |
| 26 | **Processing retention** | Every refund keeps **3%** of what was collected, configurable in `settings.refund_processing_rate`. Stripe's fee is taken on the way in and is **not** returned when a payment is refunded, so a literal full refund costs the inn that much on a cancellation it had no part in. Retention is `max(cancellation penalty, processing fee)` — **not** their sum, because a late cancellation's forfeited deposit already covers the processor many times over and adding both would charge the same transaction twice. Rounded **up**, since the entire point is that the inn is never short |
| 27 | **Maximum stay** | **31 nights**, in `settings.max_stay_nights`. Longer stays are arranged with the owner: the rates, cleaning and deposit/balance split this engine implements are not what a month-plus booking needs. The date picker stops offering departures past it and `availability.Search` refuses them, so a hand-crafted payload cannot get one through either |
| 28 | **Payment idempotency key** | `payments (stripe_id, status)`, **not `stripe_id` alone**. A declined card is retried on the same PaymentIntent, so one id carries a failed attempt and then a successful one; keying on the id alone made the success collide with the failure and drop a payment the guest had already been charged for. Both attempts are kept — the decline is what an owner needs when a charge is disputed |
| 29 | **Holding a room costs something** | `POST /api/bookings` is anonymous, free, and takes a room off sale for the hold TTL, so it is rate limited per client address far more tightly than the read endpoints. Seven rooms is a small enough inventory that an unmetered hold endpoint is a denial-of-service on the business, not on the server. The limiter's key is the last proxy hop and is only trusted when `BEHIND_PROXY` says a proxy exists |

### Payment lifecycle

```
book (arrival ≥ 8d) → charge deposit (50% of all-in), save payment method off-session
T-8 days            → email "you will be charged $X in 24 hours"
T-7 days            → off-session charge of balance
   ├─ success       → email receipt
   └─ failure       → email "you still owe $X, contact the inn" + unmissable admin flag
book (arrival < 8d) → charge full amount, no scheduled job
cancel ≥ 7d out     → refund everything paid, less the 3% the processor kept
cancel < 7d out     → refund everything paid minus 50% of the total
```

Refunds derive from **what was actually collected**, not from the total. That is what keeps them
correct when the T-7 charge failed: the guest has paid only the deposit, the penalty consumes it,
and the refund is zero rather than a negative the inn would try to collect. Implemented and tested
in `internal/pricing`.

The retention is `max(penalty, processing fee)` (decision #26), which is why the late branch above
is unchanged: the forfeited deposit already absorbs the processor's cut. The property worth holding
onto, and the one the tests assert directly, is that **the inn never ends up out of pocket** — for
any amount collected, on either side of the boundary, what it keeps is at least what Stripe took.

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
generates the TS client. One binary, one contract, no drift. *Not built yet: the booking flow's
types are hand-written in `web/src/lib/api.ts`, which is the one file the generator replaces.*

TanStack Query is likewise still ahead. The four booking screens each load one thing, and a
twenty-line hook covers it; the library earns its place when the admin console's calendar needs
caching and background refresh.

**Everything else** — Caddy (auto-TLS, reverse proxy) · Cloudflare free tier (CDN + DNS) · Sentry ·
nightly `pg_dump` + uploads to Backblaze B2 with a *tested* restore.

### Why one binary

`go build` produces a single artifact containing the API, the React build, and the job runner.
Deploy is `scp` + `systemctl restart`. No CORS, no split pipelines, no version skew between
frontend and backend, and the whole system can be run locally with one command.

---

## VPS (decision #2)

**Hetzner Cloud CX22, Ashburn VA — 2 vCPU, 4 GB RAM, 40 GB SSD, ~$5/month.**

Chosen against the owner's stated priority order: stability first regardless of price, then low
price, then simplicity.

- **Stability** is the gate, not a tiebreak. Hetzner publishes a 99.9% cloud SLA and has a long
  operational record, so it clears the bar. Anything that did not would be excluded no matter how
  cheap.
- **Price** is the next tiebreak among providers that clear the gate, and Hetzner wins it decisively:
  the comparable DigitalOcean droplet is roughly $12/month for half the RAM.
- **Simplicity** ranked last. DigitalOcean's console and documentation are gentler, and that is the
  one real thing being traded away. It is a defensible reversal if the extra ~$7/month is worth it.

The 4 GB is not incidental. Postgres plus the Go binary would fit in 1 GB, but the image pipeline
generating AVIF and WebP variants is memory-hungry, and that is where a smaller box would fail.

Ashburn keeps the server in the US and close to New Hampshire. Budget roughly 20% on top for
provider snapshots, **in addition to** the nightly `pg_dump` to Backblaze B2 — a provider snapshot is
not a tested restore.

## The job runner

An in-process goroutine polling a durable `jobs` table every 60s with `FOR UPDATE SKIP LOCKED`.
Survives restarts, idempotent by design, no external scheduler.

Built in `internal/jobs`. A claimed job is **leased, not deleted** — the claim
pushes `run_at` forward so no other runner takes it, and a runner that dies
mid-job leaves work that returns on its own. Every handler must therefore
tolerate running twice, the same discipline the webhook path lives under.

The runner is a goroutine inside the binary that serves guests, so a **panic in
any handler is recovered** and recorded as an ordinary job failure with its
stack in `last_error`. Background work misbehaving costs that job a retry; it
must never cost the inn its booking API.
Scheduling is the **database's clock**, not the caller's: `run_at` defaults to
`now()` in SQL, because the claim compares against `now()` and two clocks
deciding when a job is due is a bug that only shows up under load.

| Job | Trigger |
|---|---|
| `hold.sweep` | every minute — delete expired `kind='hold'` rows, then expire the pending bookings behind them. *Durable since step 4; the step-3 ticker is gone* |
| `balance.warn` | T-8 days — "charged in 24 hours" email. The scan catches up rather than matching a single day, so a server that was off for T-8 sends it late instead of never; `balance_warned_at` is what stops it repeating, and must be set in the same transaction that queues the mail |
| `balance.charge` | T-7 days — off-session PaymentIntent; on failure mark `payment_failed` + notify |
| `email.send` | queued sends with exponential backoff retry. *Built: `internal/email` renders and the runner delivers. A `Sender` interface stands where the Resend client goes; until it exists, `LogSender` writes each message to the log and says plainly that nothing was sent* |
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
arrives to find three steps at the entrance has been genuinely harmed.

**Current state: the filter is off and no room sets the flag.** The owner initially marked Mrs.
Beal's Suite and Rose Chamber as accessibility friendly, then clarified that both still require
stairs — as does every room in the house. So nothing claims accessibility, and there is no filter to
offer.

What remains, and why:

- **The honesty rule is a database constraint, not admin-form validation.**
  `CHECK (NOT is_accessible OR cardinality(accessibility_features) > 0)` — the flag cannot be set
  without naming at least one specific feature. Enforcing it only in the form that happens to write
  it would leave the promise dependent on which code path was used.
- **`settings.accessibility_notice`** carries a stairs disclaimer, returned with every availability
  search so the UI cannot quietly omit it. It is editable data rather than hardcoded copy, so the
  owner can reword it without a deploy.
- **Turning the filter back on** needs only real feature data and re-enabling the search parameter.
  The schema, the constraint, and the query shape are already there.

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

`GET /api/calendar` serves this: per room, the unbroken runs of sellable nights, each night carrying
the minimum stay in force on it. The client can then answer both of its questions — can a guest
arrive on this date, can they leave on that one — without a round trip per click. The test that
matters compares the picker's rule against the search over every span in a window and asserts they
agree, in both directions.

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

1. ~~**Foundation**~~ **DONE** — repo, Docker Compose Postgres, goose migrations, sqlc, one Go
   binary serving Vite. Caddy and the deploy script still outstanding, and belong with step 8.
2. ~~**Domain core**~~ **DONE** — rooms, settings, rate seasons → calendar generator,
   `room_occupancy` + exclusion constraint, availability query, and `internal/pricing` brought
   forward from step 4. Concurrency tests written before any UI, as planned, and they found a real
   deadlock (see below).
3. ~~**Booking flow**~~ **DONE** — search → results → room page → confirm → hold, plus the calendar
   the date picker greys from and the sweeper that reclaims abandoned checkouts. No Stripe anywhere
   in it, as planned.
4. **Payments** ← **IN PROGRESS.** The Stripe-independent half is built and tested: the `payments`
   ledger, the `jobs` runner (with `hold.sweep` moved into it), the `stripe_events` idempotency
   table, and `internal/payments` — the state machine that records a charge, promotes the hold,
   re-claims a room whose hold lapsed, and works out when money has to go back. What is left needs
   API keys: the Stripe SDK, `POST /api/bookings/{code}/payment-intent`, the signature-verified
   `POST /webhooks/stripe` that calls into the state machine, the off-session `balance.charge`
   handler, and the Payment Element on the front end.

   *A review of the half that is built found and fixed four things worth naming, since three of
   them would have cost money rather than merely looked wrong: the idempotency key was `stripe_id`
   alone and dropped the payment of any guest whose first card declined (decision #28); marking a
   webhook event handled committed separately from the work it guarded, so a failure mid-handler
   made Stripe stop retrying a payment that was never recorded; a panicking job handler took the
   whole binary down; and the T-8 warning was skipped permanently if the server missed that exact
   day. The unmetered hold endpoint (decision #29) was the fourth.*
5. **Comms** — Resend, email templates, PDF generation, signed manage-booking link + self-service
   cancel. *`internal/email` exists and renders: the shared letterhead layout and one file per
   message, all six deliberately **blank** — a line saying what each is for and nothing else. The
   copy is the owner's to write, like room descriptions and photos. The letterhead takes a logo
   from `EMAIL_LOGO_URL` and falls back to the inn's name in text; **no logo asset has been
   supplied yet**, so the fallback is what renders today.*
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
  exactly one succeeds and the rest get a clean "just taken" response. *(Done — at the booking
  layer as well as the occupancy one. A loser lands in one of two places depending on how close the
  race was: the room was gone before it searched, or it was taken from under it at the insert. Both
  are clean 409s; neither is a raw database error.)*
- **Turnover:** book Jun 10–13 and Jun 13–15 on the same room; both must succeed. *(Done, for holds
  as well as bookings.)*
- **Min-stay:** a 1-night query returns nothing anywhere on the calendar (global default is 2), and
  a 2-night query against a 3-night holiday season also returns nothing.
- **Min-stay bypass:** `POST /api/bookings` with a hand-crafted 1-night payload must be rejected
  server-side, not merely hidden by the date picker. *(Done — the booking path re-runs the
  availability query itself rather than trusting the client, so capacity, pets, occupancy, rate
  coverage and min-stay are all re-checked by the same SQL that produced the search results.)*
- **Pet fee:** `pet=true` returns only pet-friendly rooms and adds $50 to the quote as its own line;
  unchecked, the same room appears at no fee.
- **Accessibility filter:** *(deferred — the filter is off; see Accessibility above)*
- **Rate rebuild safety:** confirm a booking, edit the season covering its dates, rebuild, and assert
  the booking's total, nightly prices, and balance are **unchanged**.
- **Hold expiry:** create a hold, advance past TTL, confirm the sweeper frees the room. *(Done — and
  the booking behind it is marked `expired`, so a guest returning to their link is told what
  happened.)*
- **Money:** assert cents arithmetic against hand-computed totals, the 50% deposit and its rounding,
  and each refund branch. *(Done — `internal/pricing`.)*
- **Declined then retried:** record a failed attempt and then a successful one on the *same*
  PaymentIntent id; the stay must end up confirmed with the deposit collected once, and both
  attempts must be in the ledger. *(Done — this was a real bug, decision #28. The guest was charged
  and the booking stayed pending until the sweeper resold the room.)*
- **Underpayment:** a charge for less than the amount due at booking is recorded but confirms
  nothing, and topping it up confirms. *(Done — a backstop under the rule that the PaymentIntent's
  amount is derived server-side.)*
- **Held inventory:** hammering `POST /api/bookings` must stop taking rooms off sale, and must not
  be bypassable by varying `X-Forwarded-For`. *(Done — decision #29.)*
- **Job panics:** a handler that panics fails its job and leaves the server running. *(Done.)*

### What the concurrency tests actually found

Firing 16 simultaneous bookings at the last room did **not** reliably produce the clean `23P01`
exclusion violation the design assumed. Roughly once in 25 runs Postgres instead raised `40P01
deadlock_detected`: concurrent inserts wait on each other's uncommitted rows, and the deadlock
detector breaks the cycle by aborting one. In production that is an error page mid-checkout which a
guest cannot tell apart from the room genuinely being gone.

Retrying with backoff reduced it but stayed probabilistic — staggered overlapping spans still leaked
raw errors after five attempts. The fix is a **per-room advisory lock** taken in the same statement
as the insert (`pg_advisory_xact_lock(4771, room_id)`), so waiters queue in a defined order and no
cycle can form. Losers now get `23P01` every time. It is scoped per room, so the other six are
unaffected, and it made the contended path roughly three times faster by removing the lock-wait
pileup.

Verified over 200 runs of the full occupancy suite plus a deliberately adversarial staggered-overlap
test that asserts a caller never sees anything except success or a clean "room taken".

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
