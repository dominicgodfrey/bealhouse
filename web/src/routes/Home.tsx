import { useEffect, useState } from 'react'
import { Link } from 'react-router'

import { fetchPageCopy, type PagePhoto } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { Layout } from '../components/Layout'
import { Photo } from '../components/Photo'
import { SearchForm } from '../components/SearchForm'

/**
 * The home page: one screenful, and it does not scroll.
 *
 * Header, search, the two other things the inn does above the footer, footer —
 * on a monitor and on a phone alike. The middle is deliberately empty because
 * what is behind it is the house. That constraint is the design: a page that
 * cannot scroll has to decide what it is for. Everything that used to sit below
 * the fold here is on /about now, and anything added comes out of the empty
 * middle rather than getting a scrollbar.
 */

/**
 * The drone footage that belongs behind all this. Set it to the uploaded file's
 * URL and the <video> below takes over from the slideshow, with the first
 * photograph as its poster.
 */
const backdropVideo = ''

/** How long each photograph holds before the next one fades up. */
const slideMs = 7000

export function Home() {
  // Two slots, one backdrop: the house, then the town from the local area page,
  // which is where those photographs are already managed. Either can be empty.
  const home = useAsync(() => fetchPageCopy('home'), [])
  const localArea = useAsync(() => fetchPageCopy('local-area'), [])

  // The house first — it is what somebody arriving at an inn's site wants.
  const photos = [...(home.data?.photos ?? []), ...(localArea.data?.photos ?? [])]

  return (
    <Layout
      fills
      backdrop={
        <>
          {/* What shows for the moment before the first photograph decodes.
              A white gap there reads as a broken page. */}
          <div className="absolute inset-0 bg-neutral-200" />

          {!backdropVideo && <Slideshow photos={photos} />}

          {backdropVideo && (
            // muted and playsInline are what make autoplay legal on iOS.
            <video
              className="absolute inset-0 size-full object-cover"
              src={backdropVideo}
              poster={photos[0]?.path}
              autoPlay
              muted
              loop
              playsInline
              aria-hidden="true"
            />
          )}
        </>
      }
    >
      {/* justify-between is what keeps the middle clear: search against the
          top, buttons against the bottom, the rest of the screen between. On a
          short phone the gap goes to nothing and both still fit. */}
      <div className="flex h-full flex-col justify-between gap-4 px-4 py-4 sm:px-6 sm:py-6">
        <div className="mx-auto w-full max-w-3xl">
          {/*
            A card, because this is dark type on a photograph otherwise, and
            translucent so it reads as something laid over the house.

            NO backdrop-blur, and that is load-bearing: `backdrop-filter` makes
            an element the containing block for `fixed` descendants, so the
            calendar would pin itself to this card instead of to the viewport.
          */}
          <div className="rounded-xl bg-sienna/90 p-4 shadow-lg sm:p-5">
            <SearchForm overlay />
          </div>
        </div>

        {/* The two things the inn does that are not a room. */}
        <div className="mx-auto grid w-full max-w-3xl grid-cols-2 gap-3">
          <Elsewhere to="/restaurant" name="The restaurant" says="What the kitchen is serving." />
          <Elsewhere to="/events" name="Events" says="Gatherings, and how to ask about one." />
        </div>
      </div>
    </Layout>
  )
}

/**
 * The backdrop: the house, then the town, cross-fading and looping.
 *
 * Every photograph is in the DOM at once and only opacity changes — a
 * compositor animation, and no blank frame while the next file decodes.
 * **One photograph starts no timer**, which is the state the site ships in.
 */
function Slideshow({ photos }: { photos: PagePhoto[] }) {
  const [shown, setShown] = useState(0)

  useEffect(() => {
    if (photos.length < 2) return
    // Less motion means no slideshow, not the same slideshow without the fade —
    // that would be a hard cut every seven seconds, which is worse.
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const timer = setInterval(() => setShown((i) => (i + 1) % photos.length), slideMs)
    return () => clearInterval(timer)
  }, [photos.length])

  return (
    <div aria-hidden="true">
      {photos.map((photo, i) => (
        <Photo
          key={photo.path}
          src={photo.path}
          alt=""
          sources={photo}
          sizes="100vw"
          // The rest are not needed for seven seconds.
          loading={i === 0 ? 'eager' : 'lazy'}
          className={`absolute inset-0 size-full object-cover transition-opacity duration-1000 ${
            i === shown ? 'opacity-100' : 'opacity-0'
          }`}
        />
      ))}
    </div>
  )
}

/**
 * One of the two buttons above the footer. The sentence is dropped on a phone
 * rather than clipped: the name is what somebody taps, and a truncated
 * half-sentence would cost the same 40 pixels and read as a bug.
 */
function Elsewhere({ to, name, says }: { to: string; name: string; says: string }) {
  return (
    <Link
      to={to}
      className="rounded-xl bg-sienna/90 px-4 py-3 text-center shadow-lg transition hover:bg-sienna"
    >
      <span className="block font-semibold tracking-tight">{name}</span>
      <span className="mt-0.5 hidden text-sm text-neutral-600 sm:block">{says}</span>
    </Link>
  )
}
