import { useState } from 'react'

import { inn, mapEmbedUrl, mapLinkUrl } from '../lib/contact'
import { fetchPageCopy, paragraphs, submitInquiry } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Prose } from '../components/Layout'
import { Gallery, fromPagePhotos } from '../components/Gallery'

/**
 * Who runs the inn, where it is, and how to reach them.
 *
 * This is where the home page's story went when the home page became one
 * screenful that does not scroll. It is not a reinstatement of the old About
 * page that /local-area replaced: that one was the owner's prose and nothing
 * else, which is why it lost to a page answering "what is there to do here".
 * This one is the three things a visitor comes looking for when they have
 * decided they are interested — who these people are, where the house is, and
 * how to ask a question — and the last two are facts about the inn rather than
 * copy somebody has to write.
 *
 * So the page is never empty. With nothing written in the console it still has
 * an address, a telephone number, a map and a form; the paragraphs appear when
 * the owner has some, exactly as on every other page.
 */
export function About() {
  const copy = useAsync(() => fetchPageCopy('about'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <h1 className="text-center text-3xl font-semibold tracking-tight sm:text-4xl">About us</h1>

        {copy.data && (
          <>
            <Gallery photos={fromPagePhotos(copy.data.photos)} eager />
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          </>
        )}

        {/*
          Two columns at lg and stacked below it, with the details first in the
          source order either way. Somebody who wants the telephone number is
          not going to scroll past a form to find it.
        */}
        <section className="grid gap-10 lg:grid-cols-2 lg:items-start">
          <FindUs />
          <ContactForm />
        </section>
      </div>
    </Layout>
  )
}

/**
 * The address, the telephone, the email, and a map with the house on it.
 *
 * The map is OpenStreetMap's embed — an iframe and no JavaScript. A key-free
 * map that cannot run code on a page that also has a contact form on it is
 * worth more here than a prettier one that can, and it is one line of CSP
 * (`frame-src`) rather than two.
 */
function FindUs() {
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-2xl font-semibold tracking-tight">Where to find us</h2>

      <address className="flex flex-col gap-2 not-italic text-neutral-700">
        {/* The postal address as it would be written on an envelope, which is
            also how it should be read aloud over the telephone. */}
        <p>
          {inn.street}
          <br />
          {inn.locality}, {inn.region} {inn.postalCode}
        </p>
        <p className="flex flex-wrap gap-x-4 gap-y-1">
          <a href={inn.phoneHref} className="underline underline-offset-4 hover:text-neutral-900">
            {inn.phone}
          </a>
          <a
            href={`mailto:${inn.email}`}
            className="underline underline-offset-4 hover:text-neutral-900"
          >
            {inn.email}
          </a>
        </p>
      </address>

      <iframe
        // The title is what a screen reader announces in place of the map, and
        // for anybody who cannot use a map it is the only thing this element
        // says — so it states the address rather than the word "map".
        title={`Map showing ${inn.name} at ${inn.street}, ${inn.locality}, ${inn.region}`}
        src={mapEmbedUrl}
        loading="lazy"
        // No referrer: the map host has no business knowing which page of the
        // inn's site somebody was reading, and this is the only third-party
        // request the public site makes at all.
        referrerPolicy="no-referrer"
        className="aspect-4/3 w-full rounded-lg border border-neutral-200 bg-neutral-100"
      />

      <a
        href={mapLinkUrl}
        target="_blank"
        rel="noopener noreferrer"
        className="self-start text-sm text-neutral-600 underline underline-offset-4 hover:text-neutral-900"
      >
        Open the map for directions
      </a>
    </section>
  )
}

/**
 * A way to write to the inn.
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

      {/* Left-aligned: it is a form, like the search on the home page. */}
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
