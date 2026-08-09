import { useSearchParams } from 'react-router'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { RoomCard } from '../components/RoomCard'
import { SearchForm } from '../components/SearchForm'
import { searchRooms } from '../lib/api'
import { formatLong } from '../lib/dates'
import { parseStay } from '../lib/stay'
import { useAsync } from '../lib/useAsync'

export function Search() {
  const [params] = useSearchParams()
  const stay = parseStay(params)

  return (
    <Layout>
      <div className="flex flex-col gap-8">
        <SearchForm initial={stay ?? undefined} />
        {stay ? <Results stay={stay} /> : <p className="text-neutral-600">Choose your dates to see what is available.</p>}
      </div>
    </Layout>
  )
}

function Results({ stay }: { stay: NonNullable<ReturnType<typeof parseStay>> }) {
  const search = useAsync(() => searchRooms(stay), [
    stay.checkin,
    stay.checkout,
    stay.guests,
    stay.withPet,
  ])

  if (search.loading) return <Loading what="rooms" />
  if (search.error) return <ErrorNote error={search.error} />
  if (!search.data) return null

  const { rooms, nights, accessibilityNotice } = search.data

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">
          {rooms.length === 0
            ? 'Nothing available for those dates'
            : `${rooms.length} ${rooms.length === 1 ? 'room' : 'rooms'} available`}
        </h1>
        <p className="text-sm text-neutral-600">
          {formatLong(stay.checkin)} → {formatLong(stay.checkout)} · {nights}{' '}
          {nights === 1 ? 'night' : 'nights'} · {stay.guests}{' '}
          {stay.guests === 1 ? 'guest' : 'guests'}
          {stay.withPet && ' · with a pet'}
        </p>
      </div>

      {rooms.length === 0 && (
        <p className="text-neutral-600">
          Try different dates, or fewer guests. Every room here sleeps at least two.
        </p>
      )}

      {rooms.map((room) => (
        <RoomCard key={room.slug} room={room} stay={stay} />
      ))}

      {/* Shown with every search, not tucked into a room page: a guest with
          mobility needs should learn this here rather than on arrival. */}
      {accessibilityNotice && (
        <p className="rounded-lg border border-sienna-line bg-sienna px-4 py-3 text-sm text-neutral-600">
          {accessibilityNotice}
        </p>
      )}
    </section>
  )
}
