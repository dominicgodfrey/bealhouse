import { useState } from 'react'

import { fetchEvents, fetchPageCopy, paragraphs, submitInquiry, type EventItem } from '../lib/site'
import { formatLong } from '../lib/dates'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'
import { Photo } from '../components/Photo'

/**
 * The events business: what is on, and the form that starts a conversation.
 *
 * Deliberately not a booking flow. Decision #11 puts event booking and deposits
 * out of scope, so this collects an enquiry and the owner answers it — which is
 * how a wedding gets arranged anyway, and pretending otherwise would mean
 * building a second payment path for a case that has never been priced.
 */
export function Events() {
  const events = useAsync(fetchEvents, [])
  const copy = useAsync(() => fetchPageCopy('events'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <div className="flex flex-col gap-3">
          <h1 className="text-4xl font-semibold tracking-tight">Events</h1>
          {copy.data && (
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          )}
        </div>

        {events.loading && <Loading what="what's on" />}
        {events.error && <ErrorNote error={events.error} />}

        {events.data && events.data.length > 0 && (
          <section className="flex flex-col gap-6">
            <h2 className="text-2xl font-semibold tracking-tight">Coming up</h2>
            <div className="grid gap-6 sm:grid-cols-2">
              {events.data.map((event, i) => <Card key={i} event={event} />)}
            </div>
          </section>
        )}

        <InquiryForm />
      </div>
    </Layout>
  )
}

function Card({ event }: { event: EventItem }) {
  const photo = event.photos[0]

  return (
    <article className="flex flex-col gap-3 rounded-lg border border-neutral-200 p-4">
      {photo && (
        <Photo
          src={photo.path}
          alt={photo.alt}
          sources={photo}
          sizes="(min-width: 640px) 45vw, 100vw"
          className="aspect-[4/3] w-full rounded object-cover"
        />
      )}

      <div className="flex flex-col gap-1">
        <h3 className="text-lg font-semibold tracking-tight">{event.title}</h3>
        {event.happensOn && (
          <p className="text-sm text-neutral-600">{formatLong(event.happensOn)}</p>
        )}
      </div>

      {event.description && (
        <p className="text-sm whitespace-pre-wrap text-neutral-700">{event.description}</p>
      )}

      {/*
        Everything after the first photo, as a gallery. Alt text is required on
        every one of them — the column is NOT NULL — so a screen reader gets a
        description rather than a filename.
      */}
      {event.photos.length > 1 && (
        <div className="grid grid-cols-3 gap-2">
          {event.photos.slice(1).map((p, i) => (
            <Photo
              key={i}
              src={p.path}
              alt={p.alt}
              sources={p}
              // Three across, whatever the viewport.
              sizes="(min-width: 640px) 15vw, 30vw"
              className="aspect-square w-full rounded object-cover"
            />
          ))}
        </div>
      )}
    </article>
  )
}

/**
 * The one thing on this whole public site, other than booking a room, that
 * writes anything.
 *
 * It inserts a row and stops. No email goes out, nothing is charged, and there
 * is no confirmation beyond the sentence below — because the honest promise is
 * that a person will read it and reply, and a system that said more than that
 * would be saying something it cannot keep.
 */
function InquiryForm() {
  const [form, setForm] = useState({
    name: '',
    email: '',
    phone: '',
    eventDate: '',
    partySize: 0,
    message: '',
  })
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [sent, setSent] = useState(false)

  function set<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setWorking(true)
    setError(null)
    try {
      await submitInquiry(form)
      setSent(true)
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Something went wrong.'))
    } finally {
      setWorking(false)
    }
  }

  if (sent) {
    return (
      <section className="rounded-lg border border-neutral-200 bg-neutral-50 p-6">
        <h2 className="text-2xl font-semibold tracking-tight">Thank you</h2>
        <p className="mt-2 max-w-prose text-neutral-700">
          We have your message and one of us will write back. If it is urgent, the inn's phone is
          the faster route.
        </p>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-6">
      <div className="flex flex-col gap-1">
        <h2 className="text-2xl font-semibold tracking-tight">Tell us about it</h2>
        <p className="max-w-prose text-sm text-neutral-600">
          A rough date and a rough number is enough to start. Nothing here books anything or takes a
          payment — one of us reads it and writes back.
        </p>
      </div>

      {error && <ErrorNote error={error} />}

      <form onSubmit={submit} className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Your name">
            <input
              required
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              className={inputClass}
            />
          </Field>
          <Field label="Email">
            <input
              required
              type="email"
              value={form.email}
              onChange={(e) => set('email', e.target.value)}
              className={inputClass}
            />
          </Field>
          <Field label="Phone" hint="Optional.">
            <input
              value={form.phone}
              onChange={(e) => set('phone', e.target.value)}
              className={inputClass}
            />
          </Field>
          <Field label="Roughly when?" hint="Optional.">
            <input
              type="date"
              value={form.eventDate}
              onChange={(e) => set('eventDate', e.target.value)}
              className={inputClass}
            />
          </Field>
          <Field label="Roughly how many people?" hint="Optional.">
            <input
              type="number"
              min={1}
              value={form.partySize || ''}
              onChange={(e) => set('partySize', Number(e.target.value))}
              className={inputClass}
            />
          </Field>
        </div>

        <Field label="What are you thinking of?">
          <textarea
            rows={5}
            value={form.message}
            onChange={(e) => set('message', e.target.value)}
            className={inputClass}
          />
        </Field>

        <button
          type="submit"
          disabled={working}
          className="self-start rounded-lg bg-neutral-900 px-5 py-3 text-sm font-medium text-white disabled:opacity-60"
        >
          {working ? 'Sending…' : 'Send it'}
        </button>
      </form>
    </section>
  )
}

const inputClass = 'rounded-lg border border-neutral-300 px-3 py-3 text-sm'

function Field({
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
