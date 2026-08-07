import { useState } from 'react'

import { fetchCopy, savePageCopy, type PageCopy } from '../../lib/console'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Aside, Button, Card, Field, Input, Saved, Screen, Textarea, useReload, useSaving } from './ui'

/**
 * The prose on the public pages.
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
 */
export function PageCopyEditor() {
  const [nonce, reload] = useReload()
  const pages = useAsync(fetchCopy, [nonce])

  return (
    <Screen title="Page copy" subtitle="What the site says about the inn.">
      {pages.loading && <Loading what="the pages" />}
      {pages.error && <ErrorNote error={pages.error} />}

      <Aside>
        A blank heading and a blank body empties the page again — the section simply does not
        render, rather than showing an empty box to a visitor.
      </Aside>

      {pages.data?.map((page) => <Page key={page.slug} page={page} onSaved={reload} />)}
    </Screen>
  )
}

const titles: Record<string, string> = {
  home: 'Home',
  rooms: 'The rooms page',
  restaurant: 'The restaurant',
  events: 'Events',
  about: 'About the inn',
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

      <Button
        kind="primary"
        onClick={() =>
          saving.run(async () => {
            await savePageCopy(draft)
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
