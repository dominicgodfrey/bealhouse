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
  **It deliberately does not retry a deadlock**, and putting one back would undo
  a fix rather than add a safeguard: the claims that matter run inside a
  transaction the deadlock has already aborted, so a retry there can only return
  `25P02` and hide the `40P01` that explains it. The advisory lock is what stops
  claims deadlocking; running the whole thing again belongs to the caller, and
  every caller can — the jobs runner, the guest's own request, the owner's
  button.

## Deploying

Everything lives in [`deploy/`](deploy/) and the runbook is
[`deploy/README.md`](deploy/README.md). `BEAL_HOST=… ./deploy/deploy.sh`.

- **The binary carries its own migrations** (`internal/db/migrations/embed.go`,
  `bealhouse migrate up`) as well as the SPA. `go tool goose` reads the same
  files, so there is one history — what embedding buys is that the code on the
  server and the schema applied to its database cannot come from different
  commits.
- **The deploy migrates with the new binary before installing it.** A failed
  migration then leaves the old one serving guests. The consequence is that the
  old binary runs against the new schema for a second or two, so migrations
  should be additive: add a column and backfill in one deploy, require it in the
  next.
- **A rollback restores the binary, not the database**, and the script says so
  when it happens.
- **`/api/health` answers 200 with `"db":"down"`** rather than failing, because
  the binary has to be diagnosable when Postgres is not. Anything checking it —
  the deploy, an uptime monitor — must read the field and not the status code.
- **The backup takes the database and `MEDIA_DIR` as one set**, under one
  timestamp. `pg_dump` does not contain the photographs, so the two are only a
  backup together. `restore.sh drill` restores a set into a scratch database and
  a temporary directory, checks every photo row against a real file, and throws
  both away; `restore.sh verify` runs that check against what is live. Run the
  drill — a backup nobody has restored is a hypothesis.
- **Caddy sets no security headers.** The binary sets CSP, HSTS and the rest,
  and the CSP in particular has to change in step with what the app loads.
- **`BEHIND_PROXY=true` is not optional behind Caddy** and must not be set
  without it. See the note in `deploy/Caddyfile`; both halves or neither.

CI is `.github/workflows/ci.yml`: gofmt, vet, a check that `internal/db/gen`
matches the SQL, and the full suite against a real Postgres on every push —
**under `-race`**, which is the one place it runs, since this machine has no C
compiler. The 100× concurrency suite is nightly, or `workflow_dispatch`.

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
  | console | today+500 |
  | **Playwright** (`web/e2e/`) | today+600, sixty days of it |

- **A package whose fixture claims more than one room per transaction takes
  `Exclusive` too**, and that one is about locks rather than rows.
  `occupancy.Create` takes a per-room advisory lock held to the end of the
  transaction, so two packages claiming rooms in different orders is an AB-BA
  deadlock — the suite failed about one full parallel run in four, in whichever
  package lost, reporting `25P02` ("current transaction is aborted") rather than
  a deadlock. `availability` and `console` are the two. **Nothing in the
  application does this**: a booking claims exactly one room, which is what
  makes the advisory lock sufficient there.

  **The today+30 window is the soft spot.** The date picker opens on the current
  month, so clicking through the booking flow by hand lands a real hold right in
  it, and the availability search tests then fail with a room mysteriously
  missing. If those tests fail that way and pass in isolation, look for a stray
  `pending` booking before suspecting the code.

### The browser suite — `web/e2e/`

```bash
cd web && npx playwright test
```

Playwright, over the real binary and the real database. It **repeats nothing the
Go suite can assert more cheaply** — what it covers is the joins nothing else
can see: that the five screens of the booking flow actually connect, that the
total a guest agreed to is the one the hold is written with, that the `<head>`
the Go server writes reaches the document a crawler fetches, and that the
console's gate is a sign-in rather than the SPA's index page.

- **It starts the server itself**, with `go run ./cmd/server` on `:8099` and
  `STRIPE_FAKE=true`. `go run` and not `bin/bealhouse`, for both reasons the
  environment notes above give: the built file has no extension and a Windows
  shell will not execute it, and a stale `bin/bealhouse.exe` beside it is a
  documented afternoon.
