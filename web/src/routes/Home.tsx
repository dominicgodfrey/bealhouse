import { useState } from 'react'
import { Link } from 'react-router'

import { fetchPageCopy, paragraphs, submitInquiry } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Prose } from '../components/Layout'
import { SearchForm } from '../components/SearchForm'
import { Gallery, fromPagePhotos } from '../components/Gallery'

/**
 * The home page, anchored on booking.
 *
 * The search is first and stays first: it is the one thing on this site that
 * makes money, and its date picker already knows what can genuinely be sold.
 * Everything below it is the owner's — their words and their photographs from
 * the console — so this file describes a shape rather than an inn.
 *
 * It deliberately does not list the rooms. A grid of three-of-seven made the
 * rooms page redundant while answering the wrong question: somebody who has not
 * chosen dates cannot be told which rooms are free, and somebody who has is
 * already past this page. The nav and the search both lead to /rooms, which
 * shows all seven properly.
 *
 * The owner's story sits at the bottom, after the search and the two things the
 * inn does besides rooms. It is what a visitor reads once they are interested,
 * not what they came for.
 */
export function Home() {
  const copy = useAsync(() => fetchPageCopy('home'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-14">
        <section className="flex flex-col gap-8">
          <div className="flex flex-col items-center gap-3 text-center">
            <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
              Seven rooms in the White Mountains
            </h1>
          </div>

          {/*
            The search itself is not centred text — it is a form, and its fields
            read left to right like every other form on the site.
          */}
          <SearchForm alwaysOpen />
        </section>

        <section className="grid gap-6 sm:grid-cols-2">
          <Link
            to="/restaurant"
            className="rounded-lg border border-neutral-200 p-5 text-center transition hover:border-neutral-400"
          >
            <h2 className="text-lg font-semibold tracking-tight">The restaurant</h2>
            <p className="mt-1 text-sm text-neutral-600">
              What the kitchen is serving, updated as it changes.
            </p>
          </Link>
          <Link
            to="/events"
            className="rounded-lg border border-neutral-200 p-5 text-center transition hover:border-neutral-400"
          >
            <h2 className="text-lg font-semibold tracking-tight">Events</h2>
            <p className="mt-1 text-sm text-neutral-600">
              Gatherings at the inn, and how to ask about one.
            </p>
          </Link>
        </section>

        {/*
          The owner's own account of the place, and a way to write to them, side
          by side at lg and stacked below it.

          Two columns rather than one under the other because the second is the
          reply to the first: somebody who has just read who Hwasoo and Tom are
          is the person most likely to have a question, and putting the box
          right there is the difference between a question asked and a tab
          closed.
        */}
        <section className="grid gap-10 lg:grid-cols-2 lg:items-start">
          {/*
            Photograph and prose are independent — either can be empty and this
            renders whatever there is, or nothing at all.
          */}
          {(copy.data?.written || copy.data?.photos.length) && (
            <div className="flex flex-col gap-6">
              <Gallery photos={fromPagePhotos(copy.data.photos)} />
              {copy.data.written && (
                <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
              )}
            </div>
          )}

          <ContactForm />
        </section>
      </div>
    </Layout>
  )
}

/**
 * A way to write to the inn, next to the paragraph about who runs it.
 *
 * It lands in the same inbox the events form does, marked `contact` so the
 * owner can tell a general question from a wedding enquiry. One table and one
 * screen, because both are messages a person reads and answers the same way,
 * and two inboxes is two places to forget to look.
 *
 * Like the events form it inserts a row and stops: no email, no ticket, no
 * auto-reply. The honest promise is that somebody reads it, and the thank-you
 * says exactly that and nothing more.
 */
function ContactForm() {
  const [form, setForm] = useState({ name: '', email: '', phone: '', message: '' })
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [sent, setSent] = useState(false)

  function set<K extends keyof typeof form>(key: K, value: string) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setWorking(true)
    setError(null)
    try {
      await submitInquiry({ ...form, eventDate: '', partySize: 0, kind: 'contact' })
      setSent(true)
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'))
    } finally {
      setWorking(false)
    }
  }

  if (sent) {
    return (
      <section className="flex flex-col gap-2 rounded-lg border border-neutral-200 bg-neutral-50 p-6">
        <h2 className="text-2xl font-semibold tracking-tight">Thank you</h2>
        <p className="text-neutral-700">
          We have your message and one of us will write back. If it is urgent, the inn's phone is
          the faster route.
        </p>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-6">
      <div className="flex flex-col gap-1">
        <h2 className="text-2xl font-semibold tracking-tight">Get in touch</h2>
        <p className="text-sm text-neutral-600">
          A question about a room, the restaurant, or anything else. One of us reads it and writes
          back.
        </p>
      </div>

      {error && <ErrorNote error={error} />}

      {/* Left-aligned: it is a form, like the search above it. */}
      <form onSubmit={submit} className="flex flex-col gap-4 text-left">
        <ContactField label="Your name">
          <input
            required
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            autoComplete="name"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Email">
          <input
            required
            type="email"
            value={form.email}
            onChange={(e) => set('email', e.target.value)}
            autoComplete="email"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Phone" hint="Optional.">
          <input
            type="tel"
            value={form.phone}
            onChange={(e) => set('phone', e.target.value)}
            autoComplete="tel"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Your message">
          <textarea
            required
            rows={5}
            value={form.message}
            onChange={(e) => set('message', e.target.value)}
            className={contactInput}
          />
        </ContactField>

        <button
          type="submit"
          disabled={working}
          className="self-start rounded-lg bg-neutral-900 px-5 py-3 text-sm font-medium text-white hover:bg-neutral-700 disabled:opacity-60"
        >
          {working ? 'Sending…' : 'Send it'}
        </button>
      </form>
    </section>
  )
}

// py-3 rather than py-2: a 44px target is what a thumb can hit, and iOS zooms
// the whole page in on focus if the text is under 16px.
const contactInput = 'w-full rounded-lg border border-neutral-300 px-3 py-3 text-base'

function ContactField({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-neutral-500">{hint}</span>}
    </label>
  )
}
