import { Link } from 'react-router'

import { fetchPageCopy, fetchRoomCards, paragraphs, type RoomCard } from '../lib/site'
import { formatCentsShort } from '../lib/money'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'
import { Photo } from '../components/Photo'

/**
 * The seven rooms, without dates.
 *
 * This is the page somebody arrives at from a search engine before they have
 * decided when to come, so it describes the rooms and lets the visitor pick
 * dates on the room page or through the search. It deliberately does not ask
 * for dates first: a wall of form fields between a stranger and a photograph of
 * the room is how a direct booking becomes an OTA booking.
 */
export function Rooms() {
  const rooms = useAsync(fetchRoomCards, [])
  const copy = useAsync(() => fetchPageCopy('rooms'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-8">
        <div className="flex flex-col gap-3">
          <h1 className="text-4xl font-semibold tracking-tight">The rooms</h1>
          {copy.data && (
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          )}
        </div>

        {rooms.loading && <Loading what="the rooms" />}
        {rooms.error && <ErrorNote error={rooms.error} />}

        <div className="grid gap-6 sm:grid-cols-2">
          {rooms.data?.map((room) => <Card key={room.slug} room={room} />)}
        </div>
      </div>
    </Layout>
  )
}

function Card({ room }: { room: RoomCard }) {
  // The owner's own photograph when there is one, and the placeholder drawing
  // when there is not — never a broken image, and never a stock photo of
  // somebody else's inn.
  const photo = room.photos[0]

  return (
    <Link
      to={`/rooms/${room.slug}`}
      className="flex flex-col gap-3 rounded-lg border border-neutral-200 p-4 transition hover:border-neutral-400"
    >
      <Photo
        src={photo?.url ?? room.placeholderPhotoUrl}
        alt={photo?.alt ?? ''}
        sources={photo}
        // The grid is one column on a phone, two at sm, three at lg.
        sizes="(min-width: 1024px) 320px, (min-width: 640px) 45vw, 100vw"
        className="aspect-[4/3] w-full rounded object-cover"
      />

      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{room.name}</h2>
        {/*
          Absent rather than zero when the calendar prices no nights: such a room
          cannot be sold at all, and "from $0" is a promise the booking flow then
          refuses to honour.
        */}
        {room.fromCents !== undefined && (
          <span className="text-sm text-neutral-600">
            from {formatCentsShort(room.fromCents)} a night
          </span>
        )}
      </div>

      <p className="text-sm text-neutral-600">
        Sleeps {room.maxOccupancy}
        {room.view && ` · ${room.view}`}
        {room.isPetFriendly && ' · dogs welcome'}
      </p>

      {room.description && (
        <p className="line-clamp-3 text-sm text-neutral-700">{room.description}</p>
      )}
    </Link>
  )
}