- **It reuses a server already on :8099** outside CI, which is convenient and is
  also a trap — a stray one left from something else means the whole suite runs
  against the wrong configuration and fails in ways that look like application
  bugs (no database, the wrong `SITE_URL` in every canonical). If most of it
  fails at once, check what is on that port before reading the diffs.
- **It books real rooms and clears up after itself**, in the today+600 window
  above. The cleanup runs as globalSetup *and* globalTeardown: a run killed
  partway leaves committed bookings that take rooms off sale for good, and the
  next run then finds nothing available.
- **A booking code is `BH-` and six characters** over an alphabet with no I, O,
  0 or 1 (`internal/booking/code.go`) — not six characters flat, which is what
  the first version of the journey test asserted.

## Content is the owner's, not ours

Room descriptions, photos, amenities, and rate seasons are all managed through the
admin console. Do not invent content, and do not seed guesses.

**The seed is in two halves, and the split is the point.** `rooms.sql` is the
seven rooms as *facts* — occupancy, beds, views, the pet room — and leaves every
description `PLACEHOLDER` so a leak onto the live site is unmistakable rather
than plausible. `content.sql` is *provisional copy transcribed from the inn's
current site*: every sentence in it is the owner's own, taken off a page they
wrote, and it fills amenities and clears the placeholders. Five of the seven
rooms are published there and only one carries a written description, so exactly
one room gets one and the other six are blank on purpose. `menu-mock.sql` is
neither — it is invented structure to exercise the editor, and it must not reach
production. `rates.sql` is one flat placeholder season, and it is the one seed
whose numbers charge a card.

`web/public/placeholders/*.svg` stands in for photos as a **UI fallback** rather
than seeded rows — a placeholder in the database is one somebody has to remember
to delete.

**What is still the owner's to write is tracked in [OWNER-SETUP.md](OWNER-SETUP.md)**,
screen by screen, with what is placeholder in the database today. Keep it true
when the seed or the console changes; it is the document somebody hands to the
owner, and a stale line in it is a job nobody does or one they do twice.

**Email copy** (`internal/email/templates/`) shipped blank on the same reasoning
and no longer does: the owner asked for a starting point rather than eight empty
files, so all eight now carry real sentences. **They are written to be edited,
not to be shipped unread** — the owner's own words replace them from the console,
and the shipped file is what stands in until they do. Each keeps the branch that
carries its meaning (the confirmation says nothing about a balance on a stay paid
in full), every body is wrapped in `{{if .Data}}`, and every subject keeps literal
text *outside* that guard, because `Names()` renders all eight against nil as a
smoke test and a subject that came out empty would send that way. The shared
layout is still not copy and still not editable.

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
`web/public/logo.svg` — three connected buildings, on a **211 × 58** grid, in
the menu's barn red (`#a8241e`). It was ink until the site was brought into step
with the printed menu, where the crest above the wordmark is this same outline
in this same red.

**It is a trace of the owner's artwork, not a drawing of it.** The outline was
walked on the source raster's pixel grid and fitted to straight lines; it
renders within 0.3% of the original's own pixels, and that difference is
anti-aliasing on the roof slopes. The numbers in it are measurements — **do not
tidy them by eye.** The version before it was drawn from a description and had
the proportions visibly wrong (316 × 108 against a true 211 × 58), which is
exactly the mistake re-tidying would reintroduce.

Three derivatives sit beside it and must be kept in step by hand if the shape
ever changes: `favicon.svg` is the same path reversed out of a solid tile,
square because the mark is nearly four times wider than tall and a browser tab
renders that at a height nothing can read; `logo-email.png` is it rasterised at
640 × 176, because mail clients do not render SVG and the layout asks for it at
160 wide; `pdf.mark` draws the same outline a fourth time from `markOutline`, in
fpdf primitives on the same grid, because a PDF wants vectors and not a raster
to keep in step.

