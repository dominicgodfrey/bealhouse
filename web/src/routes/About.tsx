import { useState } from 'react'

import { inn, mapEmbedUrl, mapLinkUrl } from '../lib/contact'
import { fetchPageCopy, paragraphs, submitInquiry } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Prose } from '../components/Layout'
import { Gallery, fromPagePhotos } from '../components/Gallery'

/**
 * Who runs the inn, where it is, and how to reach them.
 *
 * Where the home page's story went when that page became one screenful. Not a
 * reinstatement of the About page /local-area replaced: that one was prose and
 * nothing else, which is why it lost. **This one is never empty** — with
 * nothing written in the console it still has an address, a telephone number, a
 * map and a form, and the paragraphs appear when the owner has some.
 */
export function About() {
  const copy = useAsync(() => fetchPageCopy('about'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <h1 className="text-center text-3xl font-semibold tracking-tight sm:text-4xl">About us</h1>

        {/*
          The owners beside their own words, not above them: the paragraph is
          two sentences and a full-width gallery over it left the page reading
          as a photograph with a caption. Either half can be missing — the
          column simply holds whichever there is.
        */}
        {copy.data && (copy.data.written || copy.data.photos.length > 0) && (
          <section className="grid items-center gap-8 sm:grid-cols-2">
            {copy.data.photos.length > 0 && (
              <Gallery photos={fromPagePhotos(copy.data.photos)} eager aspect="aspect-[4/5]" />
            )}
            <Prose
              heading={copy.data.heading}
              paragraphs={paragraphs(copy.data.body)}
              align="left"
            />
          </section>
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

/** The address, the telephone, the email, and a map with the house on it. */
function FindUs() {
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-2xl font-semibold tracking-tight">Where to find us</h2>

      <address className="flex flex-col gap-2 not-italic text-neutral-700">
        {/* As it would be written on an envelope. */}
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
        // Announced in place of the map, so it states the address rather than
        // the word "map".
        title={`Map showing ${inn.name} at ${inn.street}, ${inn.locality}, ${inn.region}`}
        src={mapEmbedUrl}
        loading="lazy"
        // The map host has no business knowing which page somebody was on.
        referrerPolicy="no-referrer"
        className="aspect-4/3 w-full rounded-lg border border-sienna-line bg-sienna"
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
 * A way to write to the inn. Same inbox as the events form, marked `contact` so
 * the owner can tell a general question from a wedding inquiry — two inboxes is
 * two places to forget to look.
 *
 * It inserts a row and stops: no email, no ticket, no auto-reply. The honest
 * promise is that somebody reads it, and the thank-you says exactly that.
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
      <section className="flex flex-col gap-2 rounded-lg border border-sienna-line bg-sienna p-6">
        <h2 className="text-2xl font-semibold tracking-tight">Thank you</h2>
        <p className="text-neutral-700">
          We have your message and one of us will write back. If it is urgent, the inn's phone is
          the faster route.
        </p>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-4 rounded-lg border border-sienna-line bg-sienna p-6">
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
        <ContactField label="Your name" required>
          <input
            required
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            autoComplete="name"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Email" required>
          <input
            required
            type="email"
            value={form.email}
            onChange={(e) => set('email', e.target.value)}
            autoComplete="email"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Phone">
          <input
            type="tel"
            value={form.phone}
            onChange={(e) => set('phone', e.target.value)}
            autoComplete="tel"
            className={contactInput}
          />
        </ContactField>
        <ContactField label="Your message" required>
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
  required = false,
  children,
}: {
  label: string
  hint?: string
  /**
   * Marks the label rather than the input — the `required` attribute on the
   * control is what actually enforces it, and this is the part somebody can see
   * before they start typing.
   */
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">
        {label}
        {/*
          aria-hidden, because the input's own `required` is already what a
          screen reader announces — read out, this would be the word "asterisk"
          in the middle of the label. The colour is not carrying the meaning on
          its own either: the star is there in any palette, and the fields
          without one are the optional ones.
        */}
        {required && (
          <span aria-hidden="true" className="ml-0.5 text-red-600">
            *
          </span>
        )}
      </span>
      {children}
      {hint && <span className="text-xs text-neutral-500">{hint}</span>}
    </label>
  )
}
