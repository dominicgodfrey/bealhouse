import { useState } from 'react'

import { fetchRooms, saveRoom, type RoomContent } from '../../lib/console'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Photos } from './Photos'
import {
  Aside,
  Button,
  Card,
  Field,
  Input,
  Saved,
  Screen,
  Textarea,
  centsToInput,
  inputToCents,
  useReload,
  useSaving,
} from './ui'

/**
 * The room content editor — descriptions, amenities, beds, photos.
 *
 * Everything on this screen is the owner's, which is why the seed ships
 * descriptions marked PLACEHOLDER and no amenities at all: nothing here was
 * invented on their behalf, and this is where the real words land.
 *
 * What this does not do is upload files. The image pipeline (decision #16) is
 * not built, so the photo editor manages which files a room shows, in what
 * order, described how — and a room with no photos falls back to the
 * placeholder drawing on the public side rather than to a broken image.
 */
export function RoomsContent() {
  const [nonce, reload] = useReload()
  const rooms = useAsync(fetchRooms, [nonce])

  return (
    <Screen title="Rooms" subtitle="What guests read about each room.">
      {rooms.loading && <Loading what="the rooms" />}
      {rooms.error && <ErrorNote error={rooms.error} />}
      {rooms.data?.map((room) => <RoomEditor key={room.id} room={room} onSaved={reload} />)}
    </Screen>
  )
}

function RoomEditor({ room, onSaved }: { room: RoomContent; onSaved: () => void }) {
  const { refresh } = useConsole()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(room)
  const saving = useSaving(refresh)

  function set<K extends keyof RoomContent>(key: K, value: RoomContent[K]) {
    setDraft((d) => ({ ...d, [key]: value }))
    saving.clear()
  }

  if (!open) {
    return (
      <Card>
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <span className="font-medium">{room.name}</span>
          <span className="font-mono text-xs text-neutral-500">{room.slug}</span>
        </div>
        <p className="text-sm text-neutral-600">
          Sleeps {room.maxOccupancy} · {room.photos.length}{' '}
          {room.photos.length === 1 ? 'photo' : 'photos'} ·{' '}
          {room.amenities.length || 'no'} amenities
          {room.isPetFriendly && ' · Takes pets'}
        </p>
        {room.description.includes('PLACEHOLDER') && (
          <p className="text-sm text-amber-800">
            This description is still the placeholder that ships with the build. Guests are reading
            it.
          </p>
        )}
        <Button onClick={() => setOpen(true)}>Edit</Button>
      </Card>
    )
  }

  return (
    <Card>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="font-medium">{room.name}</span>
        <span className="font-mono text-xs text-neutral-500">{room.slug}</span>
      </div>

      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved. The public page shows it now.</Saved>}

      <Field label="Name">
        <Input value={draft.name} onChange={(e) => set('name', e.target.value)} />
      </Field>

      <Field label="Description" hint="What a guest reads on the room page. Plain sentences.">
        <Textarea
          rows={6}
          value={draft.description}
          onChange={(e) => set('description', e.target.value)}
        />
      </Field>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="View" hint="Optional — “over the garden”, “towards the mountains”.">
          <Input value={draft.view ?? ''} onChange={(e) => set('view', e.target.value)} />
        </Field>
        <Field label="Sleeps" hint="A capacity filter only, never a price input.">
          <Input
            type="number"
            min={1}
            value={draft.maxOccupancy}
            onChange={(e) => set('maxOccupancy', Number(e.target.value))}
          />
        </Field>
      </div>

      <Field label="Amenities" hint="One per line.">
        <Textarea
          rows={4}
          value={draft.amenities.join('\n')}
          onChange={(e) => set('amenities', e.target.value.split('\n'))}
        />
      </Field>

      <Beds draft={draft} set={set} />
      <Photos photos={draft.photos} onChange={(photos) => set('photos', photos)} />

      <Pets draft={draft} set={set} />
      <Accessibility draft={draft} set={set} />

      <div className="flex flex-wrap gap-2">
        <Button
          kind="primary"
          onClick={() => saving.run(async () => {
            await saveRoom(draft)
            onSaved()
          })}
          disabled={saving.working}
        >
          {saving.working ? 'Saving…' : 'Save this room'}
        </Button>
        <Button onClick={() => setOpen(false)}>Close</Button>
      </div>
    </Card>
  )
}