**The favicon is the one that is reversed, and it stays that way.** At tab size
the mark is a few pixels of hairline and red-on-paper disappears, which is the
whole reason that file is not just `logo.svg`. So its tile takes the accent and
the mark takes the paper — the same two colours as everywhere else, the same way
round as the owner's artwork, which was white on black.

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

**The marketing pages ship as structure with the owner's content in it.** Rooms,
restaurant, events, about and local area all read live data — the seven rooms,
the menu, what is on — plus an optional prose slot from `page_copy`. **A page
with nothing written renders no paragraph at all**, not a placeholder: the
restaurant says the menu is not up and to ring the inn, the events page shows
only its form. That is honest and looks deliberate, where an invented sentence
about the food would sit on the public internet until somebody remembered it.

**The inn is "The Beal House", with the article**, which is how it names itself.
One string, `innName` in `internal/httpx/meta.go` and `inn.name` in
`web/src/lib/contact.ts`.

**The site is set in the dinner menu's type and painted in its palette**
(`index.css`). Fraunces for the wordmark and every heading, Karla for
everything else, warm ink on warm paper with one barn red accent. The printed
menu is the inn's identity and a site in a different face is a different inn.

- **There are webfonts now, and there deliberately were not before.** The rule
  that stood here — Optima then Candara, already installed everywhere, zero
  bytes, no FOUT — is still the fallback stack and still what renders during
  `swap`. What is paid for the match is held down on every axis available:
  **self-hosted** in `web/src/fonts`, so `font-src 'self'` in the CSP is
  untouched and there is no third-party request on the critical path;
  **variable** files, so one download covers 300–600 rather than one per
  weight; `font-display: swap`; and `unicode-range`, so the latin-ext files
  only arrive on a page that has a character in them. A **first** visit pays
  ~99 KB. **Do not reach for Google's CDN** — it costs two CSP hosts and a
  render-blocking hop for nothing this does not already have.
  - **`src/fonts` and not `public/fonts`, and that is what makes "first visit"
    true.** Referenced relatively from `index.css`, Vite fingerprints them into
    `/assets`, which `serveSPA` already answers `immutable` for a year. In
    `public/` they would keep their own names and be served `no-cache` — and
    `no-cache` needs a validator to become a 304, which the embedded bundle
    cannot supply: `embed.FS` files have no modification time, so nothing sets
    `Last-Modified` and nothing sets an `ETag`. All ~99 KB would go out again on
    every cold load. It also means swapping a typeface renames the file by
    itself rather than by somebody remembering to.
- **`font-serif` is a real second family again**, not the alias to the sans it
  used to be. Headings take it through one scoped element rule in `index.css`
  rather than a `font-serif` added to forty elements that would then have to be
  kept in step.
- The `.tabular-nums` rule is not decoration: Karla's default figures are
  proportional, as Candara's are old-style and Georgia's were before it, and
  money in a column needs the lining set.

