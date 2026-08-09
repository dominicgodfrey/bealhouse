import { Link, useParams, useSearchParams } from 'react-router'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { describeBeds } from '../components/RoomCard'
import { PriceBreakdown } from '../components/PriceBreakdown'
import { fetchRoom } from '../lib/api'
import { formatLong } from '../lib/dates'
import { parseStay, staySearch } from '../lib/stay'
import { useAsync } from '../lib/useAsync'
import { Gallery, fromRoomPhotos } from '../components/Gallery'

/**
 * One room, with or without dates.
 *
 * Reached from search results it carries the stay through, so the book button
 * needs no re-entry. Reached cold — from a link, or eventually from the
 * marketing site — it describes the room and asks for dates.
 */
export function Room() {
  const { slug = '' } = useParams()
  const [params] = useSearchParams()
  const stay = parseStay(params)

  const room = useAsync(() => fetchRoom(slug, stay ?? undefined), [
    slug,
    stay?.checkin,
    stay?.checkout,
    stay?.guests,
    stay?.withPet,
  ])

  return (
    <Layout>
      {room.loading && <Loading what="the room" />}
      {room.error && <ErrorNote error={room.error} />}

      {room.data && (
        <article className="flex flex-col gap-8">
          {/*
            The header is centred with the rest of the site. What is below it is
            not: the facts are a definition list and the panel beside them is
            how the room gets booked, and both read worse centred.
          */}
          <header className="flex flex-col items-center gap-2 text-center">
            <h1 className="text-3xl font-semibold tracking-tight">{room.data.name}</h1>
            {room.data.view && <p className="text-neutral-600">{room.data.view}</p>}
          </header>

          {/*
            One large photograph with the rest as a rail, rather than an even
            grid: the room is what somebody is looking at, and four equal
            quarters make all four small and none of them the subject. The
            room's own photographs are what this page is for, so the first is
            not deferred.
          */}
          <Gallery
            photos={fromRoomPhotos(
              room.data.photos.length > 0
                ? room.data.photos
                : [{ url: room.data.placeholderPhotoUrl, alt: '' }],
            )}
            eager
          />

          {/*
            Two columns from lg, not sm. At 640px a 2fr/1fr split leaves the
            booking panel about 190px wide, which is not enough for a price
            breakdown and a button — the numbers wrapped mid-row and the panel
            was the most cramped thing on the page. Stacked below that, with
            the panel under the description where a thumb reaches it.
          */}
          <div className="grid gap-8 lg:grid-cols-[2fr_1fr]">
            <div className="flex flex-col gap-4">
              <p className="max-w-prose text-neutral-700">{room.data.description}</p>

              <dl className="flex flex-col gap-2 text-sm">
                <Fact label="Sleeps" value={String(room.data.maxOccupancy)} />
                <Fact label="Beds" value={describeBeds(room.data)} />
                {room.data.isPetFriendly && (
                  <Fact
                    label="Pets"
                    value="Welcome in this room, for a $50 fee covering the whole stay"
                  />
                )}
              </dl>

              {/*
                Chips rather than a comma-separated line. A room carries a dozen
                of these — heat, wifi, the toiletries, the jacuzzi where there is
                one — and thirteen things joined by commas is a paragraph
                somebody skims past instead of a list they can scan for the one
                they care about.
              */}
              {room.data.amenities.length > 0 && (
                <div className="flex flex-col gap-2">
                  <h2 className="text-sm text-neutral-500">In this room</h2>
                  <ul className="flex flex-wrap gap-2">
                    {room.data.amenities.map((amenity) => (
                      <li
                        key={amenity}
                        className="rounded-full border border-neutral-200 bg-neutral-50 px-3 py-1 text-sm text-neutral-700"
                      >
                        {amenity}
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              <p className="rounded-lg bg-neutral-50 px-4 py-3 text-sm text-neutral-600">
                {room.data.accessibilityNotice}
              </p>
            </div>

            <aside className="flex flex-col gap-3 rounded-lg border border-neutral-200 p-4">
              {!room.data.hasDates && (
                <>
                  <p className="text-sm text-neutral-600">
                    Choose your dates to see what this room costs.
                  </p>
                  <Link
                    to="/"
                    className="rounded-lg bg-neutral-900 px-4 py-2 text-center text-sm font-medium text-white hover:bg-neutral-700"
                  >
                    Check dates
                  </Link>
                </>
              )}

              {room.data.hasDates && !room.data.available && (
                <>
                  <p className="text-sm font-medium">Not available</p>
                  <p className="text-sm text-neutral-600">
                    This room is taken for {formatLong(room.data.checkin!)} to{' '}
                    {formatLong(room.data.checkout!)}, or the stay is shorter than its
                    minimum.
                  </p>
                  <Link
                    to={`/search?${staySearch(stay!)}`}
                    className="text-sm text-neutral-600 underline underline-offset-4"
                  >
                    See what is available
                  </Link>
                </>
              )}

              {room.data.hasDates && room.data.available && stay && (
                <>
                  <p className="text-sm text-neutral-600">
                    {formatLong(stay.checkin)} → {formatLong(stay.checkout)}
                  </p>
                  <PriceBreakdown
                    quote={room.data.quote}
                    nightlyCents={room.data.nightlyCents}
                    checkin={stay.checkin}
                  />
                  <Link
                    to={`/book/${room.data.slug}?${staySearch(stay)}`}
                    className="rounded-lg bg-neutral-900 px-4 py-2 text-center text-sm font-medium text-white hover:bg-neutral-700"
                  >
                    Book this room
                  </Link>
                </>
              )}
            </aside>
          </div>
        </article>
      )}
    </Layout>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-2">
      <dt className="w-24 shrink-0 text-neutral-500">{label}</dt>
      <dd>{value}</dd>
    </div>
  )
}
