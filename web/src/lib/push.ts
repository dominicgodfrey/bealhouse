/**
 * Turning notifications on for this browser.
 *
 * Three parties have to agree before one arrives: the browser (permission), the
 * push service (a subscription), and this server (the row it delivers to). This
 * file is the order those happen in and the places it can fail, and it is
 * deliberately explicit about which one refused — "notifications are off" with
 * no reason is the message that makes an owner give up on the feature.
 */

import { request } from './api'

export type PushSettings = {
  /** The VAPID key a browser subscribes against. Empty means none configured. */
  publicKey: string
  configured: boolean
  /** How many browsers would hear about the next booking. */
  subscribers: number
}

export function fetchPushSettings(): Promise<PushSettings> {
  return request<PushSettings>('/api/admin/push')
}

/** Whether this browser could do it at all, before anything is asked of it. */
export function pushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

/**
 * The base64url VAPID key as the browser wants it.
 *
 * PushManager takes raw bytes, and the key travels as base64url — the same
 * encoding mismatch that made passkey ids unusable, so it is done once here
 * rather than at the call site.
 */
function applicationServerKey(base64url: string): Uint8Array<ArrayBuffer> {
  const padded = base64url.padEnd(base64url.length + ((4 - (base64url.length % 4)) % 4), '=')
  const binary = atob(padded.replace(/-/g, '+').replace(/_/g, '/'))

  // Backed by a plain ArrayBuffer rather than Uint8Array.from, whose result is
  // typed over ArrayBufferLike and so could in principle be shared memory —
  // which PushManager will not take.
  const view = new Uint8Array(new ArrayBuffer(binary.length))
  for (let i = 0; i < binary.length; i++) view[i] = binary.charCodeAt(i)
  return view
}

/**
 * Ask this browser to receive notifications.
 *
 * Must be called from inside a click. Browsers refuse a permission prompt
 * without a user gesture, exactly as they refuse a passkey ceremony — the same
 * rule the enrollment page is written around.
 */
export async function enablePush(publicKey: string, label: string): Promise<void> {
  if (!pushSupported()) {
    throw new Error('This browser cannot receive notifications.')
  }
  if (!publicKey) {
    throw new Error('This server has no notification keys configured, so there is nothing to subscribe to.')
  }

  const permission = await Notification.requestPermission()
  if (permission !== 'granted') {
    // Worth saying plainly: once denied, the browser will not ask again, and
    // the owner has to change it in site settings. A retry button that silently
    // does nothing is the alternative.
    throw new Error(
      permission === 'denied'
        ? 'This browser has blocked notifications. Turn them back on in the site settings for this page, then try again.'
        : 'Notifications were not allowed.',
    )
  }

  const registration = await navigator.serviceWorker.register('/sw.js')
  await navigator.serviceWorker.ready

  // An existing subscription is reused rather than replaced. Re-subscribing
  // with a different key silently produces one the server cannot reach, and the
  // symptom is notifications that stop with nothing in any log.
  const existing = await registration.pushManager.getSubscription()
  const subscription =
    existing ??
    (await registration.pushManager.subscribe({
      // Required to be true by every browser that implements this: a push may
      // not be received without something being shown to the user.
      userVisibleOnly: true,
      applicationServerKey: applicationServerKey(publicKey),
    }))

  // toJSON() gives exactly the shape the server stores — endpoint plus the two
  // keys — so nothing here picks the subscription apart into a payload of our
  // own invention.
  const payload = subscription.toJSON()

  await request<void>('/api/admin/push/subscribe', {
    method: 'POST',
    body: JSON.stringify({ ...payload, label }),
  })
}

/**
 * Stop notifications reaching this browser.
 *
 * Both halves, in this order: tell the server first, then unsubscribe locally.
 * The reverse loses the endpoint before it can be forgotten server-side, which
 * leaves a row that delivers to a subscription the browser has already thrown
 * away — invisible, and only cleared when the push service eventually reports
 * it gone.
 */
export async function disablePush(): Promise<void> {
  const registration = await navigator.serviceWorker.getRegistration()
  const subscription = await registration?.pushManager.getSubscription()

  if (subscription) {
    await request<void>('/api/admin/push/unsubscribe', {
      method: 'POST',
      body: JSON.stringify({ endpoint: subscription.endpoint }),
    })
    await subscription.unsubscribe()
  }
}

/** Whether this particular browser already has a subscription. */
export async function pushEnabledHere(): Promise<boolean> {
  if (!pushSupported()) return false
  const registration = await navigator.serviceWorker.getRegistration()
  const subscription = await registration?.pushManager.getSubscription()
  return !!subscription
}
