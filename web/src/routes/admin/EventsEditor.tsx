import { useEffect, useState } from 'react'

import {
  fetchEvents,
  fetchInquiries,
  saveEvents,
  setInquiryStatus,
  type EventItem,
  type Inquiry,
  type InquiryStatus,
} from '../../lib/console'
import { formatInstant } from '../../lib/admin'
import { formatShort } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Photos } from './Photos'
import {
  Aside,
  Button,
  Card,
  Empty,
  Field,
  Input,
  Saved,
  Screen,
  Textarea,
  report,
  useReload,
  useSaving,
} from './ui'

/**
 * The events business: what is on, and its gallery.
 *
 * Drafting and publishing are different acts, so an unpublished event is
 * invisible on the public page and visible here — an owner sketching out next
 * summer must not accidentally announce it.
 */
export function EventsEditor() {
  const { refresh } = useConsole()
  const loaded = useAsync(fetchEvents, [])
  const saving = useSaving(refresh)

  const [events, setEvents] = useState<EventItem[] | null>(null)

  useEffect(() => {
    if (loaded.data) setEvents(loaded.data)
  }, [loaded.data])

  function update(next: EventItem[]) {
    setEvents(next)
    saving.clear()
  }

  return (
    <Screen title="Events" subtitle="What is happening at the inn.">
      {loaded.loading && <Loading what="events" />}
      {loaded.error && <ErrorNote error={loaded.error} />}
      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved.</Saved>}

      <Aside>
        Only published events appear on the site, and only ones that have not already happened.
        Booking an event is not something this system does — the page collects an enquiry and you
        answer it.
      </Aside>

      {events?.length === 0 && <Empty>No events yet.</Empty>}

      {events?.map((event, ei) => (
        <EventCard
          key={ei}
          event={event}
          onChange={(next) => update(events.map((e, i) => (i === ei ? next : e)))}
          onRemove={() => update(events.filter((_, i) => i !== ei))}
        />
      ))}

      {events && (
        <div className="flex flex-wrap gap-2">
          <Button
            onClick={() =>
              update([
                ...events,
                { title: '', happensOn: '', description: '', published: false, photos: [] },
              ])
            }
          >
            Add an event
          </Button>
          <Button
            kind="primary"
            onClick={() => saving.run(() => saveEvents(events))}
            disabled={saving.working}
          >
            {saving.working ? 'Saving…' : 'Save events'}
          </Button>
        </div>
      )}
    </Screen>
  )
}

function EventCard({
  event,
  onChange,
  onRemove,
}: {
  event: EventItem
  onChange: (event: EventItem) => void
  onRemove: () => void
}) {
  return (
    <Card>
      <Field label="Title">
        <Input value={event.title} onChange={(e) => onChange({ ...event, title: e.target.value })} />
      </Field>

      <Field label="Date" hint="Leave empty for something that runs all season.">
        <Input
          type="date"
          value={event.happensOn ?? ''}
          onChange={(e) => onChange({ ...event, happensOn: e.target.value })}
        />
      </Field>

      <Field label="Description">
        <Textarea
          rows={4}
          value={event.description}
          onChange={(e) => onChange({ ...event, description: e.target.value })}
        />
      </Field>

      <Photos photos={event.photos} onChange={(photos) => onChange({ ...event, photos })} />

      <label className="flex items-center gap-2 text-sm font-medium">
        <input
          type="checkbox"
          checked={event.published}
          onChange={(e) => onChange({ ...event, published: e.target.checked })}
        />
        Published — visible on the site
      </label>

      <Button onClick={onRemove}>Remove this event</Button>
    </Card>
  )
}

/**
 * The inquiry inbox.
 *
 * Nothing here sends anything: the form on the public page inserts a row and
 * that is all it does, so this list is the whole system's memory of it. The
 * three statuses exist so an owner can tell what they have already answered
 * without keeping it in their head.
 */
