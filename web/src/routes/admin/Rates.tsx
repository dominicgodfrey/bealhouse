import { useState } from 'react'

import {
  deleteSeason,
  fetchRates,
  previewSeason,
  rebuildRates,
  saveSeason,
  type RateBoard,
  type RateChange,
  type Season,
} from '../../lib/console'
import { addMonths, formatShort, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import {
  Aside,
  Button,
  Card,
  Empty,
  Field,
  Input,
  Money,
  Screen,
  Section,
  centsToInput,
  inputToCents,
  report,
  useReload,
} from './ui'

/**
 * The rate editor (decision #21): seasons down, rooms across.
 *
 * The thing that makes this screen safe rather than frightening is the preview.
 * Saving a season regenerates the whole nightly calendar, and an owner who
 * cannot see what that will do will either not touch it or touch it once and
 * republish the wrong price across a summer. So the diff is computed by applying
 * the edit inside a transaction and rolling it back — the real resolution rule,
 * not an estimate — and shown before the publish button.
 */
export function Rates() {
  const [nonce, reload] = useReload()
  const board = useAsync(fetchRates, [nonce])

  return (
    <Screen title="Rates" subtitle="Per room, per season. Saving regenerates the nightly calendar.">
      {board.loading && <Loading what="the rate grid" />}
      {board.error && <ErrorNote error={board.error} />}
      {board.data && <Board board={board.data} onChanged={reload} />}
    </Screen>
  )
}

function Board({ board, onChanged }: { board: RateBoard; onChanged: () => void }) {
  const [editing, setEditing] = useState<Season | null>(null)

  const horizonIsShort =
    !board.horizon || board.horizon < addMonths(today(), 6)

  return (
    <>
      {horizonIsShort && (
        <p className="rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          {board.horizon
            ? `The calendar is only priced to ${formatShort(board.horizon)}.`
            : 'No nights are priced at all, so no room can be sold.'}{' '}
          Nights past the end of it cannot be booked, silently — the room just stops appearing in
          searches. Rebuild below, and check the monthly job is running.
        </p>
      )}

      <Aside>
        A season’s dates are <strong>inclusive</strong>: the last night is a night somebody sleeps
        here, not a checkout. Where seasons overlap, the higher priority wins — that is how a
        holiday weekend sits inside a longer season.
      </Aside>

      {editing ? (
        <Editor
          board={board}
          season={editing}
          onDone={() => {
            setEditing(null)
            onChanged()
          }}
          onCancel={() => setEditing(null)}
        />
      ) : (
        <>
          <Section title="Seasons">
            {board.seasons.length === 0 ? (
              <Empty>No seasons yet, so nothing has a price and nothing can be booked.</Empty>
            ) : (
              board.seasons.map((season) => (
                <Card key={season.id}>
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <span className="font-medium">{season.name}</span>
                    <span className="text-sm text-neutral-600">
                      {formatShort(season.startsOn)} – {formatShort(season.endsOn)}
                    </span>
                  </div>

                  <p className="text-sm text-neutral-600">
                    Minimum stay {season.minStay ?? board.defaultMinStay}
                    {season.minStay ? '' : ' (the house default)'} · priority {season.priority}
                  </p>

                  <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
                    {board.rooms.map((room) => {
                      const cents = season.prices[String(room.id)]
                      return (
                        <span key={room.id} className="text-neutral-600">
                          {room.name}{' '}
                          {cents ? (
                            <span className="font-medium text-neutral-900">
                              <Money cents={cents} />
                            </span>
                          ) : (
                            <span className="text-neutral-400">—</span>
                          )}
                        </span>
                      )
                    })}
                  </div>

                  <Button onClick={() => setEditing(season)}>Edit</Button>
                </Card>
              ))
            )}
          </Section>

          <div className="flex flex-wrap gap-2">
            <Button kind="primary" onClick={() => setEditing(blank())}>
              New season
            </Button>
            <RebuildButton onDone={onChanged} />
          </div>
        </>
      )}
    </>
  )
}

function blank(): Season {
  return {
    id: 0,
    name: '',
    startsOn: today(),
    endsOn: addMonths(today(), 3),
    minStay: null,
    priority: 0,
    prices: {},
  }
}

