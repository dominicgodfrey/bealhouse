# What still needs the owner

Everything in this list is done from the admin console at `/admin`, on a phone or
a laptop. **Nothing here needs a developer and nothing here needs SQL.** Where a
step says a path, that is the address to go to — `/admin/rates` means the console
address with `/admin/rates` on the end of it.

This document is about **content and settings**. Accounts and keys (Stripe,
Resend, the Sentry project, push keys) are in
[deploy/README.md](deploy/README.md); what the code still needs is in
[ARCHITECTURE.md](ARCHITECTURE.md).

**The state below was read out of the development database on 2026-08-10.** The
inn's live database is seeded from the same files and so starts in the same
place, with one deliberate exception: the invented menu used to exercise the
editor is not loaded onto the live server. See §4.

---

## The one that must be done before taking a booking

### 1. Real rates — `/admin/rates`

**This is the only item on this page that can charge somebody the wrong amount.**

Every other placeholder in this system renders as *nothing* — an empty
description prints no paragraph, an unwritten page prints no prose. Rates cannot
do that. A room with no price cannot be sold at all, so the seed ships one flat
season to make the site usable, and its numbers are **a guess**: the "starting
at" figures from the current site, halved, because those were quoted as two-night
totals.

What is in the database today:

| | |
|---|---|
| Seasons | **one**, named `Base rate (placeholder)` |
| Covering | 2026-01-01 to 2032-12-31 |
| Price | $200/night for Mrs. Beal's Suite, Garden Suite and Flume; $150/night for the other four |
| Minimum stay | not set on the season, so it falls back to the 2 nights in Settings |

**What to do.** On `/admin/rates`:

1. Create the seasons the inn actually prices by — a summer, a foliage season, a
   winter, holiday weekends. Each has a **Name**, a **First night**, a **Last
   night**, an optional **Minimum stay**, and a **Priority**.
2. Fill in the room × season grid with real nightly prices.
3. **Priority decides overlaps.** A holiday weekend sitting inside the summer
   season needs a *higher* priority number than the season underneath it, or the
   summer price wins on those nights.
4. **Last night is inclusive.** "Jun 1 to Aug 31" means the guest can sleep on
   August 31st. This is deliberately not the same convention as a check-out date.
5. Press **preview before saving.** The preview applies the change, works out
   exactly which nights and prices move, and then undoes it — so the number it
   shows is the real answer including any lower-priority season underneath,
   rather than an estimate.
6. Delete `Base rate (placeholder)` once the real seasons cover the calendar.
   Leave it until then: deleting it first takes every room off sale.

**Bookings already taken do not change.** A booking snapshots its own prices when
it is made, so re-pricing a season never alters a stay somebody has already paid
for.

---

## Content — nothing here shows a placeholder to a guest

Each of these renders as *nothing* until written, which is why none of them is
urgent and none of them is embarrassing. An unwritten restaurant page says the
menu is not up and to ring the inn. An unwritten room prints no paragraph.

### 2. Room descriptions — `/admin/rooms`

**Six of the seven rooms have no description.** Only Mrs. Beal's Suite has one,
because that is the only room the inn's current website describes. The rest were
left blank on purpose rather than filled in with invented sentences.

| Room | Description | Photographs |
|---|---|---|
| Mrs. Beal's Suite | ✅ written | 4 |
| Garden Suite | — | 5 |
| Blue Room | — | 3 |
| Rose Chamber | — | 3 |
| Washington Room | — | 3 |
| **Back Lavender** | — | **none** |
| **Flume** | — | **none** |

On `/admin/rooms`, open a room and fill in **Description** — a paragraph or two,
in the inn's own voice. **View**, **Sleeps**, **Amenities** and the pet fee are
on the same screen; amenities are already filled in from the current site and are
worth a read rather than a rewrite.

### 3. The two rooms with no photographs — `/admin/rooms` → a room → Photographs

Back Lavender and Flume currently show a grey placeholder graphic. Every other
room's pictures came off the inn's current website.

- Upload straight from a phone. Pictures are resized and re-encoded on the way
  in, so a full-size phone photograph is the right thing to send.
- **Alt text is required on every photograph** — one line describing what is in
  the picture, for somebody who cannot see it. The box stays amber until it has
  one and the save is refused without it.
- Drag to reorder. The first photograph is the one on the room's card in search
  results.

### 4. The menu — `/admin/menu`

**Empty**, and the restaurant page says so: that the menu is not up yet and to
ring the inn. Nothing invented is on the site.

Add a **Course** (Starters, Mains, Puddings), then dishes under it with a name, a
description of what is in it, and a price. The whole menu saves as one document,
so a half-finished edit never reaches the public page — the previous menu stays
until the save succeeds.

