import { useRef, useState } from 'react'

import { uploadPhoto, type Photo } from '../../lib/console'
import { ErrorNote } from '../../components/Layout'
import { useConsole } from './Console'
import { Button, report } from './ui'

/**
 * Photographs for a room or an event: upload, describe, reorder, remove.
 *
 * One component for both, because they are the same job and a second copy would
 * be a second place for the alt-text rule to be forgotten.
 *
 * **The order here is the order stored.** `sort_order` is the position in this
 * list at save time and never a number anybody types, so what the owner sees is
 * what the page shows.
 *
 * **Alt text is required**, and the save refuses without it. A photograph with
 * none is invisible to somebody using a screen reader, which on a page whose
 * whole content is photographs means the page is empty. The box says what to
 * write rather than just demanding something.
 */
export function Photos({
  photos,
  onChange,
}: {
  photos: Photo[]
  onChange: (photos: Photo[]) => void
}) {
  const { refresh } = useConsole()
  const picker = useRef<HTMLInputElement>(null)

  const [uploading, setUploading] = useState(0)
  const [error, setError] = useState<Error | null>(null)

  async function add(files: FileList | null) {
    if (!files?.length) return

    setError(null)
    setUploading(files.length)

    // Sequential rather than parallel. A phone on a bad connection sending six
    // photographs at once finishes none of them, and the owner watching cannot
    // tell a slow upload from a stuck one.
    const added: Photo[] = []
    try {
      for (const file of Array.from(files)) {
        const { path } = await uploadPhoto(file)
        added.push({ path, alt: '' })
        setUploading((n) => n - 1)
      }
    } catch (err) {
      report(err, refresh, setError)
    } finally {
      setUploading(0)
      // Whatever made it up is kept, so a failure halfway through six does not
      // discard the five that worked.
      if (added.length) onChange([...photos, ...added])
      // Cleared so choosing the same file again still fires a change event.
      if (picker.current) picker.current.value = ''
    }
  }

  function move(from: number, by: number) {
    const to = from + by
    if (to < 0 || to >= photos.length) return
    const next = [...photos]
    ;[next[from], next[to]] = [next[to], next[from]]
    onChange(next)
  }

  function set(index: number, photo: Photo) {
    onChange(photos.map((p, i) => (i === index ? photo : p)))
  }

  const missingAlt = photos.some((p) => !p.alt.trim())

  return (
    <div className="flex flex-col gap-3">
      <span className="text-sm font-medium">Photographs</span>

      {error && <ErrorNote error={error} />}

      {photos.length === 0 && (
        <p className="rounded-lg border border-dashed border-neutral-300 px-4 py-6 text-center text-sm text-neutral-500">
          No photographs yet. The site shows a plain drawing of the house until there are.
        </p>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        {photos.map((photo, i) => (
          <div key={photo.path + i} className="flex flex-col gap-2 rounded-lg border border-neutral-200 p-3">
            <img
              src={photo.path}
              alt={photo.alt}
              className="aspect-[4/3] w-full rounded bg-neutral-100 object-cover"
            />

            <textarea
              rows={2}
              value={photo.alt}
              onChange={(e) => set(i, { ...photo, alt: e.target.value })}
              placeholder="What is in the picture, for somebody who cannot see it"
              className={`rounded-lg border px-3 py-2 text-sm ${
                photo.alt.trim() ? 'border-neutral-300' : 'border-amber-400 bg-amber-50'
              }`}
            />

            <div className="flex flex-wrap gap-2">
              <Button onClick={() => move(i, -1)}>←</Button>
              <Button onClick={() => move(i, 1)}>→</Button>
              <Button onClick={() => onChange(photos.filter((_, j) => j !== i))}>Remove</Button>
              {i === 0 && <span className="self-center text-xs text-neutral-500">shown first</span>}
            </div>
          </div>
        ))}
      </div>

      {missingAlt && (
        <p className="text-sm text-amber-800">
          Every photograph needs a description before this can be saved.
        </p>
      )}

      {/*
        The input is hidden and driven by the button, because a bare file input
        cannot be styled to look like anything and reads as "Choose File / no
        file chosen" — which on a phone is not obviously the way to add a photo.
      */}
      <input
        ref={picker}
        type="file"
        accept="image/*"
        multiple
        onChange={(e) => add(e.target.files)}
        className="hidden"
      />

      <Button onClick={() => picker.current?.click()} disabled={uploading > 0}>
        {uploading > 0 ? `Uploading ${uploading}…` : 'Add photographs'}
      </Button>

      <p className="text-xs text-neutral-500">
        Straight off a phone is fine — large pictures are scaled down as they are stored. They
        appear on the site when you save.
      </p>
    </div>
  )
}