function Editor({
  board,
  season,
  onDone,
  onCancel,
}: {
  board: RateBoard
  season: Season
  onDone: () => void
  onCancel: () => void
}) {
  const { refresh } = useConsole()

  const [draft, setDraft] = useState<Season>(season)
  const [change, setChange] = useState<RateChange | null>(null)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  function set<K extends keyof Season>(key: K, value: Season[K]) {
    setDraft((d) => ({ ...d, [key]: value }))
    // Any edit invalidates the diff on screen. Leaving a stale one up would be
    // the one failure this whole feature exists to prevent.
    setChange(null)
  }

  function price(roomId: number, value: string) {
    const cents = inputToCents(value)
    setDraft((d) => {
      const prices = { ...d.prices }
      if (cents > 0) prices[String(roomId)] = cents
      else delete prices[String(roomId)]
      return { ...d, prices }
    })
    setChange(null)
  }

  async function run(action: () => Promise<RateChange>, then?: () => void) {
    setWorking(true)
    setError(null)
    try {
      const result = await action()
      setChange(result)
      then?.()
    } catch (err) {
      report(err, refresh, setError)
    } finally {
      setWorking(false)
    }
  }

  return (
    <Card>
      {error && <ErrorNote error={error} />}

      <Field label="Name" hint="What you call it: Leaf Season, Thanksgiving, Mud Season.">
        <Input value={draft.name} onChange={(e) => set('name', e.target.value)} />
      </Field>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="First night">
          <Input
            type="date"
            value={draft.startsOn}
            onChange={(e) => set('startsOn', e.target.value)}
          />
        </Field>
        <Field label="Last night" hint="Inclusive — a night somebody sleeps here.">
          <Input type="date" value={draft.endsOn} onChange={(e) => set('endsOn', e.target.value)} />
        </Field>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          label="Minimum stay"
          hint={`Blank uses the house default of ${board.defaultMinStay}. A season can raise it, not lower it.`}
        >
          <Input
            type="number"
            min={1}
            value={draft.minStay ?? ''}
            onChange={(e) => set('minStay', e.target.value ? Number(e.target.value) : null)}
            placeholder={String(board.defaultMinStay)}
          />
        </Field>
        <Field label="Priority" hint="Higher wins where seasons overlap.">
          <Input
            type="number"
            value={draft.priority}
            onChange={(e) => set('priority', Number(e.target.value))}
          />
        </Field>
      </div>

      <Section
        title="Price per night"
        note="A room left blank is not priced by this season, and falls through to whatever else covers those nights."
      >
        <div className="grid gap-3 sm:grid-cols-2">
          {board.rooms.map((room) => (
            <Field key={room.id} label={room.name}>
              <Input
                inputMode="decimal"
                value={centsToInput(draft.prices[String(room.id)] ?? 0)}
                onChange={(e) => price(room.id, e.target.value)}
                placeholder="—"
              />
            </Field>
          ))}
        </div>
      </Section>

      {change && <Diff change={change} />}

      <div className="flex flex-wrap gap-2">
        <Button onClick={() => run(() => previewSeason(draft))} disabled={working}>
          {working ? 'Working…' : 'What would this change?'}
        </Button>
        <Button
          kind="primary"
          onClick={() => run(() => saveSeason(draft), onDone)}
          disabled={working}
        >
          Save and republish
        </Button>
        <Button onClick={onCancel} disabled={working}>
          Cancel
        </Button>
      </div>

      {draft.id !== 0 && (
        <DeleteSeason id={draft.id} name={draft.name} onDone={onDone} />
      )}
    </Card>
  )
}

/**
 * The number that makes publishing safe.
 *
 * Nights gaining and losing a price are called out separately from the total,
 * because they are the two an owner most needs to see and a bare "142 nights
 * change" hides both: a night losing its price drops the room out of every
 * search with no error anywhere.
 */
function Diff({ change }: { change: RateChange }) {
  return (
    <div className="rounded-lg border border-neutral-300 bg-neutral-50 px-4 py-3 text-sm">
      {change.nights === 0 ? (
        <p>Nothing changes. The calendar already says exactly this.</p>
      ) : (
        <>
          <p className="font-medium">
            {change.nights} {change.nights === 1 ? 'night' : 'nights'} change across {change.rooms}{' '}
            {change.rooms === 1 ? 'room' : 'rooms'}
            {change.firstNight && (
              <>
                , {formatShort(change.firstNight)} to {formatShort(change.lastNight!)}
              </>
            )}
            .
          </p>
          {change.nightsGainingAPrice > 0 && (
            <p className="text-emerald-800">
              {change.nightsGainingAPrice} become sellable that were not priced before.
            </p>
          )}
          {change.nightsLosingTheirPrice > 0 && (
            <p className="text-red-800">
              {change.nightsLosingTheirPrice} lose their price and stop being sellable at all.
            </p>
          )}
        </>
      )}

      <p className="mt-2 text-neutral-600">
        {change.confirmedBookings === 0
          ? 'No confirmed stays fall in this range.'
          : `${change.confirmedBookings} confirmed ${
              change.confirmedBookings === 1 ? 'stay falls' : 'stays fall'
            } in this range and ${
              change.confirmedBookings === 1 ? 'is' : 'are'
            } not affected — every booking keeps the nightly prices and tax rate it was sold at.`}
      </p>
    </div>
  )
}

function DeleteSeason({ id, name, onDone }: { id: number; name: string; onDone: () => void }) {
  const { refresh } = useConsole()
  const [confirming, setConfirming] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await deleteSeason(id)
      onDone()
    } catch (err) {
      report(err, refresh, setError)
      setWorking(false)
    }
  }

  return (
    <div className="flex flex-col gap-2 border-t border-neutral-200 pt-3">
      {error && <ErrorNote error={error} />}
      {confirming ? (
        <>
          <p className="text-sm text-neutral-600">
            Nights this season was the only cover for lose their price and stop being sellable.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button kind="danger" onClick={submit} disabled={working}>
              {working ? 'Deleting…' : `Delete ${name || 'this season'}`}
            </Button>
            <Button onClick={() => setConfirming(false)}>Keep it</Button>
          </div>
        </>
      ) : (
        <Button onClick={() => setConfirming(true)}>Delete this season</Button>
      )}
    </div>
  )
}

/**
 * The manual rebuild, here because the monthly job failing is invisible:
 * nothing breaks on the day it stops, the horizon just creeps closer.
 */
function RebuildButton({ onDone }: { onDone: () => void }) {
  const { refresh } = useConsole()
  const [working, setWorking] = useState(false)
  const [nights, setNights] = useState<number | null>(null)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      const result = await rebuildRates()
      setNights(result.nights)
      onDone()
    } catch (err) {
      report(err, refresh, setError)
    } finally {
      setWorking(false)
    }
  }

  return (
    <>
      <Button onClick={submit} disabled={working}>
        {working ? 'Rebuilding…' : 'Rebuild the calendar'}
      </Button>
      {nights !== null && (
        <span className="self-center text-sm text-neutral-600">{nights} nights priced.</span>
      )}
      {error && <ErrorNote error={error} />}
    </>
  )
}
