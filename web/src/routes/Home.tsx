import { Link } from 'react-router'

import { fetchPageCopy, fetchRoomCards, paragraphs } from '../lib/site'
import { formatCentsShort } from '../lib/money'
import { useAsync } from '../lib/useAsync'
import { Layout, Prose } from '../components/Layout'
import { SearchForm } from '../components/SearchForm'
import { Photo } from '../components/Photo'

/**
 * The home page, anchored on booking.
 *
 * The search is first and stays first: it is the one thing on this site that
 * makes money, and its date picker already knows what can genuinely be sold.
 * Everything below it is the owner's — their words from the console, their
 * rooms from the database — so this file describes a shape rather than an inn.
 */
export function Home() {
  const copy = useAsync(() => fetchPageCopy('home'), [])
  const rooms = useAsync(fetchRoomCards, [])

  return (
    <Layout>
      <div className="flex flex-col gap-14">
        <section className="flex flex-col gap-8">
          <div className="flex flex-col gap-3">
            <h1 className="text-4xl font-semibold tracking-tight">
              Seven rooms in the White Mountains
            </h1>
            <p className="max-w-prose text-neutral-600">
              Book directly with the inn. No booking fees, and the dates you can choose are the
              ones we can genuinely give you.
            </p>
          </div>

          <SearchForm alwaysOpen />
        </section>

        {copy.data?.written && (
          <section>
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          </section>
        )}

        {rooms.data && rooms.data.length > 0 && (
          <section className="flex flex-col gap-6">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h2 className="text-2xl font-semibold tracking-tight">The rooms</h2>
              <Link to="/rooms" className="text-sm text-neutral-600 underline">
                All seven →
              </Link>
            </div>

            {/*
              Three of the seven, not all of them. This page's job is to get
              somebody to the search or to a room, and a full grid here makes the
              rooms page pointless while pushing the search off the screen.
            */}
            <div className="grid gap-6 sm:grid-cols-3">
              {rooms.data.slice(0, 3).map((room) => (
                <Link
                  key={room.slug}
                  to={`/rooms/${room.slug}`}
                  className="flex flex-col gap-2 rounded-lg border border-neutral-200 p-3 transition hover:border-neutral-400"
                >
                  <Photo
                    src={room.photos[0]?.url ?? room.placeholderPhotoUrl}
                    alt={room.photos[0]?.alt ?? ''}
                    sources={room.photos[0]}
                    sizes="(min-width: 640px) 30vw, 100vw"
                    className="aspect-[4/3] w-full rounded object-cover"
                  />
                  <span className="font-medium">{room.name}</span>
                  {room.fromCents !== undefined && (
                    <span className="text-sm text-neutral-600">
                      from {formatCentsShort(room.fromCents)} a night
                    </span>
                  )}
                </Link>
              ))}
            </div>
          </section>
        )}

        <section className="grid gap-6 sm:grid-cols-2">
          <Link
            to="/restaurant"
            className="rounded-lg border border-neutral-200 p-5 transition hover:border-neutral-400"
          >
            <h2 className="text-lg font-semibold tracking-tight">The restaurant</h2>
            <p className="mt-1 text-sm text-neutral-600">
              What the kitchen is serving, updated as it changes.
            </p>
          </Link>
          <Link
            to="/events"
            className="rounded-lg border border-neutral-200 p-5 transition hover:border-neutral-400"
          >
            <h2 className="text-lg font-semibold tracking-tight">Events</h2>
            <p className="mt-1 text-sm text-neutral-600">
              Gatherings at the inn, and how to ask about one.
            </p>
          </Link>
        </section>
      </div>
    </Layout>
  )
}
