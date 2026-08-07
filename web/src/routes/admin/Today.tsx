import { useState } from 'react'
import { Link } from 'react-router'

import { fetchBoard, type Stay } from '../../lib/console'
import { addDays, formatLong, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { Button, Card, Empty, MoneyLine, Screen, Section } from './ui'

/**
 * What is happening at the inn today: who arrives, who leaves, who is in a bed.
 *
 * The console's first screen because it is the only one anybody opens without a
 * reason. Everything else answers a question somebody already had; this answers
 * the one they have every morning.
 *
 * The date is a control rather than a clock read on load, so tonight's owner can
 * look at tomorrow — and so a phone in another timezone still shows the day at
 * the inn, which is the only "today" this system recognises.
 */
export function Today() {
  const [on, setOn] = useState(today())
  const board = useAsync(() => fetchBoard(on), [on])

  return (
    <Screen
      title={board.data?.date === today() ? 'Today' : formatLong(on)}
      subtitle={
        board.data &&
        `Check-in from ${board.data.checkinTime} · check-out by ${board.data.checkoutTime}`
      }
      actions={
        <div className="flex gap-2">
          <Button onClick={() => setOn(addDays(on, -1))}>←</Button>
          <Button onClick={() => setOn(today())}>Today</Button>
          <Button onClick={() => setOn(addDays(on, 1))}>→</Button>
        </div>
      }
    >
      {board.loading && <Loading what="today" />}
      {board.error && <ErrorNote error={board.error} />}

      {board.data && (
        <>
          {/*
            The two counts that are about somewhere else entirely, carried on
            this response because this is the screen that gets opened. A flag
            nobody navigates to is a flag found a week late — and a refused card
            is money the inn is owed.
          */}
          {board.data.flagged > 0 && (
            <Link
              to="/admin/bookings?flagged=true"
              className="rounded-lg border border-red-300 bg-red-50 px-4 py-3 text-sm font-medium text-red-900"
            >
              {board.data.flagged === 1
                ? '1 booking has a refused card'
                : `${board.data.flagged} bookings have refused cards`}{' '}
              — open them →
            </Link>
          )}

          {board.data.newInquiries > 0 && (
            <Link
              to="/admin/inquiries"
              className="rounded-lg border border-neutral-300 bg-white px-4 py-3 text-sm font-medium"
            >
              {board.data.newInquiries === 1
                ? '1 new event inquiry'
                : `${board.data.newInquiries} new event inquiries`}{' '}
              →
            </Link>
          )}

          <Section title="Arriving">
            {board.data.arrivals.length === 0 ? (
              <Empty>Nobody arrives today.</Empty>
            ) : (
              board.data.arrivals.map((stay) => <StayRow key={stay.code} stay={stay} />)
            )}
          </Section>

          <Section title="Leaving">
            {board.data.departures.length === 0 ? (
              <Empty>Nobody leaves today.</Empty>
            ) : (
              board.data.departures.map((stay) => <StayRow key={stay.code} stay={stay} />)
            )}
          </Section>

          <Section title="In house">
            {board.data.inHouse.length === 0 ? (
              <Empty>No rooms are occupied tonight.</Empty>
            ) : (
              board.data.inHouse.map((stay) => <StayRow key={stay.code} stay={stay} />)
            )}
          </Section>
        </>
      )}
    </Screen>
  )
}

/**
 * One stay on the board.
 *
 * The phone number is a link because the reason an owner is looking at this row
 * is usually that they need to ring the person in it.
 */
function StayRow({ stay }: { stay: Stay }) {
  return (
    <Card tone={stay.chargeFailed ? 'alarm' : 'plain'}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <Link to={`/admin/bookings/${stay.code}`} className="font-medium underline-offset-2 hover:underline">
          {stay.guestName}
        </Link>
        <span className="font-mono text-xs text-neutral-500">{stay.code}</span>
      </div>

      <div className="text-sm text-neutral-600">
        {stay.rooms || 'no room recorded'} · {stay.guests}{' '}
        {stay.guests === 1 ? 'guest' : 'guests'}
        {stay.withPet && ' · with a pet'}
        <br />
        {formatLong(stay.checkin)} → {formatLong(stay.checkout)} ({stay.nights}{' '}
        {stay.nights === 1 ? 'night' : 'nights'})
      </div>

      <MoneyLine stay={stay} />

      <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        <a href={`mailto:${stay.guestEmail}`} className="text-neutral-700 underline">
          {stay.guestEmail}
        </a>
        {stay.guestPhone && (
          <a href={`tel:${stay.guestPhone}`} className="text-neutral-700 underline">
            {stay.guestPhone}
          </a>
        )}
      </div>
    </Card>
  )
}
