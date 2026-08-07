import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'

import { addNote, deleteNote, fetchGuest, fetchGuests, type GuestCard } from '../../lib/console'
import { formatInstant } from '../../lib/admin'
import { formatShort } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import {
  Button,
  Card,
  Empty,
  Field,
  Input,
  Money,
  MoneyLine,
  Screen,
  Section,
  StatusPill,
  Textarea,
  report,
  useReload,
} from './ui'

/**
 * The guest history, searchable the way an owner actually remembers people: by
 * a name, half an email, a phone number, or the code on the paperwork in front
 * of them.
 */
export function Guests() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''

  const guests = useAsync(() => fetchGuests({ q: q || undefined }), [q])

  return (
    <Screen title="Guests" subtitle="Everyone who has ever booked.">
      <Card>
        <Field label="Search" hint="A name, an email, a phone number, or a booking code.">
          <Input
            value={q}
            onChange={(e) => setParams(e.target.value ? { q: e.target.value } : {}, { replace: true })}
            placeholder="Sarah, or sarah@…, or K3F9QX"
          />
        </Field>
      </Card>

      {guests.loading && <Loading what="guests" />}
      {guests.error && <ErrorNote error={guests.error} />}
      {guests.data?.length === 0 && <Empty>Nobody matches that.</Empty>}
      {guests.data?.map((guest) => <Row key={guest.id} guest={guest} />)}
    </Screen>
  )
}

function Row({ guest }: { guest: GuestCard }) {
  return (
    <Card>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <Link to={`/admin/guests/${guest.id}`} className="font-medium underline-offset-2 hover:underline">
          {guest.name}
        </Link>
        <span className="text-sm text-neutral-600">
          {guest.stays === 0
            ? 'no stays yet'
            : `${guest.stays} ${guest.stays === 1 ? 'stay' : 'stays'}`}
        </span>
      </div>

      <p className="text-sm text-neutral-600">
        {guest.email}
        {guest.phone && ` · ${guest.phone}`}
      </p>

      <p className="text-sm text-neutral-600">
        <Money cents={guest.lifetimeCents} /> collected
        {guest.lastCheckout && ` · last here ${formatShort(guest.lastCheckout)}`}
        {guest.notes > 0 && ` · ${guest.notes} ${guest.notes === 1 ? 'note' : 'notes'}`}
      </p>
    </Card>
  )
}

/**
 * One guest's file: who they are, every stay, and what the owners wrote down.
 *
 * The notes are most of what a seven-room inn actually runs on and none of it
 * fits in a booking — which room they like, that the dog is fine, that they
 * always arrive late.
 */
export function GuestFile() {
  const { id = '' } = useParams()
  const [nonce, reload] = useReload()
  const guest = useAsync(() => fetchGuest(Number(id)), [id, nonce])

  return (
    <Screen
      title={guest.data?.guest.name ?? 'Guest'}
      subtitle={guest.data?.guest.email}
      actions={
        <Link to="/admin/guests">
          <Button>All guests</Button>
        </Link>
      }
    >
      {guest.loading && <Loading what="this guest" />}
      {guest.error && <ErrorNote error={guest.error} />}

      {guest.data && (
        <>
          <Card>
            <p className="text-sm text-neutral-600">
              <a href={`mailto:${guest.data.guest.email}`} className="underline">
                {guest.data.guest.email}
              </a>
              {guest.data.guest.phone && (
                <>
                  {' · '}
                  <a href={`tel:${guest.data.guest.phone}`} className="underline">
                    {guest.data.guest.phone}
                  </a>
                </>
              )}
            </p>
            <p className="text-sm text-neutral-600">
              {guest.data.guest.stays === 0
                ? 'No completed stays.'
                : `${guest.data.guest.stays} ${
                    guest.data.guest.stays === 1 ? 'stay' : 'stays'
                  }, `}
              {guest.data.guest.stays > 0 && (
                <>
                  <Money cents={guest.data.guest.lifetimeCents} /> collected.
                </>
              )}
            </p>
          </Card>

          <Notes
            guestId={guest.data.guest.id}
            notes={guest.data.notes}
            onChanged={reload}
          />

          <Section title="Stays">
            {guest.data.bookings.length === 0 ? (
              <Empty>No bookings.</Empty>
            ) : (
              guest.data.bookings.map((stay) => (
                <Card key={stay.code}>
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <Link
                      to={`/admin/bookings/${stay.code}`}
                      className="font-mono text-sm underline-offset-2 hover:underline"
                    >
                      {stay.code}
                    </Link>
                    <StatusPill status={stay.status} />
                  </div>
                  <p className="text-sm text-neutral-600">
                    {formatShort(stay.checkin)} → {formatShort(stay.checkout)} ·{' '}
                    {stay.rooms || '—'}
                  </p>
                  <MoneyLine stay={stay} />
                </Card>
              ))
            )}
          </Section>
        </>
      )}
    </Screen>
  )
}

function Notes({
  guestId,
  notes,
  onChanged,
}: {
  guestId: number
  notes: { id: number; body: string; author?: string; at: string }[]
  onChanged: () => void
}) {
  const { refresh } = useConsole()
  const [body, setBody] = useState('')
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await addNote(guestId, body)
      setBody('')
      onChanged()
    } catch (err) {
      report(err, refresh, setError)
    } finally {
      setWorking(false)
    }
  }

  async function drop(noteId: number) {
    setError(null)
    try {
      await deleteNote(guestId, noteId)
      onChanged()
    } catch (err) {
      report(err, refresh, setError)
    }
  }

  return (
    <Section title="Notes" note="Only you and the other phone can see these.">
      <Card>
        {error && <ErrorNote error={error} />}
        <Textarea
          rows={3}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Prefers the back of the house. Allergic to feather pillows."
        />
        <Button kind="primary" onClick={submit} disabled={working || !body.trim()}>
          {working ? 'Saving…' : 'Add a note'}
        </Button>
      </Card>

      {notes.map((note) => (
        <Card key={note.id}>
          <p className="text-sm whitespace-pre-wrap">{note.body}</p>
          <p className="text-xs text-neutral-500">
            {note.author ? `${note.author} · ` : ''}
            {formatInstant(note.at)}
          </p>
          <Button onClick={() => drop(note.id)}>Delete</Button>
        </Card>
      ))}
    </Section>
  )
}
