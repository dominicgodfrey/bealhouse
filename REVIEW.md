# Review of step 4, and what step 4 still has to get right

A review of the payments half that was built without Stripe keys, the job runner
under it, and the HTTP surface all of it sits on. Nine issues; seven are fixed
here, two are deliberately left.

Everything below was checked against a real Postgres. The bugs marked
*reproduced* were demonstrated failing before being fixed, not inferred from
reading.

---

## Fixed

### 1. A declined-then-retried card dropped the payment — *reproduced*

**`payments (stripe_id)` was unique on the id alone.** `RecordFailure` writes a
row keyed on the PaymentIntent id, and the Stripe Payment Element retries a
declined card **on that same intent** — it returns to `requires_payment_method`
and the guest enters another card. So the eventual `payment_intent.succeeded`
collided with the failed row, `ON CONFLICT DO NOTHING` returned nothing,
`RecordCharge` read that as "already applied" and stopped before recording
anything.

Observed before the fix, on a real booking:

```
outcome="already_applied" status="pending" amount_paid=0 expected=16275
```

The guest's second card is charged $162.75. The booking stays `pending`, the
hold lapses on its TTL, and the sweeper puts the room back on sale. The ledger
says nothing was collected, so nothing downstream — not the confirmation, not
the admin's paid-vs-owed view, not a refund — has any idea.

A first card declining is routine, so this would have shown up in the first
week of live traffic.

**Fix:** migration 00010 replaces the index with a unique on
`(stripe_id, status)` — one failed attempt and one success per intent are two
different facts, and both belong in the ledger. Idempotency is untouched: a
redelivered event still carries the same pair and still inserts nothing.
Recorded as decision #28. Regression:
`TestDeclinedCardRetriedOnTheSameIntentStillConfirms`.

### 2. Webhook idempotency committed separately from the work it guarded

`payments.Seen` takes a pool-backed `*db.Queries`, so it commits on its own;
`RecordCharge` opens its own transaction. The handler shape the docs prescribed
— verify, `Seen`, `RecordCharge`, 200 — put an event id on record *before* the
work, in a different transaction. If `RecordCharge` then failed, the handler
returned 500, Stripe redelivered, `Seen` answered "already handled", and the
handler skipped a payment that was never recorded. Permanently, since Stripe
gives up after its retry window.

**Fix:** `Charge.EventID` is written inside the same transaction as the payment,
so the event and the payment commit or roll back together — a rollback leaves
the event unrecorded and a redelivery does the work. `Seen` stays for event types
that write no payment row, with a doc comment saying plainly that it must not
gate the three that do.

### 3. A panicking job handler took the whole binary down

`middleware.Recoverer` covers HTTP. The runner had nothing, and `go runner.Run(ctx)`
is a bare goroutine, so one nil deref or short slice in any handler killed the
process — including the API taking bookings. `balance.charge`, which will do
arithmetic on Stripe response structs, is exactly where that lives.

**Fix:** `jobs.run` recovers, records the panic and its stack in the row's
`last_error`, and backs the job off like any other failure.

### 4. Nothing checked that the money covered the booking

`RecordCharge` confirmed whenever the room was secured, at any amount. That is
safe while the amount comes from a signature-verified webhook, but
`POST /api/bookings/{code}/payment-intent` is the next thing to be written, and
if it reads an amount from the request body a guest confirms a $325 stay for $1.

**Fix:** `RecordCharge` compares the gross collected against the booking's own
snapshot of what was due at booking and returns `Underpaid` rather than
confirming. The money is still recorded — it happened — and the hold is left to
lapse. Topping up confirms, so a genuine two-part payment is not stranded.

**This is a backstop, not the control.** The control is that the endpoint derives
the amount server-side. See *Still to do* below.

### 5. A missed T-8 meant the guest was never warned

`ListBookingsToWarnAboutBalance` matched `balance_charge_at` on the exact day,
while the charge scan uses `<=` and self-heals. One day of downtime and the card
was charged at T-7 with no warning at all — the surprise decision #6 exists to
prevent.

