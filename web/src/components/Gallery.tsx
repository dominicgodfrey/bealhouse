import { useState } from 'react'

import { Photo } from './Photo'
import type { PhotoSources } from './Photo'

/** What Gallery needs of a photograph, whichever table it came out of. */
export type GalleryPhoto = { src: string; alt: string } & PhotoSources

/**
 * Page photographs carry `path` and room photographs carry `url`, which is a
 * wart on the API rather than a distinction worth anything. Both adapters live
 * here so the difference is dealt with in one file instead of at five call
 * sites, and the srcset fields ride along untouched.
 */
export function fromPagePhotos(
  photos: ({ path: string; alt: string } & PhotoSources)[],
): GalleryPhoto[] {
  return photos.map(({ path, ...rest }) => ({ src: path, ...rest }))
}

export function fromRoomPhotos(
  photos: ({ url: string; alt: string } & PhotoSources)[],
): GalleryPhoto[] {
  return photos.map(({ url, ...rest }) => ({ src: url, ...rest }))
}

/**
 * A set of photographs: one large, the rest as a rail beside it.
 *
 * One big picture is what somebody is actually looking at — a room, a street, a
 * plate — and an even grid of six makes all six small and none of them the
 * subject. The rail keeps the others reachable without spending the page on
 * them.
 *
 * **The rail is beside the main image on a wide screen and under it on a
 * phone.** A vertical strip of thumbnails next to a picture that is itself only
 * 340px wide leaves neither of them legible, which is the failure this layout
 * has if it is not allowed to reflow.
 *
 * Nothing renders for an empty list — the same rule the prose follows. A page
 * with no photographs shows its structure, not an empty frame.
 */
export function Gallery({
  photos,
  eager = false,
}: {
  photos: GalleryPhoto[]
  /** Set on the gallery that is the first thing on its page. */
  eager?: boolean
}) {
  const [shown, setShown] = useState(0)

  if (photos.length === 0) return null

  // Guarded rather than trusted: the list can shrink under this component when
  // the console saves, and an index past the end renders a broken image.
  const main = photos[Math.min(shown, photos.length - 1)]

  if (photos.length === 1) {
    return (
      <Photo
        src={main.src}
        alt={main.alt}
        sources={main}
        sizes="(min-width: 1024px) 1024px, 100vw"
        loading={eager ? 'eager' : 'lazy'}
        className="aspect-[16/9] w-full rounded-lg object-cover"
      />
    )
  }

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
      {/*
        The rail comes SECOND in the markup and is moved to the left visually
        at sm. In source order the big picture is the first thing a screen
        reader and a keyboard reach, which is the right order however it is
        arranged on screen.
      */}
      <Photo
        src={main.src}
        alt={main.alt}
        sources={main}
        // The main image is most of the width on a phone and about two thirds
        // of a wide layout once the rail is beside it.
        sizes="(min-width: 640px) 66vw, 100vw"
        loading={eager ? 'eager' : 'lazy'}
        className="aspect-[4/3] w-full rounded-lg object-cover sm:order-2 sm:flex-1"
      />

      <ul
        className={
          // Across the bottom on a phone, down the side from sm. Scrollable
          // rather than wrapping, so the main image never gets pushed off the
          // screen by a long list of thumbnails.
          'flex shrink-0 gap-2 overflow-x-auto pb-1 sm:order-1 sm:w-20 sm:flex-col sm:overflow-x-visible sm:overflow-y-auto sm:pb-0'
        }
      >
        {photos.map((photo, i) => {
          const current = i === Math.min(shown, photos.length - 1)
          return (
            <li key={i} className="shrink-0">
              <button
                type="button"
                onClick={() => setShown(i)}
                aria-label={photo.alt || `Photograph ${i + 1}`}
                aria-current={current}
                className={`block size-16 overflow-hidden rounded-md ring-2 transition sm:size-20 ${
                  current ? 'ring-neutral-900' : 'ring-transparent hover:ring-neutral-300'
                }`}
              >
                <Photo
                  src={photo.src}
                  // Empty: the button's aria-label already names it, and a
                  // screen reader reading both says everything twice.
                  alt=""
                  sources={photo}
                  sizes="80px"
                  className="size-full object-cover"
                />
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