*A development machine loads `menu-mock.sql`, five invented dishes written to
exercise this editor. It is deliberately not part of what is loaded onto the
live server, so if a course called Starters appears here with food nobody
cooked, that file was run somewhere it should not have been — say so rather than
editing around it.*

### 5. Events — `/admin/events`

**Empty.** The events page currently shows only its inquiry form, which is
correct and looks deliberate. Add anything on: a **Title**, a **Date**, a
**Description**, and photographs.

### 6. Page prose — `/admin/pages`

One optional slot per page. Five of seven have words in them, transcribed from
the inn's current site:

| Page in the console | Written? |
|---|---|
| About us | ✅ |
| Events | ✅ |
| The local area | ✅ (and a heading) |
| Policies | ✅ |
| The restaurant | ✅ |
| The rooms page | ✅ |
| **Home (the backdrop photo)** | **no words — see below** |

- **"Home (the backdrop photo)"** is not a paragraph anywhere. The home page is
  one screenful of search over a photograph and never scrolls. What that screen
  edits is the **photographs behind the search** (they cross-fade if there is
  more than one) and the **sentence a search engine prints under the inn's name**
  in its results. That sentence is worth writing.
- **Plain text, not formatting.** A blank line starts a new paragraph. There is
  no bold and no headings on purpose.
- **Emptying both boxes removes the words entirely** rather than leaving an empty
  paragraph on the page.
- **Nearby highlights** is on the same screen: the local-area list, each with a
  name, a distance, an optional link and an optional sentence. **The sentences
  there were written by whoever built this, not by the inn** — the source site
  lists only names and distances — so they are the rows most worth the owner's
  eye. An entry with no sentence shows as a name and a distance.

### 7. The eight emails — `/admin/email`

**All eight are written and will send as they are.** They are a starting point
written to be edited, not finished copy — read them in the inn's voice and change
what does not sound like the inn.

| Message | When it goes |
|---|---|
| Booking confirmation | the moment a payment succeeds |
| Owner notification | the inn's own copy of the same booking |
| Balance warning | eight days before arrival: "we will charge $X tomorrow" |
| Balance receipt | seven days before arrival, when that charge succeeds |
| Balance failed | when that card is refused |
| Cancellation refund | when a stay is cancelled, refund or not |
| Checkout reminder | the morning a guest leaves |
| Payment request | when a phone booking is emailed a link to pay |

- **Press Preview before saving.** It renders the draft — the words currently in
  the box, not the saved ones — against a made-up booking called Sample Guest, so
  the layout and letterhead can be seen without a real guest's name on screen.
- **"What this message knows about the booking"** lists the fields that message
  can use — the guest's name, the dates, the total. A name not in that list
  prints nothing at all.
- **A save applies to the very next message sent**, not the next time the site is
  deployed.
- **Reset** puts the shipped words back.

### 8. Settings — `/admin/settings`

Already set, and worth confirming rather than changing:

| | Now |
|---|---|
| Tax rate | 8.5% (NH Meals & Rooms) |
| Check-in from | 3:00pm |
| Check-out by | 11:00am |
| Shortest stay | 2 nights |
| Longest stay | 31 nights |
| Hold | 15 minutes |
| Payment grace | 30 minutes |
| Refund retention | 3% |

Two of these are money and are explained where they are set: **refund retention**
is the card processor's cut, which is not returned when a payment is refunded, so
a refund keeps it rather than the inn paying it out of pocket. **Hold** is how
long a room is reserved for somebody partway through checkout.

The **accessibility notice** shown with every search is also here. It currently
says every room requires stairs and asks a guest with mobility needs to ring
before booking — that is a fact about the building and should not be softened
without the owner deciding to.

---

## Before the site goes public

- [ ] **Real rate seasons in, placeholder season deleted** (§1)
- [ ] **The real menu in, or left empty on purpose** (§4)
- [ ] Descriptions for the six rooms without one (§2)
- [ ] Photographs for Back Lavender and Flume (§3)
- [ ] A sentence for the home page's search-engine description (§6)
- [ ] A read of the eight emails (§7)
- [ ] Settings confirmed, especially the tax rate and the check-in/out times (§8)
- [ ] **A second phone enrolled** at `/admin/account`. One enrolled phone is a
      lockout waiting to happen — if it is lost, getting back in needs somebody
      with shell access to the server.

## Two things that are not the owner's to fix

- **Room amenities and the local-area entries** were transcribed from the inn's
  current site and are already in. Read them; they are unlikely to need work.
- **Anything that renders as nothing** — an unwritten page, a room without a
  description — is behaving correctly. It is not a bug and there is no rush.
