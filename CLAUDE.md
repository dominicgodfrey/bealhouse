# Working on Beal House

Direct booking engine, marketing site, and admin console for a 7-room inn.
[ARCHITECTURE.md](ARCHITECTURE.md) is the design and the numbered decisions;
[README.md](README.md) is how to run it. Read the decisions table before changing
anything about money, dates, or availability — several were revised after the
document was first written and the revisions are marked.

**Build-order steps 1, 2 and 3 are done. Step 4, payments, is built end to end —
the pay endpoint, the webhook, both balance jobs and the card form — and none of
it has ever talked to Stripe. See *Step 4* below for what the account is still
needed for, and for `STRIPE_FAKE`, which makes the whole journey walkable
today.**

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
- **`go build -o bin/bealhouse` writes `bin/bealhouse`, with no `.exe`.** If a
  `bin/bealhouse.exe` is lying around from some earlier build, `./bin/bealhouse.exe`
  starts *that* one and it can be days old — a route added an hour ago 404s and
  everything else looks fine, which is a long way to chase. Run the extensionless
  file, or check the timestamps in `bin/` before believing a smoke test.
- **Docker Desktop can fail to start after an unclean shutdown**, crashing with
  "remove …engine.sock: The file cannot be accessed by the system." It leaves
  orphaned AF_UNIX socket reparse points that nothing — not Docker, not
  `Remove-Item`, not `del` — can delete. **Rename the parent directory aside**
  and it starts clean; Docker recreates it. The two seen so far are
  `%LOCALAPPDATA%\docker-secrets-engine\` and `%LOCALAPPDATA%\Docker\run\`, and
  the error names whichever it hit first.
  **Rename both in the same stop, before starting Docker again.** Fixing the one
  in the message and restarting does not converge: the failed startup recreates
  the directory you just renamed and leaves a fresh orphan in it, so the next
  attempt fails on the other one, and the one after that on the first again.
  Watched from the log it looks like the same bug reappearing, and it is really
  two of them taking turns. `%LOCALAPPDATA%\Docker\log\host\com.docker.backend.exe.log`
  is where the real error is; the GUI only offers Quit.
  The dialog's "Reset to factory defaults" would also fix it and would destroy
  every container and volume, including this project's Postgres data. Do not.
- **Do not rewrite Go or SQL files with PowerShell string replacement.** It mangles
  UTF-8; em-dashes in comments came back as mojibake. Use the Edit tool, and run
  `gofmt -l .` afterwards either way.

## Conventions that matter

- **Money is integer cents. Never floats, anywhere.** Both rates — tax and the
  refund processing retention — cross the database boundary pre-scaled to
  hundred-thousandths (`pricing.Rate`) so no numeric is ever decoded into a
  float64.
- **A refund always keeps the card processor's cut** (decision #26, 3% in
  `settings.refund_processing_rate`). Stripe does not return its fee when a
  payment is refunded, so `pricing.Refund` retains `max(penalty, fee)` — the max,
  not the sum, or a late cancellation pays for the same transaction twice. The
  fee rounds **up**. `TestRefundNeverLeavesTheInnOutOfPocket` is the property
  that matters; leave it in place.
- **Stays are capped at `settings.max_stay_nights`** (decision #27, 31 nights).
  Enforced in `availability.Search`, so `booking.Create` inherits it by re-running
  that query. The picker greys past it and says why.
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
- **...and so does a rolled-back test that runs a job runner.** `jobs` and `email`
  share one `jobs` table, and `jobs`' concurrency test commits rows into it. A
  runner under test will happily claim one of those, find no handler, and turn an
  assertion about what was sent into a coin flip. Both packages empty the queue
  inside their own transaction *and* take `Exclusive`. Count by `kind`, never
  `CountJobs`, for the same reason.
- Each package books in **its own stretch of calendar**. A committed booking inside
  another package's window silently breaks that package.

  | Package | Window |
  |---|---|
  | occupancy | fixed 2027 and 2028 dates |
  | availability — search tests | today+30 to +35 |
  | availability — calendar tests | today+120 to +150 |
  | booking | today+200 |
  | payments | today+300 |
  | httpx — webhook and manage tests | today+400 |

  **The today+30 window is the soft spot.** The date picker opens on the current
  month, so clicking through the booking flow by hand lands a real hold right in
  it, and the availability search tests then fail with a room mysteriously
  missing. If those tests fail that way and pass in isolation, look for a stray
  `pending` booking before suspecting the code.

## Content is the owner's, not ours

Room descriptions, photos, amenities, and rate seasons are all managed through the
admin console once it exists. What is in the seed is placeholder: descriptions are
marked `PLACEHOLDER`, amenities are empty, there is one flat rate season, and
`web/public/placeholders/*.svg` stands in for photos as a **UI fallback** rather
than seeded rows — a placeholder in the database is one somebody has to remember
to delete. Do not invent content, and do not seed guesses.

The same goes for **email copy** (`internal/email/templates/`). All seven templates
are blank on purpose: a subject marked `PLACEHOLDER` and one line saying what the
message is for. Write the shared layout, never the sentences a guest reads.

**And the copy is editable data, not just a file.** A row in `email_templates`
overrides the shipped template for one message; no row means the shipped one, so
resetting is a `DELETE` and a message added in a later release appears in the
editor with its own words already in it. `Renderer` reads the override **on every
send** — no cache, so a save applies to the next message rather than the next
deploy. Two things stay out of the editor: the **layout**, because one bad edit
there breaks every message rather than the one on screen, and the **payload**,
because what a template can say about a booking is fixed by the structs in
`email/data.go`. **Anything that saves copy must call `email.Parse` first** —
copy that will not compile fails at send time, which is after the guest's card
has been charged and with nothing in front of the owner to connect it to the
sentence they typed.

**The logo is the owner's and is now in the repo**, as one path in
`web/public/logo.svg` — three connected buildings, ink on nothing, on a
**211 × 58** grid.

**It is a trace of the owner's artwork, not a drawing of it.** The outline was
walked on the source raster's pixel grid and fitted to straight lines; it
renders within 0.3% of the original's own pixels, and that difference is
anti-aliasing on the roof slopes. The numbers in it are measurements — **do not
tidy them by eye.** The version before it was drawn from a description and had
the proportions visibly wrong (316 × 108 against a true 211 × 58), which is
exactly the mistake re-tidying would reintroduce.

Three derivatives sit beside it and must be kept in step by hand if the shape
ever changes: `favicon.svg` is the same path reversed out of a black tile,
square because the mark is nearly four times wider than tall and a browser tab
renders that at a height nothing can read; `logo-email.png` is it rasterised at
640 × 176, because mail clients do not render SVG and the layout asks for it at
160 wide; `pdf.mark` draws the same outline a fourth time from `markOutline`, in
fpdf primitives on the same grid, because a PDF wants vectors and not a raster
to keep in step.

The source artwork is a small raster (225 px square, the mark 211 × 58 inside
it), so this trace inherits its quantisation — a few one-pixel jogs on the
chimney tops are the source's, not design intent. **If a vector original ever
turns up, it should replace this outright** rather than be reconciled with it.

The letterhead URL **must be absolute** — mail clients do not resolve relative
paths, Gmail strips `data:` URIs from `<img>`, and CID attachments hurt
deliverability. `EMAIL_LOGO_URL` therefore defaults to `SITE_URL` +
`/logo-email.png` rather than being set by hand, since the asset ships in the
bundle this same binary serves. Set it only to serve the file from elsewhere. No
`SITE_URL` means no origin to make it absolute with, and the templates fall back
to the inn's name in text.

## The booking flow, as built

Search → results → room page → confirm → hold → pay → confirmed. Everything up to
the hold has no Stripe in it at all and still works with no processor configured;
only the last two steps need one.

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
- **The hold sweep is a durable job now** (`booking.SweepJob`, registered on
  `internal/jobs` as `hold.sweep`). It reclaims lapsed holds so an abandoned
  checkout cannot take a room off sale forever. The step-3 ticker is gone.
- **The browser redirect does not confirm anything** — the webhook does. A guest
  who pays and closes the tab has still paid. The consequence is a real gap in
  which the money has moved and the booking still says pending, so the return
  page polls `GET /api/bookings/{code}` and, after 30s, says plainly that the
  payment went through and the booking is catching up. That message is not an
  error and must not read like one.

## The HTTP surface

- **`POST /api/bookings` is rate limited harder than everything else, and that
  is about revenue rather than load.** It needs no account and no payment, and
  every call takes a real room off sale for `hold_ttl_minutes`. With seven rooms
  and no limit, a loop holds the whole inn indefinitely and the owner watches an
  empty house show as fully booked. Reads share a much looser bucket.
- **The limiter's key must not be client-chosen.** chi's `middleware.RealIP` was
  removed: it reads the *first* `X-Forwarded-For` entry, which is whatever the
  caller sent, because Caddy appends rather than replaces. `clientIP` reads the
  **last** hop and only when `BEHIND_PROXY=true`; without it the header is
  ignored entirely. Wrongly on, anyone invents an address and walks around the
  limit — so it is off by default.
- **Per-process, not per-cluster.** One binary on one VPS (decision #2) makes
  that fine. A second box makes the limit per-box, which is a reason to move it
  to Caddy or Postgres, not a reason to have skipped it.
- **The SPA fallback answers GET and HEAD only.** A POST to an unrouted path is
  somebody expecting an endpoint, and answering it with index.html and a 200 is
  worse than answering nothing — see the webhook note below.
- **The CSP already allows Stripe** (`js.stripe.com`, `hooks.stripe.com`,
  `api.stripe.com`) because the Payment Element will not load otherwise. It has
  no `unsafe-inline` for scripts and the Vite build needs none; keep it that way.
  HSTS is asserted only on a request that actually arrived over TLS.

## Step 4: payments

**Built and tested, and almost none of it needed an account:**

- `internal/payments` is the whole state machine, and it **still knows nothing
  about Stripe** — it takes amounts and opaque object ids. That is what lets the
  hard cases be tested against real Postgres with no key and no network.
  `RecordCharge`, `RecordFailure`, `RecordRefund`, `Open`, `Seen`, the T-7/T-8
  scans, and the two balance jobs.
- **`internal/gateway` is where everything Stripe-shaped lives.** It implements
  `payments.Gateway`, an interface of exactly three operations: open a payment,
  charge a saved card off-session, send money back. `payments` defines the
  interface and never imports the SDK. Three implementations — `Stripe`, `Fake`,
  and `Disabled`, which is the default and fails every call.
- `POST /api/bookings/{code}/payment-intent` and the signature-verified
  `POST /webhooks/stripe`, on the **root** router.
- `balance.warn` (T-8) and `balance.charge` (T-7), plus `rates.rebuild`.
- `internal/email` renders the seven messages and queues them as `email.send` jobs.
  **Never send inline** — the queue is the outbox, and its retry is why a Resend
  outage delays a confirmation instead of failing the booking that earned it.
  `Resend` implements `Sender` over plain `net/http` — one endpoint, one JSON
  body — and is selected the moment `RESEND_API_KEY` and `EMAIL_FROM` are both
  set. Like `gateway.Stripe`, it is written and has never made a request. Half a
  configuration logs an error and is treated as none: the binary still starts,
  because the reason mail is queued at all is that email must never stop the inn
  taking bookings.
- The Payment Element, and the return-polling page behind it.

**What the account is still for:** `gateway.Stripe` is written and has never made
a request. Add `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` and
`STRIPE_PUBLISHABLE_KEY` and it is used automatically — no code moves. Then the
verification matrix in ARCHITECTURE.md, which genuinely cannot be faked: test
cards, 3-D Secure, `stripe listen`, and **Test Clocks** for T-8 and T-7.

**`STRIPE_FAKE=true` until then.** It substitutes a processor that mints ids and
takes no money, and the pay page offers a stand-in button instead of a card form.
Everything past that button is real: `POST /api/dev/pay/{code}` builds a properly
signed delivery and sends it through the same webhook handler, signature
verification and state machine a live payment would use. It refuses to exist
unless **no** Stripe variable is set at all and `ENV=dev` — the "no variables"
half is doing the work, because ENV defaults to `dev` and a half-configured
deploy is a mistake to stop on rather than a licence to fake the missing half.

**A webhook signature needs no account to test.** It is an HMAC over the raw body
with a shared secret, so tests hold both ends (`webhook.GenerateTestSignedPayload`).
Read the raw body **before anything else touches it** — a decode-and-re-encode
anywhere upstream makes every genuine delivery fail to verify.

**Verify and decode in two steps**, with `webhook.ValidatePayload` rather than
`ConstructEvent`. ConstructEvent folds a bad signature and an unexpected payload
into one error, and the first is a 401 while the second is not. It also rejects
an API-version mismatch outright, which would fail every delivery until somebody
noticed a dashboard setting; `ParseWebhook` logs that and carries on, because it
reads five long-stable fields off a PaymentIntent and a launch day where no
payment is recorded is the worse outcome. **That is the one judgement call here
worth revisiting** if the fields it reads ever stop being stable.

**Do not gate that call on `payments.Seen`.** `Seen` commits on its own
connection, so marking the event handled out there and doing the work in
`RecordCharge`'s transaction lets the two come apart: the work fails, Stripe
redelivers, `Seen` says "already handled", and a payment that was never recorded
is skipped forever. `RecordCharge` takes the event id and writes it inside its
own transaction for exactly this reason. `Seen` is for event types that write no
payment row.

**The PaymentIntent's amount is derived server-side** from the booking's own
`deposit_cents`/`total_cents`, never read from the request body — otherwise a
guest names their own price. `payments.Open` does it and the endpoint accepts no
body at all. `RecordCharge` will not confirm a stay whose gross collected falls
short of what was due at booking (`Underpaid`), but that is a backstop, not the
control.

**Mail is queued inside the transaction that earns it**, always — the
confirmation and owner copy in `RecordCharge`, the receipt on a balance landing,
the failure notice beside `MarkBalanceChargeFailed`, the T-8 warning beside
`MarkWarned`. The queue is a table, so the message and the fact commit together
and a crash between them cannot lose either. `RecordCharge` confirms
unconditionally but only mails when the status it read at the *start* of the
transaction was not already confirmed; without that, the T-7 charge sends every
guest a second "you're booked" a week before arrival.

**The cancellation email belongs to the cancellation, not to the transfer.**
Queued by whichever transaction cancelled the stay — `refundDue` for a resold
room, `Cancel` for a guest who changed their mind — and *not* by `RecordRefund`,
which runs once per intent being refunded and used to tell a deposit-plus-balance
guest twice, each message naming part of the money. It also has to go out when
the refund is zero: a late cancellation still happened and the guest is owed the
confirmation.

**Email payloads carry money and dates already formatted** (`email.Money`,
`email.Day`). Data crosses the jobs table as JSON and comes back as
`map[string]any`, which would turn integer cents into a float64. Payload JSON
tags match the field names so `{{.Data.Code}}` reads the same either side of the
queue.

**A declined card is not a job failure.** `payments.Declined` from the gateway is
an outcome — flag the booking, mail the guest, leave the stay confirmed. Any
other error is returned so the runner retries, because the money may have moved
and this server does not know. Getting these the wrong way round either mails a
guest hourly or tells them their card failed after it was charged.

Rules that hold whatever gets built on top:

- The `bookings` money columns are **snapshots, not a ledger**: `deposit_cents` and
  `balance_due_cents` describe how the quote splits and never change again. Two
  CHECK constraints depend on that staying true, so record payments rather than
  rewriting the split.
- **`amount_paid_cents` is the gross and only ever grows.** A refund is a row in
  `payments`, never a subtraction (decision #25) — `pricing.Refund` derives from
  what was collected, so reducing it makes a second cancellation compute a smaller
  refund off an already-reduced figure.
- **Idempotency is the unique index on `payments (stripe_id, status)`**, not a
  flag read earlier in the handler. Stripe delivers at least once and redelivers
  on any non-2xx. `stripe_events` covers the event types that write no payment
  row.
  **The `status` half of that key is not decoration.** A declined card is retried
  on the *same* PaymentIntent, so one id legitimately carries one failed row and
  one succeeded row. While the index was on `stripe_id` alone the success
  collided with the failure, `RecordCharge` read the empty insert as "already
  applied", and the guest was charged for a booking that stayed pending until the
  sweeper resold the room. `TestDeclinedCardRetriedOnTheSameIntentStillConfirms`
  is the regression; leave it in place.
- **A late payment is a real case, not an edge case** (decision #24). If the hold
  has lapsed, `payments` re-claims the room through `occupancy.Create` and lets the
  exclusion constraint decide; a lost race means cancel and refund in full. The
  re-claim runs inside a **savepoint**, because losing it raises `23P01`, which
  would otherwise poison the transaction and take the payment record down with it.
- **The refund on that path is a queued job, not a value returned to the caller**
  (`payment.refund`, queued inside the transaction that cancelled the booking).
  `RecordCharge` is idempotent, so a caller that failed to issue the refund would
  get `AlreadyApplied` on the redelivery and the money would never go back — a
  guest charged for a room somebody else is standing in, with nothing anywhere
  left to notice it. The job refunds each collected payment against the intent
  that took it, so a deposit-plus-balance stay produces two refunds rather than
  one Stripe would reject.
- **`QueueRefund` takes an amount, and zero means everything.** Zero is decision
  #24's penalty-free path, which can work the figure out from the ledger when the
  job runs and so stays right if another payment landed in between. A guest's own
  cancellation cannot: what the inn keeps depends on which side of T-7 they
  cancelled, so that amount is settled in the transaction that cancelled the stay
  and carried. A partial refund is spread over the intents **in ledger order**,
  filling each before moving on — reproducibly, because a retry that divided it
  differently would ask Stripe for amounts it had not seen and every one would be
  a fresh refund. Allocations already in the ledger are matched off one for one,
  which is the guard that still holds after Stripe's own idempotency keys age out
  at twenty-four hours.
- **The sweeper leaves mid-payment bookings alone** for
  `settings.payment_grace_minutes`. That protects the booking's bookkeeping only —
  the hold is still reclaimed on its own TTL, so the room always goes back on sale.
- `balance_charge_at` being NULL is the short-notice flag (decision #7), not a
  missing value: those stays are charged in full at booking and have no T-7 job.
- Promotion from hold to booking belongs to the `payment_intent.succeeded`
  **webhook**, not the browser redirect (decision, step 6 of the booking flow).
- **Job scheduling uses the database's clock.** `run_at` defaults to `now()` in SQL
  rather than being stamped in Go; the claim compares against `now()`, and inside a
  transaction that is the transaction's start time, so a Go-stamped "run now" job is
  never due.
- **A panicking job handler costs its job a retry, not the process.** The runner
  is a goroutine, so an unrecovered panic anywhere in a handler takes the binary
  down and the booking API with it. `jobs.run` recovers, records the panic and
  its stack in `last_error`, and backs the job off like any other failure.
- **The departure-morning note matches its day exactly** (`booking.SendCheckoutEmails`,
registered as `checkout.remind`). It runs every fifteen minutes rather than hourly,
because the message is meant to be in the inbox when the guest wakes up on the day
they leave, and an hourly job phased by the last restart can be an hour into it.
It deliberately does **not** catch up the way the T-8 warning does: a late warning
still works, a "you are leaving today" that arrives after the guest got home does
not. `checkout_email_sent_at` is set **in the transaction that queues the email**,
or the same guest hears from the inn ninety-six times that morning.

**The T-8 warning scan catches up and must be marked done.** It looks for
  charges due within a day and not yet warned, so a server that was off for T-8
  still sends it late rather than never — decision #6's whole point is that the
  T-7 charge is not a surprise. Call `payments.MarkWarned` **in the same
  transaction that queues the email**, or the same guest is warned every day
  until they arrive.

## Step 6: admin auth

**Passkeys, and there is no password column anywhere** (decision #15, revised — it
used to say password plus TOTP). `internal/admin` holds it; `users`,
`user_passkeys`, `user_sessions`, `user_enrollments` and `webauthn_ceremonies`
hold the state. One shared owner account, one credential per phone.

**`bealhouse enroll` is the bootstrap and the only way in when no phone is
enrolled.** It proves shell access to the server, which is the strongest thing
available that is not a password. Every enrolment after the first can be minted
from an already-signed-in console. A console with no passkeys and nobody able to
reach the box stays shut — that is the correct failure, not a bug to work around.

**The token is printed, never logged**, and travels in the URL **fragment**: a
fragment is not sent to the server, not written to an access log, and not
forwarded in a Referer.

Four properties, each because the alternative has a specific hole:

- **Sessions are rows, stored as SHA-256 of the cookie value.** Rolling 365 days
  from last use, so a phone in weekly use never signs in again and a lost one
  dies on its own. A stateless token could not be revoked; that is the whole
  reason this is a table.
- **An invitation is single use** — `UPDATE ... RETURNING`, so concurrent racers
  produce exactly one winner. Claimed at the *start* of the ceremony and released
  if it fails, so a mis-tapped Face ID does not burn it.
- **A challenge is consumed once** — `DELETE ... RETURNING`. One that can be
  answered twice is a signature that can be replayed.
- **Every refusal is `ErrDenied`.** Expired, spent, forged and never-existed
  answer identically, or the endpoint tells whoever is trying which guesses were
  close.

**Revoke sessions *before* deleting the passkey.** `user_sessions.passkey_id` is
`ON DELETE SET NULL`, so deleting first blanks the column and the revocation
matches nothing — the lost handset then stays signed in for the rest of its year.
`TestRevokingAPasskeySignsThatPhoneOut` caught exactly this; leave it in place.

**Cookies are `HttpOnly`, `SameSite=Strict`, `Path=/api/admin`,** and `Secure`
whenever the request arrived over TLS — from the request, not hardcoded, or
nobody can sign in on a laptop. Writes also require a JSON content type and
reject a cross-site `Sec-Fetch-Site`; an HTML form can do neither, and it is the
one cross-origin shape that needs no preflight.

**No SITE_URL outside dev means no console.** A WebAuthn assertion is verified
against an origin, and a server that guessed one would accept assertions minted
somewhere else. The routes stay registered and answer 503, so the owner gets a
sentence rather than the SPA's index.html.

**A passkey id crosses the wire as base64url, unpadded** — `admin.credentialID`,
never a raw `[]byte` field. `encoding/json` writes a `[]byte` as *standard*
base64: padded, and containing `+` and `/`. `DELETE /api/admin/passkeys/{id}`
decodes base64url, and a `/` in the value splits the path, so the request 404s
before any handler sees it. While that mismatch stood, the console listed
passkeys with ids that could revoke nothing.
`TestAPasskeyIsRevokedByTheIdTheListGaveOut` asserts the **round trip** — list,
then delete that exact string — because a literal on each side is what let the
two encodings drift apart in the first place.

**Enrolment refuses with its own sentence** (`deniedEnrollment`), not
`forbiddenAdmin`'s "not signed in", which is true of everybody on that page and
says nothing about the link in their hand. It is still one message for expired,
spent, forged and never-existed alike: the wording changes, not what it
distinguishes.

### The console's front end

`web/src/routes/admin/` — the gate and frame in `Console.tsx`, the enrolment
page, and the account screen. `web/src/lib/admin.ts` is its API and
`web/src/lib/webauthn.ts` the browser half of a ceremony.

- **No WebAuthn library.** `PublicKeyCredential.parseCreationOptionsFromJSON()`,
  `parseRequestOptionsFromJSON()` and `credential.toJSON()` are the platform's
  own, and they produce exactly the encoding go-webauthn already writes and
  reads. A second base64url implementation in the bundle would be a thing to
  keep in step with the first for no gain. A browser without them is told so;
  it is not offered a button that fails.
- **Both halves of a ceremony run inside the click.** Browsers require a user
  gesture, so anything begun on mount is refused before a prompt is ever shown.
- **The enrolment token comes out of the address bar as soon as it is read**,
  and is captured in an effect rather than only at mount. A fragment arriving on
  a page already open is a *same-document* navigation: nothing re-mounts, so a
  mount-time read misses it and the clearing step then destroys an invitation
  the owner had just pasted in.
- **The account screen shows two lists, not one** — phones that *can* sign in
  and phones that *are* signed in. They come apart in the case that matters: a
  lost handset is a session to end and a key to strike off, and the owner needs
  to see both to be sure they have done both.
- **A 401 from any console action re-checks the session rather than showing an
  error.** It is not a failure to report; it is a sign-in to ask for.

## Step 5: the manage-booking link

**A booking code is an identifier, not an authenticator.** Six characters over a
32-letter alphabet, read out over the phone and printed on paperwork. Anything
that shows a guest's own details or spends their money asks for the signed token
as well — `GET /api/bookings/{code}/manage` and `POST /api/bookings/{code}/cancel`
both do, and `GET /api/bookings/{code}` deliberately still returns no contact
details.

**The token is an HMAC, not a row** (`booking.Links`). The expiry is inside the
signed message so it cannot be pushed out by editing the URL, and the code is
signed uppercase and trimmed so a mail client that lowercased the link does not
lock a guest out. A stored random token would buy revocation there is nowhere to
offer yet and cost a migration, a write per booking, and no capability at all for
any stay booked before the column.

**`BOOKING_LINK_SECRET` has no default and must not get one.** Generated at boot,
every outstanding link dies on each restart; compiled in, anyone holding this
source can cancel any guest's stay. Unset means no link in the confirmation and
403 from both endpoints — closed, and said out loud at startup.

**A missing token, a forged one, an expired one and a booking that does not exist
all answer the same 403.** Otherwise the endpoint tells anyone willing to try
which six-character codes are real.

**Cancelling is refused once the stay has begun** (`ErrStayUnderway`). Decision
#9's arithmetic has no branch for it — `IsLateCancellation` would call it late and
hand back half the money for a stay being consumed. A no-show is the owner's
conversation and the admin console's manual refund.

**The cancel endpoint moves no money itself.** It cancels the stay, puts the room
back on sale and queues `payment.refund`, all in one transaction. A guest whose
browser gave up waiting on Stripe must never be left with a booking that is
neither cancelled nor refunded.

**Quote before button.** The manage page shows what cancelling returns *today*
before offering to do it, and both figures come from the same arithmetic against
the same civil day (`payments.RefundFor`, then `payments.Cancel`). The browser
never sends an amount.

**The confirmation PDF is rendered on demand, not stored.** `internal/pdf` is
pure — no database, no clock — and does no arithmetic: every figure is the
booking's snapshot, so the document, the email and the page cannot disagree. It
sits behind the same token, because it carries the guest's name. A stored file
would have to be kept in step with a row that still changes, and a stay whose
balance landed this morning must not hand out a PDF saying it is outstanding.

**fpdf's built-in fonts are single-byte, so every string goes through
`render.text`,** which runs the cp1252 translator. Without it a `·` renders as
`Â·` and "Châtelet" is worse. Anything outside cp1252 needs an embedded TrueType
font; that is a change to make when a guest needs it, not a megabyte carried on
the chance.

**The mark is drawn, not embedded** (`pdf.mark`, from `markOutline`) — the same
outline as `web/public/logo.svg`, on the same 211 × 58 grid. Four files now
carry that shape; if it ever changes, they move together.
