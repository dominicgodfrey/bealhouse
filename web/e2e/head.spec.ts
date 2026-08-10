import { expect, test } from '@playwright/test'

import { stayFor, staySearch } from './support/window'

/**
 * What a crawler gets, which is not what a visitor gets.
 *
 * The SPA is one document for every address, so the `<head>` is written by the
 * Go server per route (decision #3, internal/httpx/meta.go) before the document
 * is served. Go tests assert the templates; what they cannot assert is that the
 * document actually arriving at a browser carries them, that Vite's static
 * `<title>` was stripped rather than joined by a second one, and that the
 * structured data survives into a parsed DOM.
 *
 * These fetch the document rather than driving the page, because a crawler does
 * too. `page.goto` then reading the DOM would be reading what React did to it.
 */

test('every marketing page describes itself, and only itself', async ({ request }) => {
  const seen = new Map<string, string>()

  for (const path of ['/', '/rooms', '/restaurant', '/events', '/about', '/local-area']) {
    const html = await (await request.get(path)).text()

    const titles = [...html.matchAll(/<title>(.*?)<\/title>/gs)].map((m) => m[1])

    // Two titles is the failure that looks fine on screen: a browser takes the
    // first and a crawler may take either, so "Beal House" ends up on all seven
    // room results.
    expect(titles, `${path} should have exactly one <title>`).toHaveLength(1)
    expect(titles[0].trim()).not.toBe('')

    // A page sharing another's title is the state this replaced, and the whole
    // of the site's search presence.
    const already = seen.get(titles[0])
    expect(already, `${path} has the same title as ${already}`).toBeUndefined()
    seen.set(titles[0], path)

    const canonical = html.match(/<link rel="canonical" href="([^"]+)"/)?.[1]
    expect(canonical, `${path} has no canonical`).toBeTruthy()
    expect(canonical).toContain('127.0.0.1:8099')
  }
})

test('a room page carries its own structured offer', async ({ request }) => {
  const rooms = await (await request.get('/api/rooms')).json()
  expect(rooms.length).toBeGreaterThan(0)

  const room = rooms[0]
  const html = await (await request.get(`/rooms/${room.slug}`)).text()

  expect(html).toContain(`<title>`)
  expect(html).toContain(room.name)

  // The JSON-LD is the reason the head exists at all on this route: a room page
  // is where somebody arrives from a search engine rather than from the front
  // door.
  const blocks = [
    ...html.matchAll(/<script type="application\/ld\+json">(.*?)<\/script>/gs),
  ].map((m) => JSON.parse(m[1]))
  expect(blocks.length).toBeGreaterThan(0)

  const hotelRoom = blocks.find((b) => b['@type'] === 'HotelRoom')
  expect(hotelRoom, 'a room page should publish a HotelRoom').toBeTruthy()
  expect(hotelRoom.name).toBe(room.name)
})

/**
 * Decision #29 arriving politely. A GET under /book or /bookings ends in a
 * hold, so a crawler walking them takes real rooms off sale for the TTL — an
 * empty inn, quietly, with nothing in any log naming the cause.
 */
test('the booking flow is noindex, uncanonicalised and disallowed', async ({ request }) => {
  const stay = stayFor(2)

  for (const path of [`/book/blue-room?${staySearch(stay)}`, '/bookings/ABC123']) {
    const html = await (await request.get(path)).text()

    expect(html, `${path} is missing a robots directive`).toContain('noindex')
    expect(html, `${path} should carry no canonical`).not.toContain('rel="canonical"')
  }

  const robots = await (await request.get('/robots.txt')).text()
  expect(robots).toContain('Disallow: /book')
  expect(robots).toContain('Disallow: /bookings')

  // Answered by the SPA fallback these would be a page of HTML with a 200, and
  // a crawler parsing that as a rule set does something nobody predicted. Same
  // reason /media/* sits on the root router.
  expect((await request.get('/robots.txt')).headers()['content-type']).toContain('text/plain')
})

test('the sitemap is absolute and lists the rooms', async ({ request }) => {
  const res = await request.get('/sitemap.xml')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('xml')

  const xml = await res.text()
  const locs = [...xml.matchAll(/<loc>(.*?)<\/loc>/g)].map((m) => m[1])
  expect(locs.length).toBeGreaterThan(0)

  // A <loc> is defined as absolute, and a file of relative ones is rejected
  // whole rather than partially.
  for (const loc of locs) {
    expect(loc).toMatch(/^https?:\/\//)
  }

  const rooms = await (await request.get('/api/rooms')).json()
  for (const room of rooms) {
    expect(locs.some((l) => l.endsWith(`/rooms/${room.slug}`))).toBeTruthy()
  }

  // The booking flow is Disallow'ed above; it must not be advertised here
  // either.
  expect(locs.some((l) => l.includes('/book'))).toBeFalsy()
})

/**
 * A missing photograph must 404 rather than being answered by the SPA fallback,
 * which would hand an <img> a page of HTML with a 200 — a broken picture with
 * nothing in any log to say why.
 */
test('a missing photograph is a 404, not the SPA', async ({ request }) => {
  const res = await request.get('/media/nothing-is-stored-under-this-name.jpg')
  expect(res.status()).toBe(404)
  expect(await res.text()).not.toContain('<html')
})