**The palette is the menu's, and it is applied by redefining the ramp rather
than by rewriting the utilities.** Tailwind v4 compiles `text-neutral-700` to
`var(--color-neutral-700)`, so `.site` — one class, on `Layout`'s root element —
redefines `neutral-50…950` and `white` to the menu's paper (#f7f1e8), rule,
muted and ink (#33201c), and everything underneath repaints. The dark end is
checked and not eyeballed: `neutral-500` on paper is 5.1:1, which is where the
small print lives.

  **`Layout` is the seam, and that is why the console is untouched.** The public
  pages render a `Layout`; the console imports `ErrorNote` and `Loading` from
  that file but never the shell, so it keeps the grey ramp *and the old humanist
  stack* from `:root` — the faces are scoped to `.site` exactly as the colours
  are, which is also why the console fetches no webfont at all. It is a tool,
  not the inn's front, and the inn's paper stock behind a table of bookings
  would be costume. Nothing public renders outside that element — there are no
  portals — so there is no second place to remember.

  **`--color-sienna` and `--color-sienna-line` are still a pair and still mean
  what they meant** — the fill on every panel the public site has (cards, forms,
  the menu's rules, the calendar) and the same hue carried past it to read as an
  edge. Only the values moved, onto the menu's second ground. A
  `border-neutral-200` on one of these is still the wrong line.

  **The barn red is one accent and stays one.** The mark, and the rule under the
  site header, which is the menu's masthead rule. Money stays ink: a price in
  red reads as a sale.

**The home page is one screenful and does not scroll**, on a desktop monitor and
on a phone alike (`Layout`'s `fills`). Header, the search under it, the
restaurant and events buttons above the footer, the footer on the bottom edge,
and the whole middle left clear because what is behind it is the house. That
constraint is the design rather than a style: a page that can scroll always has
room for one more paragraph, and this one has to decide what it is for. Anything
added to it comes out of the empty middle — it does not get a scrollbar.

- **The backdrop is a slideshow**: `page_photos` for slug `home` — the house —
  then the local area page's own photographs, cross-fading and looping. Every
  one is in the DOM with only opacity animating, so it never shows a blank frame
  and never touches layout. One photograph starts no timer, and neither does a
  visitor who has asked for less motion. It becomes a `<video>` the moment
  `backdropVideo` in `Home.tsx` names one, with the first photograph as poster.
- **The calendar starts closed and floats**, never pushing the page (SearchForm's
  `overlay`). It hangs off the bottom of the field **at every size**, phone
  included. It used to become a sheet pinned to the bottom of the viewport below
  a `roomy` breakpoint, and that variant is gone: it put the panel somewhere
  different on a phone than on a monitor for no reason a guest could see, with
  the field it belongs to up the screen and the panel against the bottom edge.
  - **What the breakpoint was guarding is still real**, and is now guarded by
    measurement. The home page cannot scroll, so a panel taller than the space
    under the field has a bottom that no gesture can reach. `fit` in SearchForm
    gives it exactly the room between its own top edge and the viewport, and it
    scrolls inside that. **Not a `max-h-[calc(100dvh-…)]` class**: the constant
    such a class needs is how far down the field ends, and that moves with the
    header, with the fields going from stacked to side by side, and with the
    dates line wrapping once a range is chosen.
  - The trade is a **landscape phone**, where there are about 155px under the
    field and the calendar is a short scrolling panel. The sheet had more room
    there. Portrait, which is what a phone books in, gains the whole thing.
  - **Nothing between that panel and the viewport may carry a
    `backdrop-filter`**: a blurred ancestor becomes the containing block for
    absolutely positioned descendants as well as fixed ones, and the panel would
    pin itself to the card it is trying to escape.
- **Search with no dates opens the calendar** rather than sitting greyed out.

**Everything that used to be below the fold there is on `/about`**: the owner's
story, the address, the telephone, a map and the contact form. It is a page that
can be as long as it needs to be, and unlike the About page `/local-area`
replaced, it is never empty — the address and the map are facts about the inn
rather than copy somebody has to write.

**The address and telephone are site chrome, not `page_copy`.** They are in the
footer of every page including the ones with no prose slot at all, and an empty
console field must not be able to take the telephone number off the site. They
live in two files — `meta.go` for the structured data and `web/src/lib/contact.ts`
for the pages — with no build step between them, so **change both**.

**The map is OpenStreetMap's iframe embed**, which is why `frame-src` in the CSP
names it. An iframe and no third-party JavaScript on the one public page that
also has a form on it, no API key, and one CSP entry rather than two.

**The `<head>` is written by the server, per route** (`internal/httpx/meta.go`, decision #3). The
SPA is one document for every address, so the fallback fills in that page's title, description,
canonical, Open Graph tags and JSON-LD before serving it.

- **It reimplements no read model.** The rooms in the head come from `roomCards`, which is what
  `GET /api/rooms` answers with; the menu is `ops.PublicMenu`; the prose is `ops.PageFor`. A
  second query assembling them slightly differently is how the document a crawler indexes ends
  up quoting a price the page does not show.
- **Vite's static `<title>` is stripped from the shell**, not joined by a second one. A browser
  takes the first and a crawler may take either, which is the failure that looks fine on screen
  and puts "Beal House" on all seven room results.
- **A description is the owner's words or nothing.** Same rule as the pages: no copy written means
  no `<meta name="description">` at all, because an empty one tells a search engine the page has
  nothing to say. The home page's fallback is the one sentence this repository supplies and it
  says only what the footer already says.
- **Absolute URLs need `SITE_URL`**, exactly like the email letterhead. No origin means no
  canonical, no `og:url`, no `og:image` and no `sitemap.xml` — a `<loc>` is defined as absolute
  and a file full of relative ones is rejected whole.
- **This is the one place console text becomes markup.** Page copy is plain text with no markdown
  parser so that the console cannot put a `<script>` on the public site; `html/template` for the
  attributes and `json.Marshal`'s `<` escaping inside the `ld+json` blocks are the other
  half of that. Two different escapers — `TestTheOwnersWordsCannotEscapeTheDocument` asserts both.
- **The booking flow and the console are `noindex` with no canonical**, and `robots.txt`
  `Disallow`s them. A GET under `/book` or `/bookings` ends in a hold, so a crawler walking them
  takes real rooms off sale for the TTL — decision #29's problem arriving politely.
- **`robots.txt` and `sitemap.xml` are on the root router ahead of the SPA fallback**, for the
  reason `/media/*` is: answered by the fallback they would be a page of HTML with a 200, and a
  crawler parsing that as a rule set does something nobody predicted.
- **Nothing here may fail a request.** A query that errors costs the structured data and logs a
  warning; the visible page is what the visitor came for.

**Photographs upload from the console** (`internal/media`, decision #16). An
image arrives, is decoded, scaled so its longest side is at most 2400px,
re-encoded as JPEG and written to `MEDIA_DIR` under a name that is the SHA-256
of its own bytes. `/media/*` serves it.

- **Re-encoding is not polish.** A phone photograph is 4000px and several
  megabytes, and serving that to somebody on mountain mobile data fails the one
  job the page has. Decoding is also the *only* real check that a file is an
  image — its name and its content type are both the caller's to invent.
- **Content addressing buys three things**: the same photograph uploaded twice
  is one file, two uploads cannot collide, and the URL can be `immutable`
  because the bytes at a name can never change. The corollary is that
  **removing a photo from a room does not delete the file** — two rooms may
  point at the same bytes, and an orphan costs kilobytes where a wrong delete
  costs a photograph off somebody else's page.
- **`/media/*` is registered before the SPA fallback**, which is the whole point
  of it being on the root router. The fallback answers unknown GETs with
  index.html and a 200; for an `<img>` that is a broken picture with no error
  anywhere. A missing photograph 404s.
- **`media.Name` is the only way a stored path becomes a filename.** It refuses
  separators, dot-files and anything not under the prefix, so whatever ends up
  in the database cannot address a file outside the directory.
- **`MEDIA_DIR` is not in the binary and not in `pg_dump`.** On the VPS it
  belongs somewhere a deploy does not overwrite and the nightly backup does
  reach — a restore that brings back the paths and not the files is a site of
  broken images.
- **Alt text is required on every one** — the column is NOT NULL, the save
  refuses a blank, and the editor marks the box amber until it has one. A room
  with no photo falls back to `availability.PlaceholderPhoto(slug)`.

**An upload produces a ladder: 480/960/1600/2400, in JPEG and WebP.** The page
picks with `srcset`, through `web/src/components/Photo.tsx`.

- **The widths matter far more than the formats.** Measured: the 960px JPEG is
  76 KB against 955 KB at 2400px — twelve times — where WebP saves a further
  half at the same width. A card four hundred CSS pixels wide was downloading
  the full-size file, and no format recovers that.
- **The rung is in the filename** (`<hash>-w960.webp`), so which rungs exist is
  knowable from the stored path alone. That is what makes `media.Sources` a
  **pure function** — no directory listing, no column, no Store threaded through
  three packages — and why the srcset can never name a file that was not
  written. A 404 inside a srcset does not fall back; it is a broken image.
- **`src` is always a whole file, never a rung.** The canonical JPEG is the
  fallback and works in anything.
- **The hash is still of the canonical JPEG**, so dedup and `immutable` hold for
  it. The rungs are named *from* that hash, so they are only as immutable as the
  encoder settings: changing `jpegQuality`, `webpQuality` or the ladder means new
  canonical names, not new bytes at old ones. Changing `maxEdge` gets that for
  free; a quality change does not.
- A path stored before the ladder existed gets **no srcset at all** and renders
  the plain `<img src>`. Nothing to migrate.

**AVIF is deliberately not built.** It is feasible with no cgo — the same
WASM-backed family — and measured at −61% against JPEG at full size. It costs
5.3 MB of binary and ~1.7s per upload, and that second number is what decides
it: it would push the work into a background job, which then needs the API to
report which variants exist *yet* so a `<picture>` never points at a file not
written. JPEG plus WebP takes about half a second and needs none of that.

**The encoder is `gen2brain/webp`, chosen for one property:** libwebp compiled
to WebAssembly and run under wazero, so it builds with `CGO_ENABLED=0` on a
machine with no C compiler — this one. The pure-Go alternatives encode lossless
VP8L only, which for a photograph is larger than the JPEG it replaces. It costs
about 5 MB on the binary.

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
  `api.stripe.com`) because the Payment Element will not load otherwise, and
  `www.openstreetmap.org` in `frame-src` for the About page's map. It has no
  `unsafe-inline` for scripts and the Vite build needs none; keep it that way.
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
- `internal/email` renders the eight messages and queues them as `email.send` jobs.
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

## Notifications to the owner's phone — `internal/push`

**Beside the email, never instead of it.** A booking or an inquiry reaches the
handset while the console is shut. Web Push with a VAPID pair, a service worker
in `web/public/sw.js`, and `push_subscriptions` (migration 00023).

- **Shaped exactly like `internal/email`, on purpose:** a durable job
  (`push.send`) on `internal/jobs`, a `Sender` with a real implementation and a
  logging one, and **nothing sent inline**. `push.Queue` takes a `*db.Queries`,
  so the notification commits with the booking that caused it.
- **A subscription can die, and that is the one piece of state this package
  owns.** A push service answering 404 or 410 means the browser is gone for
  good, so `ErrGone` is its own error and the handler **deletes the row** rather
  than retrying it forever while everything queues up behind it. Every other
  status is a retry.
- **One job fans out to every browser**, not a job per handset. The thing being
  retried is the notification, so a phone that was unreachable gets it on the
  retry — at the cost of re-sending to the ones that already had it. With two
  handsets that trade is the right way round; with two hundred it would not be.
- **A notification is a nudge, not a payload.** Title, body, a path to tap
  through to, and a `Tag` that collapses supersessions — distinct per booking
  code rather than per kind, because two bookings are two things to see.
  Anything richer would be a second read model to keep in step with the console
  that is one tap away.
- **`bealhouse vapid` mints the pair, and both halves are required together.**
  Half a pair is an error in the log and treated as none. Generating a new pair
  **turns every phone off** — each has to subscribe again — which is why the keys
  are deployment configuration and not a setting.

## Step 6: admin auth

**Passkeys, and there is no password column anywhere** (decision #15, revised — it
used to say password plus TOTP). `internal/admin` holds it; `users`,
`user_passkeys`, `user_sessions`, `user_enrollments` and `webauthn_ceremonies`
hold the state. One shared owner account, one credential per phone.

**`bealhouse enroll` is the bootstrap and the only way in when no phone is
enrolled.** It proves shell access to the server, which is the strongest thing
available that is not a password. Every enrollment after the first can be minted
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

**Enrollment refuses with its own sentence** (`deniedEnrollment`), not
`forbiddenAdmin`'s "not signed in", which is true of everybody on that page and
says nothing about the link in their hand. It is still one message for expired,
spent, forged and never-existed alike: the wording changes, not what it
distinguishes.

### The console's front end

`web/src/routes/admin/` — the gate and frame in `Console.tsx`, the enrollment
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
- **The enrollment token comes out of the address bar as soon as it is read**,
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

### What the console does — `internal/console`

**Everything the owner does once signed in lives in one package, and it
reimplements no rule.** Claiming a room is `occupancy.Create`, pricing a stay is
`availability.Search`, refunding is `payments.Cancel` / `QueueRefund`,
regenerating the calendar is the SQL function, and saving email copy is
`email.Parse`. What the package adds is the read models — one query per screen,
in `internal/db/queries/console.sql` — and the transactions holding a multi-row
save together. `internal/httpx/console.go` above it only decodes and encodes.

The console is where the invariants can be walked around *by accident*, because
there is a person on the other end who is allowed to do things a guest cannot.
"Allowed to" is not "unconstrained":

- **A manual booking is `booking.Create` with `Manual` set**, not a second write
  path. Same availability re-check, same pricing, same claim through the
  exclusion constraint — an owner taking a booking by phone must not be able to
  double-book a room a guest on the website could not. The flag changes exactly
  three things: confirmed rather than pending, an occupancy row of kind
  `booking` rather than an expiring `hold`, and `balance_charge_at` left NULL
  because there is no saved card for a T-7 job to charge.
- **A manual booking sends the same mail a website booking does.** The
  confirmation and the owner's copy are queued by `payments.QueueConfirmation` —
  the *same* payload builder the payment path uses, not a second construction of
  it, because two guests with the same booking must not be able to read different
  accounts of what they owe. The departure-morning note follows on its own, since
  that scan matches confirmed stays by checkout date and this is one. The two
  balance messages do not and must not: they announce and then take money from a
  saved card, and there is none.
- **There are three ways a phone booking gets paid for**, chosen on the form and
  changing nothing about the stay itself: settled offline, an emailed link
  (`payment_request`, the eighth template), or a card keyed in on the spot.
  Offline is the default, so an owner who does not read that section has not
  silently invoiced anybody.
  **`payments.Open` therefore accepts a confirmed booking** — but only one with
  `balance_charge_at` NULL and money outstanding, which is exactly this case. A
  confirmed booking that *does* have a charge date is a website booking whose
  card is on file and whose balance the T-7 job will take; letting a page collect
  it early is how a guest pays twice. `RequestPayment` refuses that booking for
  the same reason.
  Money landing on an already-confirmed stay is labelled `KindBalance`, so
  `RecordCharge` sends a receipt rather than a second "you're booked" — without
  it a guest paying an emailed link would be charged and told nothing at all.
- **Keying in a card is `payments.OpenKeyedIn`, and no card number touches this
  server.** The console mounts Stripe's own Payment Element against a client
  secret and the digits go from that iframe to Stripe, exactly as on the guest's
  pay page. **There is no endpoint here that accepts a card number and there must
  never be** — that is the whole of PCI SAQ-A for this inn.
  The intent carries `MOTO`, which is not a convenience flag: a guest on the
  telephone cannot answer a 3-D Secure challenge, because the challenge goes to
  *them*. Declaring the payment as mail-order/telephone-order exempts it, and
  moves fraud liability to the inn — the honest trade for taking a card nobody
  has seen, and what a telephone booking has always meant. It also has its own
  Stripe idempotency key, or an owner reaching for the card after emailing a link
  is handed the browser's intent, wallets and all, and watches it decline.

  **`BalanceDue` and `BalanceChargeOn` are set by two separate conditions** for
  exactly this reason. They came apart when the console started taking phone
  bookings — money outstanding, no date it will be collected on — and tying both
  to `balance_charge_at` sends that guest a confirmation shaped like *paid in
  full*, which is the one thing it must not say. Nothing changed for the website
  path: a deposit still leaves both set, a short-notice stay still leaves neither.
- **`booking.Request.AfterCreate` is what queues it**, inside the transaction
  that wrote the booking and *after* the room is claimed. A hook rather than
  `booking` doing the mailing itself, because the payload is built in `payments`
  and `booking` cannot import it — `payments` already imports `booking`. The
  console imports both and is where the two are joined.
  `TestARefusedManualBookingQueuesNoMail` is the property that matters: a booking
  that loses the race for its room must not have told anybody it happened.
- **Blocking goes through `occupancy.Create`** for the same reason, so an owner
  blocking nights a guest is halfway through paying for is refused rather than
  overriding them. **Unblocking filters on `kind = 'block'` in the SQL**, not in
  Go: an id naming a booking's occupancy row must match nothing, or a paid stay's
  room goes back on sale with the guest still arriving.
  `TestUnblockingWillNotReleaseABooking` is that property.
- **The rate preview applies the edit, takes the diff, and rolls back.** That is
  what makes the number the real season-resolution rule rather than an estimate
  from a second copy of it — nothing else could account for a lower-priority
  season underneath the one being edited. `rate_calendar_changes()` and
  `generated_rate_calendar()` in migration 00015 are that resolution lifted out
  of `rebuild_rate_calendar`, which now calls it, so there is one copy and not
  two. **If the preview ever commits, an owner asking "what would this do" has
  already done it** — `TestPreviewingASeasonLeavesTheCalendarAlone` guards it.
- **A manual refund refuses zero.** `payments.QueueRefund` reads zero as "the
  whole ledger" on purpose (decision #24's penalty-free path), but an owner
  leaving the amount box empty means *nothing*, and the gap between the two
  readings is a whole stay's money.
- **The menu, the events list and a room's photos save as whole documents** in
  one transaction. That is how they are edited — reordered, moved between
  sections, repriced across a whole course in one sitting — and reconciling it as
  a stream of per-row edits would be a diff algorithm on the client whose failure
  mode is a half-applied menu on the public site. This way the failure mode is
  the previous menu, unchanged. `sort_order` is the array index at save time and
  never a number anybody types.
- **`Ops` takes a `Store`, not a pool** — `db.DBTX` plus `Begin` — so tests pass
  an open transaction whose nested `Begin` is a savepoint and roll back like the
  rest of the suite. Same arrangement as `booking.Beginner`.
- **What a template can say is served, not listed in the bundle.**
  `email.Fields(name)` reflects over the payload struct's JSON tags, so the
  editor's field list cannot drift from what the message actually carries. A
  name that is not in it renders as nothing, silently — which is exactly the
  mistake a hand-kept list causes.

**Page prose is `page_copy`, on the same terms as `email_templates`:** a row is
an override, no row means the page renders its structure with nothing in that
slot, and emptying both fields is a DELETE rather than a row of empty strings.
Plain text, not markdown — blank lines are paragraphs — because the alternative
is a parser in the bundle or a way to put a `<script>` on the public site from a
phone. The pages are `console.PageSlugs()`, a property of the binary.

  **`home` is in that list and renders no paragraph anywhere.** Its slot is the
  backdrop photograph behind the search and the meta description a search engine
  shows under the inn's name, which is why the console labels it as those two
  things rather than as "the home page".

  **`local_attractions` has a description per entry**, editable on the same
  screen. The sentences in the seed were written here and not by the owner —
  their site lists names and distances alone — so they are, with the links, the
  rows most worth their review. An entry with no description renders as a name
  and a distance, exactly as every entry did before the column.

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

**Which is also why the PDF is the one surface not set in the site's faces.**
It takes the menu's colours — ink, the muted brown, the pale rule, the mark in
barn red — and keeps Times and Helvetica, because Fraunces and Karla here mean
embedding two TrueType files and giving up the built-in cp1252 handling above.
It does not take the paper: a guest printing this at home would spend a
cartridge laying #f7f1e8 over paper that is already that colour.

**The mark is drawn, not embedded** (`pdf.mark`, from `markOutline`) — the same
outline as `web/public/logo.svg`, on the same 211 × 58 grid. Four files now
carry that shape; if it ever changes, they move together.
