import { useState, type ReactNode } from 'react'

import { ApiError } from '../../lib/api'
import { formatCents } from '../../lib/money'
import type { BookingStatus, Stay } from '../../lib/console'

/**
 * The console's shared furniture.
 *
 * Twelve screens, and without this each of them invents its own card border and
 * its own idea of what a disabled button looks like. More importantly, each of
 * them would invent its own answer to a 401 — and there is exactly one right
 * answer, which is not an error message.
 */

export function Screen({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {subtitle && <p className="text-sm text-neutral-600">{subtitle}</p>}
        </div>
        {actions}
      </header>
      {children}
    </div>
  )
}

export function Section({
  title,
  note,
  children,
}: {
  title: string
  note?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-sm font-medium tracking-wide text-neutral-500 uppercase">{title}</h2>
        {note && <p className="text-sm text-neutral-600">{note}</p>}
      </div>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  )
}

export function Card({ children, tone }: { children: ReactNode; tone?: 'plain' | 'alarm' }) {
  const border = tone === 'alarm' ? 'border-red-300 bg-red-50' : 'border-neutral-200 bg-white'
  return <div className={`flex flex-col gap-3 rounded-lg border p-4 ${border}`}>{children}</div>
}

/** Nothing here yet, said in a sentence rather than left as blank space. */
export function Empty({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-lg border border-dashed border-neutral-300 px-4 py-6 text-center text-sm text-neutral-500">
      {children}
    </p>
  )
}

export function Button({
  children,
  onClick,
  disabled,
  kind = 'plain',
  type = 'button',
}: {
  children: ReactNode
  onClick?: () => void
  disabled?: boolean
  kind?: 'plain' | 'primary' | 'danger'
  type?: 'button' | 'submit'
}) {
  const styles = {
    plain: 'border border-neutral-300 bg-white',
    primary: 'bg-neutral-900 text-white',
    danger: 'bg-red-700 text-white',
  }[kind]

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      // py-3 rather than py-2 throughout: this is used one-handed on a phone and
      // a 44px target is the difference between tapping and aiming.
      className={`rounded-lg px-4 py-3 text-sm font-medium disabled:opacity-60 ${styles}`}
    >
      {children}
    </button>
  )
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-neutral-500">{hint}</span>}
    </label>
  )
}

export const inputClass = 'rounded-lg border border-neutral-300 px-3 py-3 text-sm'

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`${inputClass} ${props.className ?? ''}`} />
}

export function Textarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`${inputClass} ${props.className ?? ''}`} />
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`${inputClass} bg-white ${props.className ?? ''}`} />
}

/** Integer cents in, a string out, at the last possible moment. */
export function Money({ cents }: { cents: number }) {
  return <>{formatCents(cents)}</>
}

/**
 * Cents as an editable dollars field.
 *
 * The conversion happens here and nowhere else, so no screen is tempted to keep
 * a price as a float between the box and the wire. An empty box is zero, which
 * is the same thing every price in this schema treats as "no price".
 */
export function centsToInput(cents: number): string {
  return cents === 0 ? '' : (cents / 100).toFixed(2)
}

export function inputToCents(value: string): number {
  const parsed = Number.parseFloat(value.replace(/[^0-9.]/g, ''))
  return Number.isFinite(parsed) ? Math.round(parsed * 100) : 0
}

const statusStyles: Record<BookingStatus, string> = {
  confirmed: 'bg-emerald-100 text-emerald-900',
  pending: 'bg-amber-100 text-amber-900',
  cancelled: 'bg-neutral-200 text-neutral-700',
  expired: 'bg-neutral-200 text-neutral-700',
}

export function StatusPill({ status }: { status: BookingStatus }) {
  return (
    <span className={`rounded px-2 py-0.5 text-xs font-medium ${statusStyles[status]}`}>
      {status}
    </span>
  )
}

/**
 * The paid-against-total line every booking list carries.
 *
 * A refused charge is red and says so in words, not only in colour: an owner
 * scanning this on a phone in daylight is the person who has to notice it, and
 * ARCHITECTURE calls for it to be unmissable.
 */
export function MoneyLine({ stay }: { stay: Stay }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
      <span className="text-neutral-600">
        <Money cents={stay.paidCents} /> of <Money cents={stay.totalCents} />
      </span>
      {stay.outstandingCents > 0 && stay.status === 'confirmed' && (
        <span className="text-neutral-500">
          · <Money cents={stay.outstandingCents} /> outstanding
        </span>
      )}
      {stay.chargeFailed && (
        <span className="rounded bg-red-700 px-2 py-0.5 text-xs font-medium text-white">
          card refused
        </span>
      )}
    </div>
  )
}

/**
 * A 401 is the session having gone, not something to put in a red box.
 *
 * Every action in the console routes its failures through here so the gate
 * closes and asks for the passkey again — one answer, in one place, rather than
 * each of forty buttons deciding for itself.
 */
export function report(err: unknown, onSignedOut: () => void, setError: (e: Error | null) => void) {
  if (err instanceof ApiError && err.status === 401) {
    onSignedOut()
    return
  }
  setError(err instanceof Error ? err : new Error('Something went wrong.'))
}

/**
 * A save button that reports what happened next to itself.
 *
 * Used by every editor here. `save` throws to report a failure, which is what
 * the API client already does, so a screen never has to invent a result type
 * for "it worked".
 */
export function useSaving(onSignedOut: () => void) {
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [saved, setSaved] = useState(false)

  async function run(save: () => Promise<unknown>) {
    setWorking(true)
    setError(null)
    setSaved(false)
    try {
      await save()
      setSaved(true)
    } catch (err) {
      report(err, onSignedOut, setError)
    } finally {
      setWorking(false)
    }
  }

  return { working, error, saved, run, clear: () => setSaved(false) }
}

/** Bumps a counter, which is how every screen here re-runs its loader. */
export function useReload(): [number, () => void] {
  const [nonce, setNonce] = useState(0)
  return [nonce, () => setNonce((n) => n + 1)]
}

export function Saved({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-900">
      {children}
    </p>
  )
}

/**
 * A note about how something works, for the places where the honest answer is a
 * sentence rather than a different button — the half-open block dates, the
 * manual booking that sends no email, the confirmed stays a rate change cannot
 * touch.
 */
export function Aside({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-lg bg-neutral-100 px-4 py-3 text-sm text-neutral-700">{children}</p>
  )
}