export function Inquiries() {
  const [status, setStatus] = useState<string>('')
  const [kind, setKind] = useState<string>('')
  const [nonce, reload] = useReload()
  const inquiries = useAsync(
    () => fetchInquiries(status || undefined, kind || undefined),
    [status, kind, nonce],
  )

  return (
    <Screen
      title="Messages"
      subtitle="From the events page and the contact form."
      actions={
        <div className="flex flex-col gap-2 sm:items-end">
          <div className="flex flex-wrap gap-2">
            {[
              ['', 'All'],
              ['new', 'New'],
              ['contacted', 'Answered'],
              ['closed', 'Closed'],
            ].map(([value, label]) => (
              <Button key={value} onClick={() => setStatus(value)} kind={status === value ? 'primary' : 'plain'}>
                {label}
              </Button>
            ))}
          </div>

          {/*
            One inbox with a filter rather than two screens. Both are messages a
            person reads and answers the same way, and a second screen is a
            second place to forget to look.
          */}
          <div className="flex flex-wrap gap-2">
            {[
              ['', 'Both kinds'],
              ['event', 'Events'],
              ['contact', 'Contact form'],
            ].map(([value, label]) => (
              <Button key={value} onClick={() => setKind(value)} kind={kind === value ? 'primary' : 'plain'}>
                {label}
              </Button>
            ))}
          </div>
        </div>
      }
    >
      {inquiries.loading && <Loading what="enquiries" />}
      {inquiries.error && <ErrorNote error={inquiries.error} />}
      {inquiries.data?.length === 0 && <Empty>Nothing here.</Empty>}
      {inquiries.data?.map((inquiry) => (
        <InquiryCard key={inquiry.id} inquiry={inquiry} onChanged={reload} />
      ))}
    </Screen>
  )
}

function InquiryCard({ inquiry, onChanged }: { inquiry: Inquiry; onChanged: () => void }) {
  const { refresh } = useConsole()
  const [error, setError] = useState<Error | null>(null)

  async function move(status: InquiryStatus) {
    setError(null)
    try {
      await setInquiryStatus(inquiry.id, status)
      onChanged()
    } catch (err) {
      report(err, refresh, setError)
    }
  }

  return (
    <Card>
      {error && <ErrorNote error={error} />}

      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="flex items-baseline gap-2">
          <span className="font-medium">{inquiry.name}</span>
          <span className="rounded bg-neutral-100 px-2 py-0.5 text-xs text-neutral-600">
            {inquiry.kind === 'contact' ? 'Contact form' : 'Events'}
          </span>
        </span>
        <span className="text-xs text-neutral-500">{formatInstant(inquiry.at)}</span>
      </div>

      <p className="text-sm">
        <a href={`mailto:${inquiry.email}`} className="underline">
          {inquiry.email}
        </a>
        {inquiry.phone && (
          <>
            {' · '}
            <a href={`tel:${inquiry.phone}`} className="underline">
              {inquiry.phone}
            </a>
          </>
        )}
      </p>

      {/*
        Events only. A contact message has no date and no party size, and
        printing "no date given" against a question about parking is noise
        dressed up as information.
      */}
      {inquiry.kind !== 'contact' && (
        <p className="text-sm text-neutral-600">
          {inquiry.eventDate ? formatShort(inquiry.eventDate) : 'no date given'}
          {inquiry.partySize ? ` · about ${inquiry.partySize} people` : ''}
        </p>
      )}

      {inquiry.message && <p className="text-sm whitespace-pre-wrap">{inquiry.message}</p>}

      <div className="flex flex-wrap gap-2">
        {inquiry.status !== 'contacted' && (
          <Button onClick={() => move('contacted')}>Mark answered</Button>
        )}
        {inquiry.status !== 'closed' && <Button onClick={() => move('closed')}>Close</Button>}
        {inquiry.status !== 'new' && <Button onClick={() => move('new')}>Back to new</Button>}
      </div>
    </Card>
  )
}
