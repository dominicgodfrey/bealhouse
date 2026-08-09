import { Link } from 'react-router'

import { fetchPageCopy } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { Layout } from '../components/Layout'
import { Photo } from '../components/Photo'
import { SearchForm } from '../components/SearchForm'

/**
 * The home page: one screenful, and it does not scroll.
 *
 * Header at the top, the search under it, the two other things the inn does
 * just above the footer, and the footer on the bottom edge — on a desktop
 * monitor and on a phone alike. Everything between the search and those two
 * buttons is deliberately empty, because what is behind it is the house.
 *
 * That constraint is the whole design. A home page that scrolls has somewhere
 * to put a paragraph, and then somewhere to put another one; a page that cannot
 * scroll has to decide what it is for. This one is for booking a room and for
 * showing the building. Everything that used to live below the fold here — the
 * owner's story, the photographs of it, the way to write to the inn — is on
 * /about now, which is a page that can be as long as it needs to be.
 *
 * Nothing here is `overflow-auto` except the calendar, which floats over the
 * page rather than growing it (see SearchForm's `overlay`). If a control is
 * ever added to this screen, it comes out of the empty middle — it does not get
 * a scrollbar.
 */

/**
 * The looped footage that belongs behind all this: photographs of the house and
 * the drone footage over it.
 *
 * Empty until the owner supplies the file, and while it is empty the backdrop
 * is the winter photograph of the house they already have on the site. Set this
 * to the URL of the uploaded video — `/media/<name>.mp4` if it is served from
 * MEDIA_DIR like everything else — and the <video> below takes over, with the
 * same photograph as its poster so the first frame is never a black rectangle.
 *
 * A constant rather than a setting, for now, because there is exactly one video
 * and nobody has asked to change it from a phone. When that changes it belongs
 * in `page_copy`'s neighbourhood, not here.
 */
const backdropVideo = ''

export function Home() {
  const copy = useAsync(() => fetchPageCopy('home'), [])
  const photo = copy.data?.photos[0]

  return (
    <Layout
      fills
      backdrop={
        <>
          {/*
            neutral-200 underneath everything: the first paint, and what a
            visitor sees for the moment before the photograph decodes. A white
            gap here reads as a broken page.
          */}
          <div className="absolute inset-0 bg-neutral-200" />

          {photo && !backdropVideo && (
            <Photo
              src={photo.path}
              alt={photo.alt}
              sources={photo}
              // Full-bleed, so the browser should be choosing the largest rung
              // its screen can use rather than a card-sized one.
              sizes="100vw"
              // The one photograph on this site that is unambiguously above the
              // fold, because the page is nothing but above the fold.
              loading="eager"
              className="absolute inset-0 size-full object-cover"
            />
          )}

          {backdropVideo && (
            // muted and playsInline are what make autoplay legal on iOS; the
            // poster is the still it holds until enough of the file has
            // arrived, which on mountain mobile data is a while.
            <video
              className="absolute inset-0 size-full object-cover"
              src={backdropVideo}
              poster={photo?.path}
              autoPlay
              muted
              loop
              playsInline
              // It has no sound and says nothing a screen reader needs.
              aria-hidden="true"
            />
          )}
        </>
      }
    >
      {/*
        justify-between is what keeps the middle clear: the search sits against
        the top of this box and the two buttons against the bottom, and the gap
        between them is however much of the screen is left. On a short phone
        that gap goes to nothing and the two blocks meet — which is the correct
        way for this to degrade, since both of them still fit.
      */}
      <div className="flex h-full flex-col justify-between gap-4 px-4 py-4 sm:px-6 sm:py-6">
        <div className="mx-auto w-full max-w-3xl">
          {/*
            The search in a card of its own, because it is dark type on a
            photograph otherwise. bg-white/95 rather than solid: it should read
            as something laid over the house, not as a band cut out of it.

            NO backdrop-blur here, and that is load-bearing rather than taste:
            `backdrop-filter` makes an element the containing block for every
            `fixed` descendant, and the calendar pins itself to the bottom of
            the viewport on a screen with no room under the field. Blurred, this
            card would become "the viewport" and the calendar would hang off the
            card it was trying to escape. At 95% white the blur was invisible
            anyway.
          */}
          <div className="rounded-xl bg-white/95 p-4 shadow-lg sm:p-5">
            <SearchForm overlay />
          </div>
        </div>

        {/*
          The restaurant and the events, at the foot of the screen. They are the
          two things the inn does that are not a room, and this is the only page
          that has room to say so at all — so they are buttons rather than an
          explanation.
        */}
        <div className="mx-auto grid w-full max-w-3xl grid-cols-2 gap-3">
          <Elsewhere to="/restaurant" name="The restaurant" says="What the kitchen is serving." />
          <Elsewhere to="/events" name="Events" says="Gatherings, and how to ask about one." />
        </div>
      </div>
    </Layout>
  )
}

/**
 * One of the two buttons above the footer.
 *
 * The sentence under the name is hidden on a phone and not shortened for one.
 * Two lines of explanation each is 40-odd vertical pixels on the screen that
 * has the least of them, and the name of the thing is what somebody taps — a
 * clipped half-sentence would cost the same space and read as a bug.
 */
function Elsewhere({ to, name, says }: { to: string; name: string; says: string }) {
  return (
    <Link
      to={to}
      className="rounded-xl bg-white/95 px-4 py-3 text-center shadow-lg transition hover:bg-white"
    >
      <span className="block font-semibold tracking-tight">{name}</span>
      <span className="mt-0.5 hidden text-sm text-neutral-600 sm:block">{says}</span>
    </Link>
  )
}
