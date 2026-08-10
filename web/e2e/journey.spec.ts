import { expect, test } from '@playwright/test'

import { stayFor, staySearch } from './support/window'

/**
 * The guest journey, in a real browser: search → results → room → confirm →
 * hold → pay → confirmed.
 *
 * Everything below the browser already has tests against a real Postgres, and
 * this deliberately does not repeat them. What has never been exercised is
 * whether the five screens actually join up — whether the stay survives the URL
 * from one to the next, whether the quote a guest agrees to is the one the hold
 * is written with, and whether the return page notices the webhook landing.
 * Those are joins between the SPA and the API, and no Go test can see them.
 *
 * The payment is the stand-in button (STRIPE_FAKE), so no card and no account
 * are involved. Everything past it is real: the server signs a delivery and
 * sends it through its own webhook handler, signature verification and state
 * machine.
 */

test('a guest searches, holds a room, pays, and the booking is confirmed', async ({ page }) => {
  const stay = stayFor(0)

  // Straight to the results with the dates in the URL. The picker has its own
  // test below; driving it here would make every assertion after it depend on a
  // calendar widget, and this test is about the journey.
  await page.goto(`/search?${staySearch(stay)}`)

  await expect(page.getByRole('heading', { name: /rooms? available/ })).toBeVisible()

  // The first room offered, whichever it is. Naming one would make this fail
  // the day the owner takes a room off sale, which is a thing they are allowed
  // to do.
  const firstRoom = page.locator('a[href^="/rooms/"]').first()
  const roomName = (await firstRoom.textContent())?.trim() ?? ''
  expect(roomName).not.toBe('')
  await firstRoom.click()

  await expect(page.getByRole('heading', { level: 1, name: roomName })).toBeVisible()

  // The price the room page shows is what the guest is agreeing to, so it is
  // carried forward and checked at every screen it appears on. A total that
  // changes between the room page and the hold is the failure this journey
  // exists to catch, and it would be invisible to any test that looked at one
  // screen.
  const roomPageTotal = await totalOnPage(page)

  await page.getByRole('link', { name: 'Book this room' }).click()

  await expect(page.getByRole('heading', { name: 'Confirm your stay' })).toBeVisible()
  expect(await totalOnPage(page)).toBe(roomPageTotal)

  await page.getByLabel('Name').fill('E2E Guest')
  await page.getByLabel('Email').fill('e2e@example.test')
  await page.getByLabel('Phone').fill('603 555 0100')

  // The room cannot be held until the policies are accepted. Asserted rather
  // than merely ticked, because "the button was disabled" is the visible half
  // of a rule the server enforces too, and a UI that quietly stopped disabling
  // it would leave guests submitting forms that fail at the end.
  const hold = page.getByRole('button', { name: 'Hold this room' })
  await expect(hold).toBeDisabled()

  await page.getByRole('checkbox').check()
  await expect(hold).toBeEnabled()
  await hold.click()

  await expect(page.getByRole('heading', { name: 'Your room is held' })).toBeVisible()
  expect(await totalOnPage(page)).toBe(roomPageTotal)

  // The reference a guest reads out over the telephone: BH- and six characters
  // over an alphabet with no I, O, 0 or 1 in it, which are the four that go
  // wrong when somebody is writing it down (internal/booking/code.go).
  await expect(page).toHaveURL(/\/bookings\/BH-[2-9A-HJ-NP-Z]{6}$/)
  const code = page.url().split('/').pop()!

  // The hold is a room_occupancy row with an expiry, and the page says how long
  // is left on it. Not a decoration: it is the only thing telling a guest the
  // room is not theirs indefinitely.
  await expect(page.getByText(/Held for/)).toBeVisible()

  await page.getByRole('link', { name: 'Pay and confirm' }).click()
  await expect(page).toHaveURL(new RegExp(`/bookings/${code}/pay$`))

  await page.getByRole('button', { name: 'Pretend the card was accepted' }).click()

  // The redirect back does not confirm anything — the webhook does — so the
  // page polls. This is the gap where the money has moved and the booking has
  // not caught up, and the whole reason that polling exists.
  await expect(page.getByText('Your stay is confirmed.')).toBeVisible({ timeout: 35_000 })
  await expect(page.getByRole('heading', { name: 'Your booking' })).toBeVisible()

  // And it is still the same money at the end of it.
  expect(await totalOnPage(page)).toBe(roomPageTotal)
})

test('a room already held is refused rather than double-booked', async ({ page, request }) => {
  const stay = stayFor(1)

  await page.goto(`/search?${staySearch(stay)}`)
  await expect(page.getByRole('heading', { name: /rooms? available/ })).toBeVisible()

  const firstRoom = page.getByRole('heading', { level: 2 }).getByRole('link').first()
  const href = await firstRoom.getAttribute('href')
  const slug = href!.split('/')[2].split('?')[0]

  // Take the room out from under the browser through the API, which is what a
  // second guest a moment earlier looks like from here.
  const held = await request.post('/api/bookings', {
    data: {
      roomSlug: slug,
      checkin: stay.checkin,
      checkout: stay.checkout,
      guests: 2,
      withPet: false,
      guest: { name: 'E2E Rival', email: 'rival@example.test', phone: '603 555 0101' },
      acceptedPolicies: true,
    },
  })
  expect(held.ok()).toBeTruthy()

  // The guest is told, on the screen they were about to book from, rather than
  // being walked to a form that fails at the end of it.
  await page.goto(`/book/${slug}?${staySearch(stay)}`)
  await expect(page.getByRole('heading', { name: 'That room is no longer available' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'See what else is free' })).toBeVisible()
})

test('search with no dates asks for them instead of showing an empty list', async ({ page }) => {
  await page.goto('/search')

  await expect(page.getByText('Choose your dates to see what is available.')).toBeVisible()
  await expect(page.getByRole('heading', { name: /rooms? available/ })).toHaveCount(0)
})

/**
 * The all-in total, as the guest reads it.
 *
 * By its rendered text and not by a test id, because the assertion worth making
 * is that the number a person sees is the same on every screen — something that
 * read the value out of a prop could agree perfectly while the page showed
 * otherwise.
 *
 * The row beside the word "Total" specifically, and not the last money on the
 * page: the held screen puts "Due at booking" underneath it, which is the
 * deposit and is a different and smaller number.
 */
async function totalOnPage(page: import('@playwright/test').Page): Promise<string> {
  const total = page
    .getByText('Total', { exact: true })
    .locator('xpath=following-sibling::span')
    .first()
  await expect(total).toBeVisible()
  return (await total.textContent())!.trim()
}
