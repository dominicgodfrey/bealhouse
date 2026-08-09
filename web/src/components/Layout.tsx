import type { ReactElement, ReactNode } from 'react'
import { Link } from 'react-router'

import { inn, innAddressLine } from '../lib/contact'

/**
 * The site shell.
 *
 * Still deliberately plain: the words on every page come out of the console
 * rather than out of this repository. What this carries is the navigation — the
 * things the site is for besides booking a room — so a visitor who lands on a
 * room page from a search engine can find the restaurant.
 */
export function Layout({
  children,
  fills = false,
  backdrop,
}: {
  children: ReactNode
  /**
   * Locks the page to the viewport: nothing scrolls. The home page and only the
   * home page — every other page is a document of unknown length, and `h-dvh`
   * on one of those hides whatever did not fit.
   */
  fills?: boolean
  /** Rendered edge to edge behind <main>. Only meaningful with `fills`. */
  backdrop?: ReactNode
}) {
  return (
    <div
      className={`flex flex-col bg-white text-neutral-900 ${
        fills ? 'h-dvh overflow-hidden' : 'min-h-dvh'
      }`}
    >
      {/* shrink-0 on both bars: in a fixed-height column it is main that has
          room to give, not a header whose nav has wrapped. */}
      <header className="shrink-0 border-b border-neutral-200">
        {/*
          gap-x-6 with a smaller gap-y: when the nav wraps under the wordmark on
          a narrow screen, a uniform gap-4 leaves it floating in the middle of
          nowhere. px-4 on a phone, px-6 from sm — six is a lot of a 375px
          screen to spend on margin.
        */}
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-4 sm:px-6">
          {/*
            The mark carries no words, so the name stays beside it and the image
            takes an empty alt rather than repeating what the link already says.
          */}
          <Link
            to="/"
            className="flex items-center gap-2.5 text-lg font-semibold tracking-tight"
          >
            <img src="/logo.svg" alt="" className="h-6 w-auto" />
            {inn.name}
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
            <Link to="/local-area" className="hover:text-neutral-900">
              Local area
            </Link>
            <Link to="/about" className="hover:text-neutral-900">
              About
            </Link>
          </nav>
        </div>
      </header>

      <main
        className={
          fills
            ? // isolate, or the backdrop's -z-10 stacks against the page and
              // disappears behind the white body.
              'relative isolate min-h-0 flex-1'
            : 'mx-auto w-full max-w-5xl flex-1 px-4 py-8 sm:px-6'
        }
      >
        {backdrop && <div className="absolute inset-0 -z-10 overflow-hidden">{backdrop}</div>}
        {children}
      </main>

      {/* The address and telephone are on every page: somebody looking up how
          to reach the inn is on whichever page they happen to be on. */}
      <footer className="shrink-0 border-t border-neutral-200 bg-white">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-x-6 gap-y-1 px-4 py-3 text-xs text-neutral-500 sm:px-6 sm:py-4 sm:text-sm">
          <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <span className="font-medium text-neutral-700">{inn.name}</span>
            <span aria-hidden="true">·</span>
            {/* Wraps as one thing, so a narrow screen breaks before the
                street rather than inside it. */}
            <span className="whitespace-nowrap">{innAddressLine}</span>
          </p>

          <nav className="flex flex-wrap items-center gap-x-4 gap-y-0.5">
            {/* On the device most of this site is read on, the number is the
                button. */}
            <a href={inn.phoneHref} className="whitespace-nowrap hover:text-neutral-900">
              {inn.phone}
            </a>
            <Link to="/about" className="hover:text-neutral-900">
              Contact us
            </Link>
            {/*
              The policies are linked from every page, not only from the box a
              guest ticks on the way to paying. Terms you can only reach at the
              moment you are asked to accept them are terms nobody can check
              afterwards.
            */}
            <Link to="/policies" className="hover:text-neutral-900">
              Policies
            </Link>
          </nav>
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
  align = 'center',
}: {
  heading?: string
  paragraphs?: string[]
  /**
   * Centred by default, which is what the marketing pages want. The policies
   * page asks for 'left': it is a document to be read down rather than a page
   * to be looked at, and its own sections are left-aligned, so a centred
   * preamble above them read as a different page.
   */
  align?: 'center' | 'left'
}) {
  if (!heading && !paragraphs?.length) return null

  // The column stays max-w-prose either way — centring the block is what
  // balances the page, and letting the line length grow with the viewport is
  // what would make it hard to read.
  return (
    <div
      className={`flex flex-col gap-3 ${
        align === 'center' ? 'items-center text-center' : 'mx-auto w-full max-w-prose text-left'
      }`}
    >
      {heading && <h2 className="text-2xl font-semibold tracking-tight">{heading}</h2>}
      {paragraphs?.map((paragraph, i) => (
        // whitespace-pre-line, so a single newline inside a paragraph is a line
        // break and a blank line is still a new paragraph. That is what lets the
        // owner type a list — the local area's eleven attractions and their
        // distances — without a markdown parser in this bundle.
        <p key={i} className="max-w-prose whitespace-pre-line text-neutral-700">
          {linkify(paragraph)}
        </p>
      ))}
    </div>
  )
}

/**
 * Turns bare URLs in the owner's prose into links, and nothing else.
 *
 * This is deliberately not the thin end of a markdown parser. It returns React
 * nodes — never HTML, never `dangerouslySetInnerHTML` — so every other
 * character in the copy is still text that React escapes, and the console
 * remains unable to put markup of its choosing on the public site. The only
 * element it can produce is an `<a>` whose href is built from the matched text
 * itself.
 *
 * The pattern matches `https://…`, `http://…` and `www.…` only, which is also
 * what excludes `javascript:` — the one scheme that would make a link an attack
 * rather than a link.
 */
const urlPattern = /\b(?:https?:\/\/|www\.)[^\s<>()]+[^\s<>().,;:!?]/gi

function linkify(text: string) {
  const out: (string | ReactElement)[] = []
  let last = 0

  for (const match of text.matchAll(urlPattern)) {
    const start = match.index
    if (start > last) out.push(text.slice(last, start))

    const shown = match[0]
    const href = shown.startsWith('www.') ? `https://${shown}` : shown
    out.push(
      <a
        key={start}
        href={href}
        target="_blank"
        // noreferrer as well as noopener: the target page has no business
        // knowing which page of the inn's site somebody left from.
        rel="noopener noreferrer"
        className="underline underline-offset-4 hover:text-neutral-900"
      >
        {shown}
      </a>,
    )
    last = start + shown.length
  }

  if (last < text.length) out.push(text.slice(last))
  return out
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
