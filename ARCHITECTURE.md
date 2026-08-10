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
| 3 | Rendering | Vite React SPA embedded in ONE Go binary via `embed.FS`; Go injects per-route meta + JSON-LD with live DB data. **Built** (`internal/httpx/meta.go`): the SPA fallback writes the page's own title, description, canonical, Open Graph tags and structured data into `<head>` before serving it, from the same read models the page's own API calls return — so the document a crawler indexes and the one a visitor reads cannot quote different rooms at different prices. Vite's static `<title>` is stripped, or every page would carry two. A description is the owner's own words or **absent**, never invented, on the same terms as the pages themselves; absolute URLs appear only with a `SITE_URL` to build them on. The booking flow and the console are `noindex` and carry no canonical. `robots.txt` and a `sitemap.xml` generated from live rooms sit beside it, ahead of the SPA fallback for the reason `/media/*` is |
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
| 15 | Admin auth | **Passkeys (WebAuthn), no password anywhere.** Single shared owner account, a real `users` table behind it, one credential per phone. *(revised; was a password plus TOTP)* The console is opened from the two owners' phones, so the authenticator is the handset itself: a private key it will only use after Face ID or a fingerprint. What that buys over the original plan is that **nothing stored server-side is a credential** — a leaked dump contains public keys — and that it **cannot be phished**, because the browser binds every signature to the origin. There is also no shared secret two people have to hand to each other and rotate. Sessions are **rows**, hashed, rolling **365 days** from last use, so a phone in regular use never signs in again and a lost one can be struck off. Enrollment is a **single-use** invitation, minted by `bealhouse enroll` on the server or from an already-signed-in console. **No step-up auth** on refunds: the owners' call, and the phones are locked |
| 16 | Media | Owner uploads in admin → VPS disk, Go generates AVIF/WebP variants, Cloudflare free CDN. **Built:** `internal/media` decodes an upload — which is also the only real check that it is an image — scales it to 2400px on the longest side, re-encodes it as JPEG, and stores it under the SHA-256 of its own bytes in `MEDIA_DIR`. Content addressing means the same photograph uploaded twice is one file and the URL can be served `immutable`; it also means **removing a photo does not delete the file**, since two rooms may point at the same bytes. `/media/*` is registered ahead of the SPA fallback, or a missing photograph would answer index.html with a 200 and render as a broken image with no error anywhere. **Built, less AVIF:** an upload now produces a ladder — 480/960/1600/2400, in JPEG and WebP — and the page picks with `srcset`. The widths are the larger half of that by far: the 960px JPEG measured 76 KB against 955 KB at 2400px, where WebP saves a further half at the same width, so a card four hundred CSS pixels wide was downloading twelve times what it could use. The rung is **in the filename**, which is what makes `media.Sources` a pure function and keeps a srcset from ever naming a file that was not written — a 404 inside one does not fall back, it is a broken image. The encoder is `gen2brain/webp`, libwebp under wazero, chosen because it builds with `CGO_ENABLED=0` on a machine with no C compiler; the pure-Go alternatives are lossless-only and larger than the JPEG they replace. **AVIF is feasible and deliberately deferred**: −61% at full size, but 5.3 MB of binary and ~1.7s per upload, which would move the work into a background job and require the API to report which variants exist yet. `MEDIA_DIR` is in neither the binary nor `pg_dump`, so it needs its own place on the VPS and its own line in the backup |
| 17 | Email | Resend. SPF/DKIM/DMARC at Bluehost DNS (SPF must include Resend *and* the mailbox host) |
| 18 | Launch | Placeholder site today → clean cutover. Google Business Profile + Search Console on day one |
| 19 | Guest self-service | Signed expiring link in confirmation email → view booking + **cancel**, refund executes automatically. Date changes go through the owner. **Built:** an HMAC over the code and an expiry (`BOOKING_LINK_SECRET`), not a stored token — stateless, valid for bookings made before the feature existed, and expiring thirty days after checkout. Cancelling is refused once the stay has begun, because decision #9's arithmetic does not describe a visit in progress |
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
| `balance.warn` | **Built.** T-8 — "charged in 24 hours" email. The scan catches up rather than matching a single day, so a server that was off for T-8 sends it late instead of never; `balance_warned_at` stops it repeating and is set in the same transaction that queues the mail. Runs hourly, because a daily job fires at whatever time the server last restarted |
| `balance.charge` | **Built.** T-7 — off-session charge against the card saved at booking; on failure flag `balance_charge_failed_at` and mail the guest. A decline is an outcome, not a job failure: returned as an error the runner would retry hourly and mail the same guest every time. A network error *is* returned, because the money may have moved and this server does not know |
| `checkout.remind` | **Built.** The departure-morning note — checkout time and the inn's goodbye, in the guest's inbox before they wake up on the day they leave. Every **fifteen** minutes rather than the balance jobs' hour, because those only have to land inside the right day and this one is meant to land at the start of it. **Matches the checkout date exactly**, where the T-8 warning uses a threshold to catch up: a late warning still works, a "you are leaving today" that arrives after the guest got home does not, so a day the server spent entirely off is a note that does not go out. `checkout_email_sent_at` is set in the same transaction that queues the mail |
| `email.send` | queued sends with exponential backoff retry. *Built: `internal/email` renders and the runner delivers. `Resend` implements `Sender` over plain `net/http` and takes over as soon as `RESEND_API_KEY` and `EMAIL_FROM` are set; until then `LogSender` writes each message to the log and says plainly that nothing was sent* |
| `rates.rebuild` | **Built.** Monthly, and on season save once admin exists — regenerate the nightly calendar 24 months forward. Nothing breaks on the day it stops: the horizon just creeps closer until a guest planning next autumn finds no price and the room drops out of the search with no error anywhere |
| `push.send` | **Built.** One queued notification fanned out to every browser the console is signed in on, so a booking reaches the owner's handset while the console is shut. Deliberately not one job per subscription: the thing being retried is the notification, and with two handsets re-sending to the one that already had it is the cheaper half of that trade. A push service answering 404 or 410 means the browser is gone, which is the one failure that must not be retried — the row is deleted instead |
| ~~`backup.verify`~~ | **Not a job on this runner.** It is `bealhouse-verify.timer`, weekly, shelling out to `restore.sh drill`. Written here first and moved because the drill wants `CREATE DATABASE`, `pg_restore` and a scratch directory — which the hardened unit serving the public internet should not have — runs for minutes rather than inside a transaction, and proves a backup that is itself a systemd timer. See [deploy/README.md](deploy/README.md) |

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

