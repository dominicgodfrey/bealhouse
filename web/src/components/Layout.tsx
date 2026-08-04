import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

/**
 * The site shell.
 *
 * Deliberately plain. The marketing site is build-order step 7 and the owner's
 * photography and copy do not exist yet; this exists so every booking screen
 * sits in the same frame and real content has somewhere to land.
 */
export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-white text-neutral-900">
      <header className="border-b border-neutral-200">
        <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-4">
          <Link to="/" className="text-lg font-semibold tracking-tight">
            Beal House
          </Link>
          <nav className="text-sm text-neutral-600">
            <Link to="/" className="hover:text-neutral-900">
              Rooms
            </Link>
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">{children}</main>

      <footer className="border-t border-neutral-200">
        <div className="mx-auto max-w-5xl px-6 py-6 text-sm text-neutral-500">
          Littleton, New Hampshire
        </div>
      </footer>
    </div>
  )
}

/** A consistent place for "we could not load this", so no screen invents one. */
export function ErrorNote({ error }: { error: Error }) {
  return (
    <p className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
      {error.message}
    </p>
  )
}

export function Loading({ what }: { what: string }) {
  return <p className="py-12 text-center text-sm text-neutral-500">Loading {what}…</p>
}