**Fix:** the scan looks a day ahead and takes anything not yet warned;
`bookings.balance_warned_at` stops it repeating. Marking must happen in the same
transaction that queues the email — `payments.MarkWarned` says so.

### 6. `POST /api/bookings` was an unmetered way to take the inn off sale

The most exploitable thing in the codebase. No account, no payment, no captcha,
and every call takes a real room off sale for `hold_ttl_minutes`. Seven rooms. A
ten-line loop holds the entire inn indefinitely, re-firing as the sweeper frees
each hold, and the owner watches an empty house show as fully booked. Not a
server-load problem — a denial of service on the business.

**Fix:** a per-address token bucket, tight on bookings (burst 5, one per six
minutes) and loose on reads (burst 40, one per second). Verified end to end
against the running server: five bookings through, then 429 with `Retry-After`,
reads unaffected. Recorded as decision #29.

### 7. The rate limit key was spoofable, so it would not have held

chi's `middleware.RealIP` reads the **first** `X-Forwarded-For` entry. Caddy
appends rather than replaces, so a caller who sends `X-Forwarded-For: 1.2.3.4`
arrives as `1.2.3.4, <real client>` and the first value is theirs to choose. A
new value per request would have bought a fresh bucket every time and made
fix #6 decorative.

**Fix:** `clientIP` reads the **last** hop — the one the trusted proxy appended —
and only when `BEHIND_PROXY=true`. Unset, the header is ignored entirely, because
an app on a public port cannot tell a forwarded address from an invented one. Off
by default: wrongly on is a bypass, wrongly off is merely too strict.

### 8. Also hardened, while in there

- **The SPA fallback answered any method with 200 and index.html.** So
  `POST /webhooks/stripe` — before that handler exists — looked *delivered* to
  Stripe, which marks the event done and never retries. A misconfigured or
  not-yet-deployed webhook would have silently dropped live payments. It now
  serves GET and HEAD only and 404s the rest.
- **Security headers:** `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`,
  and a CSP written for what the app actually loads — no `unsafe-inline` for
  scripts, and `js.stripe.com` / `hooks.stripe.com` / `api.stripe.com` already
  allowed so the Payment Element is not a debugging session later. Verified by
  loading the real built bundle: renders clean, no CSP violations, the calendar
  fetch works. HSTS only on requests that actually arrived over TLS.
- **A 20s request timeout**, inside the server's 30s `WriteTimeout`.

---

## Deliberately not fixed

**A permanently failing job retries forever.** `attempts` increments and nothing
reads it but the backoff, so a poison payload spins hourly until someone looks in
`last_error`. Left alone because the honest fix is the admin console's job view
(step 6) — a dead-letter state nobody can see is not an improvement. Capped at an
hour, so the cost is one query per job per hour.

**A queued email can send twice.** The job is leased, not deleted, so a process
that dies between `Send` and `DeleteJob` re-sends. Inherent to at-least-once
delivery and correctly documented as such. The fix belongs with the Resend client
in step 5: pass an idempotency key derived from the booking code and template
name, and Resend will drop the duplicate. Noted here so it is not forgotten when
`LogSender` is swapped out.

---

## What step 4's remaining work must get right

The Stripe-dependent half is unchanged in scope. These are the constraints the
review either established or confirmed.

**`POST /api/bookings/{code}/payment-intent`**

- **Derive the amount from the booking, never from the request body.** Read
  `deposit_cents` or `total_cents` off the row by code. The `Underpaid` guard is a
  backstop; this is the control.
- Put the booking **code** in the PaymentIntent metadata. `RecordCharge` already
  reads it from there rather than from the browser, which is what stops a guest
  attaching their payment to somebody else's stay.
- Set `setup_future_usage: 'off_session'` or the T-7 balance charge has no saved
  method to use.
- Call `payments.StartPayment` so the sweeper leaves the booking alone for
  `payment_grace_minutes` while a 3-D Secure challenge is on screen.
- Refuse when the booking is not `pending`, and when `StripeConfigured()` is false.

**`POST /webhooks/stripe`**

