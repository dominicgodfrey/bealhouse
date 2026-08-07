/**
 * The console's API (decision #15), kept apart from the guest-facing shapes in
 * api.ts because it is a different surface with a different gate: everything
 * here rides on the session cookie, and a 401 is not an error to display but a
 * sign-in to ask for.
 *
 * Hand-written for the same reason as api.ts, and the same one file to replace
 * when the OpenAPI generator in decision #3 exists.
 */

import { request } from './api'

export type Identity = { name: string }

export type Passkey = {
  /** base64url, unpadded — the same string DELETE /passkeys/{id} takes back. */
  id: string
  label: string
  createdAt: string
  lastUsedAt?: string
}

export type Device = {
  /** Absent when the passkey behind this session has since been revoked. */
  passkeyId?: string
  label: string
  userAgent: string
  signedInAt: string
  lastSeenAt: string
  /** The phone asking. Without it the owner cannot tell two rows apart. */
  current: boolean
}

export type Invitation = {
  /** Carries its token in the fragment, so it is never logged by anything. */
  url: string
  label: string
  expiresAt: string
}

/** Who is signed in, or a 401 that says nobody is. */
export function fetchIdentity(): Promise<Identity> {
  return request<Identity>('/api/admin/me')
}

/**
 * The two ceremonies, each a begin and a finish.
 *
 * Both begins set a short-lived cookie naming the challenge, so the finish has
 * nothing to carry but the phone's answer — which is why neither of these
 * threads an id through the browser.
 */
export function beginSignIn(): Promise<{ publicKey: unknown }> {
  return request<{ publicKey: unknown }>('/api/admin/auth/login/begin', { method: 'POST' })
}

export function finishSignIn(credential: unknown): Promise<Identity> {
  return request<Identity>('/api/admin/auth/login/finish', {
    method: 'POST',
    body: JSON.stringify(credential),
  })
}

export function beginEnrollment(token: string): Promise<{ publicKey: unknown }> {
  return request<{ publicKey: unknown }>('/api/admin/auth/enroll/begin', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
}

export function finishEnrollment(credential: unknown): Promise<Identity> {
  return request<Identity>('/api/admin/auth/enroll/finish', {
    method: 'POST',
    body: JSON.stringify(credential),
  })
}

export function signOut(): Promise<void> {
  return request<void>('/api/admin/auth/logout', { method: 'POST' })
}

export function fetchPasskeys(): Promise<Passkey[]> {
  return request<Passkey[]>('/api/admin/passkeys')
}

export function fetchDevices(): Promise<Device[]> {
  return request<Device[]>('/api/admin/devices')
}

export function mintInvitation(label: string): Promise<Invitation> {
  return request<Invitation>('/api/admin/passkeys/invite', {
    method: 'POST',
    body: JSON.stringify({ label }),
  })
}

export function revokePasskey(id: string): Promise<void> {
  return request<void>(`/api/admin/passkeys/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function signOutEverywhere(): Promise<void> {
  return request<void>('/api/admin/sessions/revoke-all', { method: 'POST' })
}

/**
 * Timestamps here are instants, not civil dates, so they are formatted with a
 * Date rather than through lib/dates.ts — which exists precisely to keep Dates
 * away from check-in and check-out. "When did this phone last sign in" is a
 * moment in time and reads correctly in the inn's own timezone.
 */
const instant = new Intl.DateTimeFormat('en-US', {
  timeZone: 'America/New_York',
  dateStyle: 'medium',
  timeStyle: 'short',
})

export function formatInstant(iso: string): string {
  return instant.format(new Date(iso))
}