### Authentication *(built — decision #15)*

`internal/admin`, five tables, no passwords. Sign-in is one tap: the credential is discoverable, so
the phone holds the account handle beside the key and the login page has no username field to type
into. Four things carry the security, and each exists because the alternative has a specific hole:

- **Sessions are hashed rows.** "Stays signed in" is only safe next to a list somebody can strike a
  line through, and a signed stateless token cannot be revoked when a phone is lost. The cookie
  holds 32 random bytes; the table holds their SHA-256 — no salt and no work factor, because the
  value was not chosen by a person and there is no dictionary to run at it.
- **Enrollment invitations are single use.** Enrolling a passkey creates a permanent way in, so the
  thing authorising it must be spendable exactly once — which an HMAC link is not. The claim is one
  `UPDATE ... RETURNING`, so two phones racing produce one winner.
- **Challenges are rows, deleted on use.** A challenge that can be answered twice is a signature
  that can be replayed. `DELETE ... RETURNING`, for the same reason.
- **Every refusal is the same refusal.** Expired, spent, forged and never-existed all answer
  `ErrDenied` / 401. Anything else is an oracle telling whoever is trying which guesses were close.

Cookies are `HttpOnly`, `SameSite=Strict`, scoped to `/api/admin` so they never ride along on a
guest's request, and `Secure` whenever the request arrived over TLS. Writes additionally require a
JSON content type and reject a `Sec-Fetch-Site` that says cross-site — an HTML form, the one
cross-origin shape needing no preflight, can do neither.

**Bootstrap is `bealhouse enroll` on the server**, which proves shell access, and is deliberately
the only way in when no phone is enrolled. Every enrollment after the first can be minted from the
console. Removing the last passkey is refused; revoking one signs out its sessions **first**, before
the row is deleted, because `ON DELETE SET NULL` would otherwise blank the link and leave the lost
handset signed in for the rest of its year.

### The shell *(built)*

`/admin` is a gate and a frame. The gate is the session cookie and nothing else — no route hides a
screen, because every endpoint under `/api/admin` is already closed, so a forged front end reaches
empty boxes. A 401 renders the sign-in **in place of** the screen that was asked for rather than
redirecting to a login address: the URL survives, so signing in lands where the owner was going, and
a session expiring under an open console is a prompt rather than a page that navigated out from
under them.

