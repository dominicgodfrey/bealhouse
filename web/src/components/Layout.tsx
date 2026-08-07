import type { ReactNode } from 'react'
import { Link } from 'react-router'

/**
 * The site shell.
 *
 * Still deliberately plain: the owner's photography has not arrived, and the
 * words on every page come out of the console rather than out of this
 * repository. What this now carries is the navigation — the four things the
 * site is for besides booking a room — so a visitor who lands on a room page
 * from a search engine can find the restaurant.
 */
export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-white text-neutral-900">
      <header className="border-b border-neutral-200">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-4 px-6 py-4">
          {/*
            The mark carries no words, so the name stays beside it and the image
            takes an empty alt rather than repeating what the link already says.
          */}
          <Link
            to="/"
            className="flex items-center gap-2.5 text-lg font-semibold tracking-tight"
          >
            <img src="/logo.svg" alt="" className="h-6 w-auto" />
            Beal House
          </Link>

          <nav className="flex flex-wrap gap-x-5 gap-y-1 text-sm text-neutral-600">
            <Link to="/rooms" className="hover:text-neutral-900">
              Rooms
            </Link>
            <Link to="/restaurant" className="hover:text-neutral-900">
              Restaurant
            </Link>
            <Link to="/events" className="hover:text-neutral-900">
              Events
            </Link>
            <Link to="/about" className="hover:text-neutral-900">
              About
            </Link>
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">{children}</main>

      <footer className="border-t border-neutral-200">
        <div className="mx-auto flex max-w-5xl flex-wrap justify-between gap-4 px-6 py-6 text-sm text-neutral-500">
          <span>Beal House · Littleton, New Hampshire</span>
          <span>Book direct. No booking fees.</span>
        </div>
      </footer>
    </div>
  )
}

/**
 * The owner's prose for a page, rendered where it belongs.
 *
 * Nothing at all when nothing has been written — not a placeholder, not an
 * empty heading. A page with no copy shows its structure and its live data
 * (the rooms, the menu, the dates you can book) and simply does not have a
 * paragraph, which is honest and looks deliberate. A placeholder sentence about
 * the inn would be on the public internet until somebody remembered it.
 */
export function Prose({
  heading,
  paragraphs,
}: {
  heading?: string
  paragraphs?: string[]
}) {
  if (!heading && !paragraphs?.length) return null

  return (
    <div className="flex flex-col gap-3">
      {heading && <h2 className="text-2xl font-semibold tracking-tight">{heading}</h2>}
      {paragraphs?.map((paragraph, i) => (
        <p key={i} className="max-w-prose text-neutral-700">
          {paragraph}
        </p>
      ))}
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
