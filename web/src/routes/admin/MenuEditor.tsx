import { useEffect, useState } from 'react'

import { fetchMenu, saveMenu, type MenuItem, type MenuSection } from '../../lib/console'
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
  Saved,
  Screen,
  Textarea,
  centsToInput,
  inputToCents,
  useSaving,
} from './ui'

/**
 * The menu editor (decision #12).
 *
 * Structured rather than a slab of text, because the same rows render the
 * restaurant page and the JSON-LD `Menu` that puts the inn in a search result —
 * and a price the machine can read has to be a number, not a line of prose.
 *
 * The whole menu saves as one document. That is how a menu is edited: courses
 * reordered, dishes moved between them, a whole section repriced in one sitting.
 * Reconciling that as a stream of per-row edits would be a diff algorithm here
 * whose failure mode is a half-applied menu on the public site; this way the
 * failure mode is the previous menu, unchanged.
 */
export function MenuEditor() {
  const { refresh } = useConsole()
  const loaded = useAsync(fetchMenu, [])
  const saving = useSaving(refresh)

  const [sections, setSections] = useState<MenuSection[] | null>(null)

  // The loader owns the first value and the editor owns every value after it.
  // Without this the draft is thrown away each time the async hook settles.
  useEffect(() => {
    if (loaded.data) setSections(loaded.data)
  }, [loaded.data])

  function update(next: MenuSection[]) {
    setSections(next)
    saving.clear()
  }

  return (
    <Screen title="Menu" subtitle="What the kitchen is serving.">
      {loaded.loading && <Loading what="the menu" />}
      {loaded.error && <ErrorNote error={loaded.error} />}
      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved. The restaurant page shows it now.</Saved>}

      <Aside>
        Turning a dish <em>off</em> keeps its description and its place for tomorrow; guests simply
        do not see it tonight. Leave a price empty for anything at market price or included in a
        set menu.
      </Aside>

      {sections?.length === 0 && (
        <Empty>Nothing on the menu yet. Add a course to start.</Empty>
      )}

      {sections?.map((section, si) => (
        <Section
          key={si}
          section={section}
          onChange={(next) => update(sections.map((s, i) => (i === si ? next : s)))}
          onRemove={() => update(sections.filter((_, i) => i !== si))}
          onMove={(by) => {
            const next = [...sections]
            const target = si + by
            if (target < 0 || target >= next.length) return
            ;[next[si], next[target]] = [next[target], next[si]]
            update(next)
          }}
        />
      ))}

      {sections && (
        <div className="flex flex-wrap gap-2">
          <Button
            onClick={() => update([...sections, { name: '', description: '', items: [] }])}
          >
            Add a course
          </Button>
          <Button
            kind="primary"
            onClick={() => saving.run(() => saveMenu(sections))}
            disabled={saving.working}
          >
            {saving.working ? 'Saving…' : 'Publish the menu'}
          </Button>
        </div>
      )}
    </Screen>
  )
}

function Section({
  section,
  onChange,
  onRemove,
  onMove,
}: {
  section: MenuSection
  onChange: (section: MenuSection) => void
  onRemove: () => void
  onMove: (by: number) => void
}) {
  function setItem(index: number, item: MenuItem) {
    onChange({ ...section, items: section.items.map((it, i) => (i === index ? item : it)) })
  }

  return (
    <Card>
      <Field label="Course">
        <Input
          value={section.name}
          placeholder="Starters"
          onChange={(e) => onChange({ ...section, name: e.target.value })}
        />
      </Field>
      <Field label="A line under it" hint="Optional. “Served until 3pm”, and so on.">
        <Input
          value={section.description}
          onChange={(e) => onChange({ ...section, description: e.target.value })}
        />
      </Field>

      {section.items.map((item, i) => (
        <div key={i} className="flex flex-col gap-2 rounded-lg border border-neutral-200 p-3">
          <Input
            value={item.name}
            placeholder="Dish"
            onChange={(e) => setItem(i, { ...item, name: e.target.value })}
          />
          <Textarea
            rows={2}
            value={item.description}
            placeholder="What is in it"
            onChange={(e) => setItem(i, { ...item, description: e.target.value })}
          />
          <div className="flex flex-wrap items-center gap-3">
            <Input
              inputMode="decimal"
              value={centsToInput(item.priceCents)}
              placeholder="—"
              onChange={(e) => setItem(i, { ...item, priceCents: inputToCents(e.target.value) })}
              className="w-28"
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={item.available}
                onChange={(e) => setItem(i, { ...item, available: e.target.checked })}
              />
              On tonight
            </label>
            <Button
              onClick={() =>
                onChange({ ...section, items: section.items.filter((_, j) => j !== i) })
              }
            >
              Remove
            </Button>
          </div>
        </div>
      ))}

      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() =>
            onChange({
              ...section,
              items: [
                ...section.items,
                { name: '', description: '', priceCents: 0, available: true },
              ],
            })
          }
        >
          Add a dish
        </Button>
        <Button onClick={() => onMove(-1)}>↑</Button>
        <Button onClick={() => onMove(1)}>↓</Button>
        <Button onClick={onRemove}>Remove the course</Button>
      </div>
    </Card>
  )
}