function Beds({
  draft,
  set,
}: {
  draft: RoomContent
  set: <K extends keyof RoomContent>(key: K, value: RoomContent[K]) => void
}) {
  const types = ['king', 'queen', 'full', 'twin', 'daybed', 'sofa_bed']

  return (
    <div className="flex flex-col gap-2">
      <span className="text-sm font-medium">Beds</span>
      {draft.beds.map((bed, i) => (
        <div key={i} className="flex flex-wrap gap-2">
          <select
            value={bed.type}
            onChange={(e) => {
              const beds = [...draft.beds]
              beds[i] = { ...bed, type: e.target.value }
              set('beds', beds)
            }}
            className="rounded-lg border border-neutral-300 bg-white px-3 py-3 text-sm"
          >
            {types.map((t) => (
              <option key={t} value={t}>
                {t.replace('_', ' ')}
              </option>
            ))}
          </select>
          <Input
            type="number"
            min={1}
            value={bed.count}
            onChange={(e) => {
              const beds = [...draft.beds]
              beds[i] = { ...bed, count: Number(e.target.value) }
              set('beds', beds)
            }}
            className="w-20"
          />
          <Input
            value={bed.location ?? ''}
            placeholder="where, if it matters"
            onChange={(e) => {
              const beds = [...draft.beds]
              beds[i] = { ...bed, location: e.target.value }
              set('beds', beds)
            }}
          />
          <Button onClick={() => set('beds', draft.beds.filter((_, j) => j !== i))}>Remove</Button>
        </div>
      ))}
      <Button
        onClick={() => set('beds', [...draft.beds, { type: 'queen', count: 1, location: '' }])}
      >
        Add a bed
      </Button>
    </div>
  )
}

function Pets({
  draft,
  set,
}: {
  draft: RoomContent
  set: <K extends keyof RoomContent>(key: K, value: RoomContent[K]) => void
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg bg-neutral-50 p-3">
      <label className="flex items-center gap-2 text-sm font-medium">
        <input
          type="checkbox"
          checked={draft.isPetFriendly}
          onChange={(e) => {
            set('isPetFriendly', e.target.checked)
            // A fee on a room that does not take pets could never be charged,
            // and the database refuses the combination outright — so clearing it
            // here is what stops the save failing on a rule the owner cannot see.
            if (!e.target.checked) set('petFeeCents', 0)
          }}
        />
        Takes pets
      </label>

      {draft.isPetFriendly && (
        <Field label="Pet fee per stay" hint="Taxed with the room charge, and refundable on the same terms.">
          <Input
            inputMode="decimal"
            value={centsToInput(draft.petFeeCents)}
            onChange={(e) => set('petFeeCents', inputToCents(e.target.value))}
            placeholder="50.00"
          />
        </Field>
      )}
    </div>
  )
}

/**
 * The accessibility flag, and the rule that guards it.
 *
 * The database will not let the flag be set without at least one named feature
 * (decision #22), because "accessible" is a promise a guest plans a trip around
 * and a wheelchair user who arrives to find three steps has been genuinely
 * harmed. Today no room at this inn sets it — every room needs stairs — and the
 * form says so rather than quietly offering a checkbox with no consequence.
 */
function Accessibility({
  draft,
  set,
}: {
  draft: RoomContent
  set: <K extends keyof RoomContent>(key: K, value: RoomContent[K]) => void
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg bg-neutral-50 p-3">
      <label className="flex items-center gap-2 text-sm font-medium">
        <input
          type="checkbox"
          checked={draft.isAccessible}
          onChange={(e) => set('isAccessible', e.target.checked)}
        />
        Accessible
      </label>

      <Aside>
        Only tick this if it is true of the room as a guest with mobility needs would mean it. Every
        room at Beal House currently requires stairs. Saying otherwise is a promise somebody plans a
        trip around.
      </Aside>

      {draft.isAccessible && (
        <Field
          label="What makes it accessible"
          hint="One per line — step-free entry, ground floor, roll-in shower, grab bars, wide doorway. At least one is required."
        >
          <Textarea
            rows={3}
            value={draft.accessibilityFeatures.join('\n')}
            onChange={(e) => set('accessibilityFeatures', e.target.value.split('\n'))}
          />
        </Field>
      )}
    </div>
  )
}
