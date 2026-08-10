import { useState } from 'react'
import { Link, useParams } from 'react-router'

import { addNote, deleteNote, fetchGuest } from '../../lib/console'
import { formatInstant } from '../../lib/admin'
import { formatShort } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import {
  Button,
  Card,
  Empty,
  Money,
  MoneyLine,
  Screen,
  Section,
  StatusPill,
  Textarea,
  report,
  useReload,
} from './ui'

/*
 * The guest list that used to live here is the reservations search now: one box
 * returning people and stays together, because "Sarah rang" is a name and not a
 * choice of tab. What stays here is one person's file, which the search links
 * into and which has no equivalent on the reservations side.
 */

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