`/admin/enroll` sits **outside** the gate, because a phone accepting an invitation is by definition
not signed in yet — the single-use token in the fragment is what authorises it. The page takes that
token out of the address bar the moment it has read it. A fragment never reaches the server, so it
is in no access log, but it is still a one-shot key to the console sitting in plain sight and in
this browser's history. It is captured in an effect and not only at mount, because a fragment
arriving on a page already open is a *same-document* navigation: nothing re-mounts, so a mount-time
read would miss it and the clearing step would then destroy an invitation the owner had just
pasted in.

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
- **Email copy** *(built)* — the eight messages the inn sends, each with a subject and a
  body the owner edits, and a preview that posts the **draft** rather than naming the stored row:
  the question is what a save will look like, and asking after saving asks too late. It renders
  against a sample booking, in a frame sandboxed with neither `allow-scripts` nor
  `allow-same-origin` — the markup is the owner's, but it has no business reaching the session it
  is previewed from. A row in `email_templates` overrides the file that ships; no row means the
  shipped one, so "reset to the original" is a delete and a message added in a later release turns
  up in the editor on its own. Nothing is cached, so a save applies to the next message rather than
  the next deploy. The **layout is not editable** — it carries the letterhead and the table
  scaffolding that survives Outlook, and one bad edit there would break every message at once. The
  save path **must** call `email.Parse` first: copy that will not compile fails at send time, which
  is after the guest's card has been charged.
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
4. **Payments** ← **IN PROGRESS, and further along than "needs keys" suggested.** Built and tested:
   the `payments` ledger, the `jobs` runner, the `stripe_events` idempotency table, the state
   machine, `POST /api/bookings/{code}/payment-intent`, the signature-verified
   `POST /webhooks/stripe`, the `balance.warn` and `balance.charge` jobs, the confirmation and
   receipt mail queued inside the transactions that earn it, and the Payment Element with the
   return-polling page behind it.

   *Almost none of that needed an account.* A webhook signature is an HMAC over the raw body with
   a shared secret, so the tests hold both ends: tampered bodies, foreign secrets, replays outside
   the tolerance window, and a delivery that must never reach the state machine. `internal/gateway`
   holds everything Stripe-shaped; `internal/payments` still does not import the SDK, which is what
   keeps the hard cases testable against real Postgres with no key and no network.

   **What genuinely remains needs keys:** exercising `gateway.Stripe` against the real API at all —
   every line of it is written and none of it has ever made a request — plus the publishable key
   for the card form, and the verification matrix below (test cards, 3-D Secure, Test Clocks).
   Until then `STRIPE_FAKE=true` substitutes a processor that mints ids and takes no money, so the
   whole journey is walkable in a browser. It refuses to exist unless no Stripe variable is set at
   all and `ENV=dev`, because ENV defaults to `dev` and an unconfigured production deploy would
   otherwise look exactly like a laptop.

   *A review of the half that is built found and fixed four things worth naming, since three of
   them would have cost money rather than merely looked wrong: the idempotency key was `stripe_id`
   alone and dropped the payment of any guest whose first card declined (decision #28); marking a
   webhook event handled committed separately from the work it guarded, so a failure mid-handler
   made Stripe stop retrying a payment that was never recorded; a panicking job handler took the
   whole binary down; and the T-8 warning was skipped permanently if the server missed that exact
   day. The unmetered hold endpoint (decision #29) was the fourth.*
5. **Comms** ← **IN PROGRESS.** Built:

   - **The provider.** `email.Resend` implements `Sender` over plain `net/http` — one endpoint,
     one JSON body, one bearer token, no SDK — and takes over the moment `RESEND_API_KEY` and
     `EMAIL_FROM` are both set. Like `gateway.Stripe` it is written and has never made a request;
     its tests hold the far end with an `httptest` server. Half a configuration is an error in
     the log and is treated as none, but does not stop the binary booting: the whole reason mail
     is queued is that email must never fail a booking.
   - **The letterhead.** The inn's mark now ships in the repo — `web/public/logo.svg`, its square
     reversed favicon and a PNG for mail clients — and `EMAIL_LOGO_URL` defaults to `SITE_URL` +
     `/logo-email.png`, so the absolute-URL rule holds without anyone retyping their own origin.
   - **The manage-booking link and self-service cancel** (decision #19). An HMAC over the code and
     an expiry, signed with `BOOKING_LINK_SECRET`: stateless, works for every booking there has
     ever been, and expires thirty days after checkout so a capability in a forwarded email does
     not outlive the stay by years. Behind it, `GET /api/bookings/{code}/manage` quotes what
     cancelling would return today and `POST .../cancel` does it — cancelling the stay, putting
     the room back on sale and queueing the refund in one transaction, with the amount settled
     there rather than recomputed by the job.
   - **The confirmation PDF** (`internal/pdf`, `go-pdf/fpdf`), rendered on demand from the
     booking's snapshot and served behind the same token, because it carries the guest's name.
     Pure and doing no arithmetic, so it cannot disagree with the email beside it — money and
     dates are formatted by `email.Money` and `email.Day` rather than by a second copy of those
     rules. The inn's mark is drawn in vector primitives, the same geometry as `logo.svg`.

   - **The departure-morning note** (`checkout.remind`), the seventh message. It goes out in the
     first minutes of the day a guest leaves, carrying the checkout hour from `settings` rather
     than baked into the words, so the sentence about it stays true when the owner changes the
     setting. It carries no money at all: a stay reaching it has been paid in full, and a guest
     whose card was refused has had the `balance_failed` message instead.
   - **The copy is data now.** `email_templates` holds a subject and a body per message the owner
     has actually written, and the shipped file stands in for every message they have not — so the
     admin console's editor is one authenticated endpoint away rather than a schema change. See the
     Admin console section for where the lines are drawn.

   - **Notifications to the owner's handset** (`internal/push`, migration 00023). A booking or an
     inquiry reaches the phone while the console is shut, beside the email rather than instead of
     it. Web Push with a VAPID pair from `bealhouse vapid`, a service worker in
     `web/public/sw.js`, and the subscription rows the only state it owns — the handler forgets one
     the push service says is gone. Queued on the same runner mail is, for the same reason: a push
     service having a bad afternoon must delay the nudge and never the booking that earned it.
     Half a key pair is an error in the log and treated as none, and notifications are logged
     instead of sent.

   *The eight templates shipped **blank** and no longer are.* The copy is still the owner's, and
   still theirs to replace from the console — but the owner asked for a starting point rather than
   eight empty files, so all eight now carry real sentences **written to be edited, not shipped
   unread**. Each keeps the branch that carries its meaning: the confirmation says nothing about a
   balance on a stay paid in full, the declined-card message leads with the room still being
   theirs, and the departure note carries no figure at all. The email copy editor previews a draft
   against a **sample booking** — Sample Guest, code SAMPLE, invented figures, every optional field
   filled — so an inn on its first day sees its own letterhead and no real guest's name appears on
   a screen that never asked about them. The manage link is wired into the confirmation as
   structure rather than copy, because it is the only way a guest reaches their booking.

   **Still to do:** the Resend account itself — DNS for SPF/DKIM/DMARC (decision #17) and a first
   real send — a `PUSH_VAPID_*` pair from `bealhouse vapid`, and the owner's pass over the eight
   messages in the console, which is a review of words that already work rather than a blank page.
6. **Admin** ← **IN PROGRESS.** **Auth is built** (decision #15, revised): passkeys, no passwords,
   `internal/admin` plus the `/api/admin/auth/*` routes and the session middleware everything else
   will sit behind. `bealhouse enroll` is the bootstrap. The tests are written adversarially rather
   than happily — single-use invitations and challenges under concurrent racers, a revoked phone's
   session dying with it, one account unable to touch another's keys, cross-site writes, forged
   cookies, an unconfigured console answering 503 instead of the SPA. They found the ordering bug in
   passkey revocation.

   **The shell is built too.** `/admin` is the frame every later screen hangs off: the session
   gate, the one-tap sign-in, the enrollment page a phone accepts an invitation on, and the one
   screen whose backend already existed — the phones that can sign in, the phones currently
   signed in, and the buttons that mint an invitation, strike a handset off, or end every
   session at once. Nothing on it is a placeholder; every panel is wired to a real endpoint,
   which is why it stops there.

   The browser half of a passkey ceremony is the platform's own `parse*OptionsFromJSON()` and
   `credential.toJSON()`, which are exactly the encoding go-webauthn writes and reads — so
   there is no WebAuthn library in the bundle, and no second implementation of base64url to
   disagree with the first. A browser too old for them is told so instead of being shown a
   button that cannot work.

   *Building it found the encoding bug the Go tests could not: `Passkey.ID` was a `[]byte`,
   which `encoding/json` writes as standard padded base64, while `DELETE /api/admin/passkeys/{id}`
   decodes base64url. Every id the console read back was unusable — and because standard base64
   contains `/`, the request did not even reach the handler. No phone could be revoked from the
   console at all, leaving shell access to the server as the only way to strike off a lost
   handset, which is the thing a second enrolled phone exists to avoid needing. The ids are
   base64url on the wire now and the regression asserts the round trip rather than a literal,
   since a literal on each side is what let the two drift.*

   **The screens are built too.** `internal/console` holds them — one package, one read model
   per screen, and no rule reimplemented: claiming a room is still `occupancy.Create`, pricing a
   stay is still `availability.Search`, refunding is still `payments.Cancel`, and regenerating
   the calendar is still the SQL function the monthly job calls. Today's board; the reservations
   list with paid against total and refused cards in red; the booking editor with its refund
   quote and its manual refund; the 7-row calendar and blocking; the rate grid; guest search and
   notes; the room, menu, events and inquiry content editors; the email copy editor; the page
   prose; and settings.

   *Three things there were worth getting right rather than merely working.* A **manual
   booking** is `booking.Create` with a flag, not a second write path, so an owner taking a
   reservation by phone goes through the same exclusion constraint a guest does and cannot
   double-book a room the website would have refused — and it earns the guest the same
   confirmation, from the same payload builder, queued in the same transaction that wrote the
   stay. What it does not earn them is the two balance messages, which announce and then take
   money from a saved card there is none of. That distinction exposed a real defect in the
   confirmation itself: `BalanceDue` was tied to `balance_charge_at`, so a stay with money
   outstanding and no scheduled date to collect it would have read as *paid in full*.
   **Unblocking filters on `kind = 'block'`
   in SQL**, so an id naming a confirmed booking's occupancy row matches nothing instead of
   putting a paid stay's room back on sale. And the **rate preview applies the edit inside a
   transaction and rolls back** — the only way a diff can account for a lower-priority season
   sitting underneath the one being edited, which is why the season resolution was lifted out of
   `rebuild_rate_calendar` into `generated_rate_calendar()` and both now share one copy of it.

   **Still to do:** driving the authenticated screens on a real handset, which is the manual
   verification below and needs an enrolled phone rather than more code.
7. **Content & marketing** ← **IN PROGRESS.** The pages are built: home anchored on the search,
   a rooms index, the restaurant with its live menu and `Menu` JSON-LD, events with a gallery and
   the inquiry form, and the owner's story. Each renders live data plus an optional prose slot
   from `page_copy`, and each **renders no paragraph at all** where nothing has been written —
   the words are the owner's, and an invented sentence about the food would sit on the public
   internet until somebody remembered it was invented.

   **Photographs upload from the console** (decision #16, `internal/media`): straight off a phone,
   scaled and re-encoded on the way in, stored content-addressed and served `immutable` from
   `/media/*` ahead of the SPA fallback. Alt text is required on every one, and remove and reorder
   are the same list the page renders from.

   **The head is the server's now** (decision #3, `internal/httpx/meta.go`). The SPA is one
   document for every address, so until this the home page, all seven rooms, the restaurant and
   the events shared one title, no description and no structured data — which is the whole of the
   site's search presence, and it cost most on the room pages, the ones somebody arrives at from
   a search engine rather than from the front door. The fallback now writes the page's own title,
   description, canonical, Open Graph tags and JSON-LD (`LodgingBusiness`, `HotelRoom` with the
   same "from" price the card shows, `Restaurant` + `Menu`, `Event`) before serving the document,
   and `robots.txt` and a live `sitemap.xml` sit beside it.

   *Three things there were worth getting right.* The **description is the owner's or nothing** —
   the same rule the pages follow, so a page nobody has written publishes no description rather
   than an empty one, and the only sentence this repository supplies is the home page's fallback,
   which says what the footer has always said. The **booking flow is `noindex` and `Disallow`ed**,
   because `/book` and `/bookings` take a real room off sale for the hold TTL and a crawler
   walking them empties the inn quietly — decision #29's risk arriving through the front door.
   And this is **the one place console text becomes markup**: page copy is stored as plain text
   with no markdown parser precisely so there is no way to put a `<script>` on the public site
   from a phone, and `html/template` plus `json.Marshal`'s own escaping is the other half of that
   promise. `TestTheOwnersWordsCannotEscapeTheDocument` asserts both halves, since the attribute
   and the JSON-LD are different escapers and only one of them being right is the interesting
   failure.

   **Photographs ship as a ladder now** (decision #16): 480/960/1600/2400, in JPEG and WebP, with
   the page picking through `srcset`. The widths are the larger half by a distance — the 960px
   JPEG measured 76 KB against 955 KB at 2400px, so a room card four hundred CSS pixels wide was
   downloading twelve times what it could use, which is exactly the failure the re-encoding step
   was written to prevent and had only half prevented. The rung is **in the filename**, which is
   what makes `media.Sources` a pure function callable from three packages with no Store threaded
   through, and what keeps a `srcset` from ever naming a file that was not written — a 404 inside
   one does not fall back to the `src`, it is a broken image.

   *Wiring it up found a real defect*: `availability` prefixed `/media/` onto a path that already
   carried it, so every photograph on the **search results and the room page** would have been
   broken. Nothing had noticed because no photographs have been uploaded yet and no test asserted
   the shape; `TestAPhotoURLIsTheStoredPathUntouched` does now.

   **Still to do:** AVIF, which is feasible and deferred — see decision #16 for the trade.
8. **Launch** ← **IN PROGRESS.** The machinery is written and lives in [`deploy/`](deploy/):
   the hardened systemd unit, the Caddyfile, a deploy script, the nightly backup and its timer,
   and the restore drill. `.github/workflows/ci.yml` runs gofmt, vet, a generated-code check and
   the full suite against a real Postgres on every push — **under `-race`**, which is the one
   place it can run at all, since the development machine has no C compiler.

   *Four things there were worth getting right.* **The binary carries its own migrations**, so a
   deploy is one file and `bealhouse migrate up`, and the code on the box and the schema applied
   to the database cannot come from different commits. The deploy **migrates with the new binary
   before installing it**, so a failed migration leaves the old one serving guests, and rolls the
   binary back — not the database — if health does not come up; `/api/health` answers 200 with
   `"db":"down"` rather than failing, so the check reads the field. The **backup takes the
   database and `MEDIA_DIR` as one set under one timestamp**, because `pg_dump` does not contain
   the photographs and a restore that brings back the paths and not the files is a site of broken
   images with nothing in any log to say so (decision #16) — `restore.sh drill` proves the pair
   into a scratch database and throws it away, and `verify` runs the same check against what is
   live. **The drill runs itself**, from `bealhouse-verify.timer` every Sunday after the backup it
   proves, on the newest set and with no argument to get wrong. And **Caddy sets no security
   headers**, because the binary sets them all and two sources for one header drift apart.

   *Writing the CI found a pre-existing flake in the suite itself*, which had been failing roughly
   one full parallel run in four: `availability` and `console` each claim **several rooms inside
   one transaction**, `occupancy.Create` takes a per-room advisory lock held to the end of it, and
   two packages doing that in different orders is an AB-BA deadlock. Nothing in the application
   does this — a booking claims exactly one room, which is what makes that lock sufficient — so
   the fix is `testdb.Exclusive` in the two fixtures. It also exposed something worth knowing
   about `occupancy.Create`: its deadlock retry was a no-op when the caller owns the transaction,
   because the deadlock has already aborted it, and the retry then returned `25P02` in place of
   the real error — which is why the flake read as an unrelated statement having failed earlier.

   *That retry is now gone, and the deadlock reaches the caller as itself.* It was left over
   from before the advisory lock: with the lock, claims for one room queue rather than wait on
   each other, so the single-room case it was written for can no longer deadlock at all — and
   the claims that matter run inside a transaction, where a retry could never have worked.
   Making it work would mean a `SAVEPOINT` around every claim, and the retry inside it would
   then wait out `deadlock_timeout` again against a lock the other transaction still holds.
   Running the work again belongs to the caller, and every caller can: the jobs runner re-runs
   its job, a guest's request is one they can send again, and the owner blocking a room is a
   person looking at a button. `TestADeadlockArrivesAsADeadlock` deadlocks two transactions on
   purpose and asserts the SQLSTATE that comes back is `40P01` and not its aftermath.

   **Error reporting is an `slog` handler** (`internal/sentry`), which is the only shape that does
   not depend on somebody remembering to add a second line beside their `slog.Error` — the reports
   that would be missing are exactly the ones from the paths nobody reviewed. It wraps rather than
   replaces, so the journal on the box stays the full account and Sentry is the view of the part
   worth looking at, and it reports **Error and above only**, because WARN here is how this binary
   says "no Stripe key" and "no media directory" — conditions it is designed to survive and which
   would otherwise open an issue on every boot. Written against `net/http` rather than the SDK, on
   the same reasoning as `email.Resend`: one endpoint, one JSON body, one header, and this binary
   already has panic recovery in the two places that need it. `slog.Record` carries the PC of the
   call site, so a report names the line that produced it without a stack captured from inside a
   handler, which would be full of slog's own frames. Reporting never blocks the goroutine that
   logged and never retries — the error is already in the log, and a retry loop against an ingest
   that is rate limiting the inn turns one bad afternoon into two.

   **Still to do:** a Sentry project and its DSN, uptime monitoring from outside the box, DNS
   cutover, Search Console, the Google Business Profile, and Stripe live keys.

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
- **The preview changes nothing:** previewing a season that would reprice a month must report the
  change and leave both `rate_seasons` and `rate_calendar` exactly as they were. If it ever
  commits, an owner asking what a save would do has already done it. *(Done.)*
- **The console cannot double-book:** a manual booking overlapping a stay the console itself just
  took must be refused, by the exclusion constraint rather than by the form. *(Done.)*
- **Unblocking is not a release:** removing a "block" by the id of a confirmed booking's occupancy
  row must affect nothing and leave the room off sale. *(Done — the alternative is a paid stay's
  room back on sale with the guest still arriving.)*
- **A manual booking schedules no charge:** it is confirmed, its occupancy row never expires, and
  `balance_charge_at` is NULL — otherwise the T-7 job tries a card that was never saved, flags a
  failure, and mails the guest about a payment they were always making by cheque. *(Done.)*
- **A manual booking is still confirmed to the guest:** the confirmation and the owner's copy are
  queued, the confirmation reports nothing collected and the whole total outstanding, and it
  carries no charge date. A stay with money owed must never read as paid in full. *(Done.)*
- **A refused manual booking tells nobody:** the confirmation is queued inside the transaction and
  after the room is claimed, so a booking that lost the race for its room leaves no message behind
  about a stay that does not exist. *(Done.)*
- **A payment link is never sent to a booking with a card on file:** a confirmed stay whose balance
  is already scheduled for T-7, or one with nothing outstanding, is refused. Both are how a guest
  pays twice. *(Done.)*
- **Every marketing page describes itself, and only itself:** each of the five has its own title
  and canonical, the shell's static `<title>` is gone rather than joined by a second one, and a
  room page's structured offer quotes the same "from" price the API does. *(Done.)*
- **A page nobody has written publishes no description:** absent, not empty — the head follows the
  same rule the page does, and an invented sentence in a search result outlives the memory of
  having invented it. *(Done.)*
- **The console cannot put markup on the public site:** a page heading and a dish name containing
  `"><script>` must survive into the document as text, through the HTML attribute escaper *and*
  through JSON-LD, which are two different escapers. *(Done — this is the one place plain-text
  page copy becomes markup.)*
- **The booking flow is not crawlable:** `/book` and `/bookings` are `noindex`, carry no canonical
  and are `Disallow`ed, because a GET there ends in a hold and a crawler walking them takes the
  inn off sale. *(Done.)*
- **Every URL in a `srcset` is a file that exists:** a photograph smaller than a rung does not
  have that rung, and a 404 inside a srcset does not fall back to the `src` — the browser has
  committed to that candidate and the page shows a broken image. *(Done — the rung is in the
  filename, so what exists is derivable rather than guessed.)*
- **A stored path is the URL, unchanged:** prefixing `/media/` onto a path that already carries it
  breaks every photograph on the page. *(Done — this was a real defect on the search results and
  room pages, invisible only because nothing has been uploaded yet.)*
- **An uploaded photograph is decoded, not trusted:** a renamed PDF is refused and nothing is
  written; a 3600px photograph comes back at 2400 with its aspect ratio kept; the same file uploaded
  twice occupies one path. *(Done.)*
- **A stored path cannot escape the media directory:** `..`, a nested path, and a dot-file all
  resolve to nothing and answer 404, whatever ends up in the database. *(Done.)*
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
- **Forged webhook:** a delivery with no signature, one signed with another secret, one whose body
  was altered after signing, and one replayed outside the tolerance window must all be refused —
  and must not reach the payment state machine at all, rather than being stopped somewhere further
  in where a later edit could let them through. *(Done, and with no Stripe account: the tests sign
  their own payloads.)*
- **Amount is the server's:** the pay endpoint takes no body, derives the deposit or full total
  from the booking's own snapshot, and asks only for what is still outstanding. *(Done.)*
- **T-7 decline vs. outage:** a refused card flags the booking, mails the guest and leaves the stay
  confirmed; a network error fails the job instead, tells nobody, and flags nothing — the money may
  have moved. One refused card must not stop the inn collecting from anybody else that day.
  *(Done.)*
- **Confirmed once:** a redelivered webhook must not send a second confirmation, and the T-7 charge
  landing on a stay confirmed weeks earlier must send a receipt rather than a second "you're
  booked". *(Done — the second was a real bug, caught by its own test.)*
- **Cancelled once, and told once:** a stay that paid a deposit and then a balance must produce one
  cancellation email naming the whole refund, not one per intent refunded. *(Done — this was a
  real bug. The message was queued inside `RecordRefund`, which the refund job calls once per
  payment; it now belongs to the transaction that cancels the stay.)*
- **The link is the authenticator:** the manage and cancel endpoints must refuse a missing, forged,
  expired or foreign-signed token, must not distinguish any of those from a booking that does not
  exist, and must leave the booking untouched in every case. *(Done, and with no account: an HMAC
  is a secret the tests hold both ends of, exactly like the webhook signature.)*
- **Refund policy at the boundary:** cancelling exactly seven days out is in time; three days out
  forfeits the deposit and returns nothing when only the deposit was collected; and a cancellation
  on or after the arrival date is refused rather than run through arithmetic that does not
  describe it. *(Done.)*
- **The document says what the row says:** the confirmation PDF renders from the booking's own
  snapshot and computes nothing, so it cannot drift from the email or the page; and a guest whose
  name is not ASCII must not find it mangled, which the built-in fonts do silently without a
  cp1252 translation. *(Done.)*
- **Refunding twice:** the refund job re-run must send the money once. The division of a partial
  refund over several intents has to be reproducible, or a retry asks Stripe for amounts it has
  not seen and each is a fresh refund. *(Done.)*

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

The retry stayed beside it for a while and is now gone. It could not work where every claim
actually happens — inside the caller's own transaction, which the deadlock has already aborted —
and on its way out it replaced the `40P01` with the `25P02` its own retry had earned. A deadlock
now reaches the caller as a deadlock, and running the work again is the caller's to do.

Verified over 200 runs of the full occupancy suite plus a deliberately adversarial staggered-overlap
test that asserts a caller never sees anything except success or a clean "room taken".

**Payments** — Stripe CLI (`stripe listen --forward-to localhost:8080/webhooks/stripe`) for the full
matrix: success, decline, 3DS required, duplicate webhook delivery (must be idempotent), out-of-order
delivery. Critically, use **Stripe Test Clocks** to fast-forward a real test booking through T-8 and
T-7 and verify the warning email, the off-session charge, and the failure path — this is the only
honest way to test decision #6 without waiting a week.

**End-to-end** *(built, less the parts that need an account)* — Playwright in `web/e2e/`, run by CI
after `go test`. It deliberately repeats nothing the Go suite can assert more cheaply; what it covers
is the joins nothing else can see.

- **The guest journey**: search → results → room → confirm → hold → pay → confirmed, through the
  stand-in processor, with the **total carried from screen to screen and compared at each one**. A
  quote that changed between the room page and the hold is the failure this exists to catch, and it
  is invisible to any test looking at one screen.
- **The head a crawler gets**, fetched as a document rather than driven as a page: one `<title>` per
  route and never two, no two pages sharing one, a canonical on each, a room's `HotelRoom` offer,
  an absolute `sitemap.xml` listing every room, and `/book` and `/bookings` `noindex` and
  `Disallow`ed. `robots.txt` served as `text/plain` and a missing photograph as a 404, because both
  answered by the SPA fallback would be HTML with a 200.
- **The console's gate**: `/admin` is a sign-in and not the SPA's index, the URL survives it, every
  `/api/admin/*` route answers 401 as JSON, and a POST to an unrouted path is not answered with the
  SPA.

It books real rooms in its own stretch of the calendar (today+600) and clears that stretch before
and after the run — before as well, because a run killed partway leaves exactly the bookings that
would make the next one find nothing available.

**Still needing an account or a handset:** the test card and 3-D Secure, the PDF download and the
self-service cancel link — both of which sit behind a token that reaches the guest by email, so
proving them in a browser means reading the inn's outbox — and the console's authenticated screens,
which need an enrolled phone.

**Manual** — admin console driven entirely on a real phone; run the whole booking flow on mobile;
Lighthouse ≥ 90 on home and room pages; validate JSON-LD in Google's Rich Results Test; confirm
SPF/DKIM/DMARC pass and that confirmations land in a real Gmail inbox, not spam.
