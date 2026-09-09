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

### 1. Rates — `/admin/rates`

**This is the only item on this page that can charge somebody the wrong amount**,
which is why it is the one that ships already filled in with the inn's own
numbers rather than a placeholder. The prices below are the ones the owner
confirmed on 2026-09-07; what is left is to read them once on the screen that
will change them from now on.

What is in the database today, per night and before the 8.5% tax:

| | Mrs. Beal's · Garden · Flume | Rose · Blue · Washington · Back Lavender |
|---|---|---|
| **Standard** — every night | $200 | $150 |
| **Fall** — August 14th to October 31st | $250 | $200 |

The inn prices by five named seasons — Winter, Low Spring, Summer, Fall, Low
Fall — and four of them carry the same price for every room. Only Fall differs,
so that is how it is entered: a Standard row covering every night, and a Fall
row laid over it each year at a higher priority. Two rows a year cannot leave a
gap, where the five as written did (Fall ended on the 30th and Low Fall began on
the 1st, and a night no season covers is a night no room can be sold on).

There is no fee of any kind beyond the tax, and the minimum stay is two nights
in every season with no holiday exceptions — so **Minimum stay** is blank on
every row and the 2 in Settings applies.

**What to do.** On `/admin/rates`:

1. Read the grid against the table above. Search a Fall date and a Standard
   date on the public site and see the two prices you expect.
2. **Fall is seeded through 2030.** Before the autumn of 2029, add `Fall 2031`
   — first night August 14th, last night October 31st, priority 1, the same
   four prices — or that autumn sells at the Standard rate.
3. To change a price, change it here; the calendar regenerates for future
   nights only. **Priority decides overlaps** — a row laid over Standard needs a
   higher number or Standard wins — and **last night is inclusive**: "Aug 14 to
   Oct 31" means the guest can sleep on the 31st, which is deliberately not the
   same convention as a check-out date.
4. Press **preview before saving.** The preview applies the change, works out
   exactly which nights and prices move, and then undoes it — so the number it
   shows is the real answer including any lower-priority season underneath,
   rather than an estimate.

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
because that was the only room the inn's current website described when the
copy was transcribed. The rest were left blank on purpose rather than filled in
with invented sentences. *Since then the current site has gained a Flume Room
page with a paragraph on it, in the inn's own words — worth pasting in here.*

| Room | Description | Photographs |
|---|---|---|
| Mrs. Beal's Suite | ✅ written | 4 |
| Garden Suite | — | 5 |
| Blue Room | — | 3 |
| Rose Chamber | — | 3 |
| Washington Room | — | 4 |
| **Back Lavender** | — | **none** |
| **Flume** | — | **none** |

On `/admin/rooms`, open a room and fill in **Description** — a paragraph or two,
in the inn's own voice. **View**, **Sleeps** and **Amenities** are on the same
screen; amenities are already filled in from the current site and are
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

- [ ] **The rate grid read once on `/admin/rates` and checked on the public site** (§1)
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
