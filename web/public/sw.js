/*
 * The service worker, and it exists for exactly one reason: a notification has
 * to arrive when nobody is looking at the console.
 *
 * That is the whole difference between this and anything the page could do. A
 * push message is delivered to the browser, not to a tab — so this file runs
 * with no page open, wakes for a few hundred milliseconds, shows a notification
 * and stops. It deliberately does nothing else: no caching, no offline shell,
 * no interception of requests. A service worker that started caching the
 * console would be a second, stale copy of an application whose whole job is to
 * be current, and the failure would look like the inn's own data being wrong.
 */

// Take over as soon as installed rather than waiting for every console tab to
// close. An owner who has just switched notifications on expects the next
// booking to reach them, not the one after they next quit the browser.
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  // A push with no payload is legitimate — a service may wake a worker without
  // one — so this has to mean something rather than throw. It should not
  // happen: this server always sends a body.
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = {}
  }

  const title = data.title || 'The Beal House'
  const options = {
    body: data.body || '',
    // The reversed mark, which is the one icon in the bundle that reads at the
    // size Android draws this.
    icon: '/favicon.svg',
    badge: '/favicon.svg',
    // Collapses supersedes rather than stacking six identical taps for a phone
    // that was off. Distinct per booking, so two bookings stay two.
    tag: data.tag || undefined,
    data: { url: data.url || '/admin' },
  }

  // waitUntil, or the worker is killed before the notification is shown. This
  // is the whole contract of a push handler.
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()

  const target = (event.notification.data && event.notification.data.url) || '/admin'

  // Focus a console that is already open rather than piling up tabs, and only
  // open a new one if there is none. An owner tapping three notifications
  // should end with one console showing the last thing they tapped.
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((tabs) => {
      for (const tab of tabs) {
        if (tab.url.includes('/admin') && 'focus' in tab) {
          if ('navigate' in tab) tab.navigate(target).catch(() => {})
          return tab.focus()
        }
      }
      return self.clients.openWindow(target)
    }),
  )
})
