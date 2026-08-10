import { expect, test } from '@playwright/test'

/**
 * The console's front door.
 *
 * Not the screens behind it — those need an enrolled passkey, which is a real
 * handset and the one piece of manual verification ARCHITECTURE.md still lists.
 * What is worth a browser here is the gate itself: that /admin is a sign-in and
 * not the SPA's index page, that a forged front end reaches nothing, and that
 * the console is `noindex`.
 */

test('the console asks for a passkey rather than showing a screen', async ({ page }) => {
  await page.goto('/admin/bookings')

  await expect(page.getByRole('heading', { name: 'The Beal House console' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Sign in with your passkey/ })).toBeVisible()

  // The URL survives, so signing in lands where the owner was going and a
  // session expiring under an open console is a prompt rather than a page that
  // navigated out from under them.
  await expect(page).toHaveURL(/\/admin\/bookings$/)
})

test('nothing behind the gate answers an unauthenticated request', async ({ request }) => {
  for (const path of [
    '/api/admin/today',
    '/api/admin/bookings',
    '/api/admin/settings',
    '/api/admin/passkeys',
  ]) {
    const res = await request.get(path)
    expect(res.status(), `${path} should be closed`).toBe(401)

    // Not the SPA's index.html with a 200, which is what an unrouted GET gets.
    expect(res.headers()['content-type']).toContain('json')
  }
})

test('the console is not crawlable', async ({ request }) => {
  const html = await (await request.get('/admin')).text()
  expect(html).toContain('noindex')

  const robots = await (await request.get('/robots.txt')).text()
  expect(robots).toContain('Disallow: /admin')
})

/**
 * The SPA fallback answers GET and HEAD only. A POST to an unrouted path is
 * somebody expecting an endpoint, and answering it with index.html and a 200 is
 * worse than answering nothing — a webhook sender would read it as success.
 */
test('a POST to an unrouted path is not answered with the SPA', async ({ request }) => {
  const res = await request.post('/not-an-endpoint', { data: {} })
  expect(res.status()).not.toBe(200)
  expect(await res.text()).not.toContain('<html')
})