- **Register it on the root router, above the SPA fallback.** The fallback now
  404s a POST instead of answering 200, which turns a silent failure into a loud
  one, but the route still has to exist.
- **Verify the signature against the raw body**, before any JSON decoding. Read
  the body once, verify, then parse — a decoded-and-re-encoded body will not
  match the signature.
- Pass the event id as `Charge.EventID` and Stripe's own event name as
  `Charge.EventType` — `stripe_events.type` is an audit column, and an audit
  table recording the inn's word (`deposit`) instead of Stripe's
  (`payment_intent.succeeded`) cannot be reconciled against the dashboard.
  **Do not** gate the call on `payments.Seen` (issue #2).
- Answer 200 for `AlreadyApplied` — the work is done, and a non-2xx only asks
  Stripe to deliver the same thing again.
- `RefundDue` and `Underpaid` both mean *a person has to know*. Neither can be
  left to a log line nobody reads; both need the owner notification that step 5
  builds.
- Reject everything when `StripeConfigured()` is false rather than processing
  unverified events.

**`balance.charge`**

- Idempotent by construction: the T-7 scan stops returning a booking the moment
  `amount_paid_cents` reaches `total_cents`, so re-running it costs nothing.
- The handler must tolerate running twice — leases lapse — and must not panic on
  a Stripe response it did not expect. It will not take the server down now, but
  it will burn its retries.
- Set `balance_charge_failed_at` through `RecordFailure` on a decline. The stay
  stays confirmed; the guest is still arriving.

**Test with Stripe Test Clocks, not by waiting.** Fast-forward a real test
booking through T-8 and T-7 and assert the warning, the off-session charge and
the failure path. It is the only honest way to verify decision #6 without waiting
a week — and now also the way to check that the T-8 warning marks itself done.

Add to the webhook matrix, given what this review found: **a declined payment
followed by a successful retry on the same PaymentIntent.** That is issue #1 end
to end, and the unit test covers the ledger but not the Stripe object lifecycle.

---

## Residual risks worth naming

**The booking rate limit is per address and per process.** An attacker with a
botnet still gets through, and a second server would make the limit per-server.
The stronger control — if holds ever become a real problem — is a cap on
concurrent pending holds, enforced in the database where the limit is global. Not
built, because there is no evidence it is needed and the cheap fix removes the
trivial attack.

**Booking codes are enumerable.** ~2³⁰ codes (32⁶) with `GET /api/bookings/{code}`
unauthenticated. The read limiter now makes walking the space slow, and the
endpoint deliberately withholds name, email and phone — but dates, room, prices
and status are readable for any code guessed. The real fix is the signed expiring
link in decision #19, which step 5 builds; until then this is throttled rather
than closed.

**`BEHIND_PROXY` must be set to `true` at deploy time.** Left unset behind Caddy,
every guest shares one rate-limit bucket, because `RemoteAddr` is the proxy for
all of them. That degrades to "too strict" rather than "bypassable", which is the
right way round, but it will look like a mysterious 429 under normal traffic.
This belongs on the step 8 launch checklist alongside the DNS cutover.

**No CSRF protection on `POST /api/bookings`.** It is same-origin only, takes JSON,
and rejects unknown fields, which stops a form-encoded cross-site post. Worth
revisiting when the admin console adds authenticated mutating endpoints in step
6 — that is where CSRF actually bites.

---

## Verification

`go build ./...`, `gofmt -l .`, `go vet ./...` all clean. Full suite green.
Concurrency paths run 30× after the change, since `payments` claims rooms through
`occupancy.Create`:

```
ok  bealhouse/internal/occupancy  21.756s
ok  bealhouse/internal/booking    16.235s
ok  bealhouse/internal/payments   28.157s
```

New tests: six in `payments` (retry collision, redelivered failure, underpayment,
top-up, event-id atomicity, warning catch-up), one in `jobs` (handler panic), and
seven in `httpx`, which had none before (SPA method guard, headers, HSTS-over-TLS
only, booking limit, spoof resistance, bucket refill, no free refill).
