import { useState } from 'react'

import {
  fetchAttractions,
  fetchCopy,
  saveAttractions,
  savePageCopy,
  savePagePhotos,
  type Attraction,
  type PageCopy,
} from '../../lib/console'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Photos } from './Photos'
import { Aside, Button, Card, Field, Input, Saved, Screen, Textarea, useReload, useSaving } from './ui'

/**
 * The words and the photographs on the public pages.
 *
 * On exactly the terms the email copy is held: a row is an override, and no row
 * means the page renders its structure with nothing in that slot. Nothing here
 * ships with words in it, because the words are the owner's — and a placeholder
 * paragraph about the inn is one that would still be on the public internet a
 * year later.
 *
 * Plain text, not markdown: blank lines separate paragraphs and that is the
 * whole of the formatting. A rich editor would mean either a parser in this
 * bundle or a way to put a <script> on the public site from a phone.
 *
 * The photographs use the same editor a room's do, so the alt-text rule has one
 * implementation and not two. They save on the same button but in a second
 * request, because on the server the prose and the gallery are separate rows
 * and emptying one must not take the other with it.
 */
export function PageCopyEditor() {
  const [nonce, reload] = useReload()
  const pages = useAsync(fetchCopy, [nonce])

  return (
    <Screen title="Pages" subtitle="What the site says and shows about the inn.">
      {pages.loading && <Loading what="the pages" />}
      {pages.error && <ErrorNote error={pages.error} />}

      <Aside>
        A blank heading and a blank body empties the page again — the section simply does not
        render, rather than showing an empty box to a visitor. The same goes for the photographs.
      </Aside>

      {pages.data?.map((page) => <Page key={page.slug} page={page} onSaved={reload} />)}

      <Nearby />
    </Screen>
  )
}

/**
 * The local-area page's nearby list.
 *
 * It sits on this screen rather than getting one of its own because it is part
 * of a page the owner edits here — but it is its own table and its own save,
 * because each entry has three fields and one of them is a link. Flattening
 * that into the prose box is what made the list unlinkable in the first place.
 */
function Nearby() {
  const { refresh } = useConsole()
  const loaded = useAsync(fetchAttractions, [])
  const [draft, setDraft] = useState<Attraction[] | null>(null)
  const saving = useSaving(refresh)

  const list = draft ?? loaded.data ?? []

  function set(index: number, next: Attraction) {
    setDraft(list.map((a, i) => (i === index ? next : a)))
    saving.clear()
  }

  function move(from: number, by: number) {
    const to = from + by
    if (to < 0 || to >= list.length) return
    const next = [...list]
    ;[next[from], next[to]] = [next[to], next[from]]
    setDraft(next)
    saving.clear()
  }

  return (
    <Card>
      <div className="flex flex-col gap-1">
        <span className="font-medium">Nearby highlights</span>
        <span className="text-sm text-neutral-500">
          The list on the local area page. A link is optional — leave it empty and the name shows
          as plain text rather than as a link going nowhere.
        </span>
      </div>

      {loaded.loading && <Loading what="the list" />}
      {loaded.error && <ErrorNote error={loaded.error} />}
      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved. The page shows it now.</Saved>}

      {list.map((place, i) => (
        <div
          key={i}
          className="flex flex-col gap-2 rounded-lg border border-neutral-200 p-3 sm:flex-row sm:items-center"
        >
          <Input
            value={place.name}
            placeholder="Franconia Notch State Park"
            onChange={(e) => set(i, { ...place, name: e.target.value })}
          />
          <Input
            value={place.distance}
            placeholder="15 minutes away"
            onChange={(e) => set(i, { ...place, distance: e.target.value })}
            className="sm:w-48"
          />
          <Input
            value={place.url}
            placeholder="https://… (optional)"
            onChange={(e) => set(i, { ...place, url: e.target.value })}
          />
          <div className="flex gap-2">
            <Button onClick={() => move(i, -1)}>↑</Button>
            <Button onClick={() => move(i, 1)}>↓</Button>
            <Button
              onClick={() => {
                setDraft(list.filter((_, j) => j !== i))
                saving.clear()
              }}
            >
              Remove
            </Button>
          </div>
        </div>
      ))}

      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() => {
            setDraft([...list, { name: '', distance: '', url: '' }])
            saving.clear()
          }}
        >
          Add a place
        </Button>
        <Button
          kind="primary"
          onClick={() => saving.run(() => saveAttractions(list))}
          disabled={saving.working}
        >
          {saving.working ? 'Saving…' : 'Save the list'}
        </Button>
      </div>
    </Card>
  )
}

const titles: Record<string, string> = {
  home: 'Home',
  rooms: 'The rooms page',
  restaurant: 'The restaurant',
  events: 'Events',
  'local-area': 'The local area',
  policies: 'Policies',
}

function Page({ page, onSaved }: { page: PageCopy; onSaved: () => void }) {
  const { refresh } = useConsole()
  const [draft, setDraft] = useState(page)
  const saving = useSaving(refresh)

  return (
    <Card>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="font-medium">{titles[page.slug] ?? page.slug}</span>
        {!page.written && (
          <span className="rounded bg-neutral-200 px-2 py-0.5 text-xs text-neutral-700">
            nothing written
          </span>
        )}
      </div>

      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved. The page shows it now.</Saved>}

      <Field label="Heading">
        <Input
          value={draft.heading}
          onChange={(e) => {
            setDraft({ ...draft, heading: e.target.value })
            saving.clear()
          }}
        />
      </Field>

      <Field label="Words" hint="Leave a blank line between paragraphs.">
        <Textarea
          rows={8}
          value={draft.body}
          onChange={(e) => {
            setDraft({ ...draft, body: e.target.value })
            saving.clear()
          }}
        />
      </Field>

      <Photos
        photos={draft.photos}
        onChange={(photos) => {
          setDraft({ ...draft, photos })
          saving.clear()
        }}
      />

      <Button
        kind="primary"
        onClick={() =>
          saving.run(async () => {
            // Two requests, one button. The photographs go second so a page
            // being emptied of prose — which deletes its row — still ends with
            // the gallery the owner left in place.
            await savePageCopy(draft)
            await savePagePhotos(draft.slug, draft.photos)
            onSaved()
          })
        }
        disabled={saving.working}
      >
        {saving.working ? 'Saving…' : 'Save'}
      </Button>
    </Card>
  )
}
