import { Link } from 'react-router'

import type { Room, Stay } from '../lib/api'
import { formatCents, formatCentsShort } from '../lib/money'
import { staySearch } from '../lib/stay'
import { Photo } from './Photo'

/**
 * One result.
 *
 * Everything here comes from the API, including the photo fallback: rooms have
 * no uploaded photos until the owner adds them in admin, and a placeholder that
 * lives in the database is one somebody has to remember to delete.
 */
export function RoomCard({ room, stay }: { room: Room; stay: Stay }) {
  const photo = room.photos[0]
  const query = staySearch(stay)

  return (
    <article className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-4 sm:flex-row">
      <Photo
        src={photo?.url ?? room.placeholderPhotoUrl}
        alt={photo?.alt ?? ''}
        sources={photo}
        // Full width on a phone, a fixed 224px thumbnail once the card goes
        // side by side.
        sizes="(min-width: 640px) 224px, 100vw"
        className="h-40 w-full rounded object-cover sm:w-56"
      />

      <div className="flex flex-1 flex-col gap-2">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-lg font-medium">
              <Link to={`/rooms/${room.slug}?${query}`} className="hover:underline">
                {room.name}
              </Link>
            </h2>
            {room.view && <p className="text-sm text-neutral-500">{room.view}</p>}
          </div>

          <div className="text-right">
            <p className="text-lg font-medium">{formatCentsShort(room.quote.totalCents)}</p>
            <p className="text-xs text-neutral-500">
              total incl. {formatCents(room.quote.taxCents)} tax
            </p>
          </div>
        </div>

        <p className="text-sm text-neutral-600">
          Sleeps {room.maxOccupancy} · {describeBeds(room)}
          {room.isPetFriendly && ' · Pets welcome'}
        </p>

        {room.quote.petFeeCents > 0 && (
          <p className="text-sm text-neutral-600">
            Includes a {formatCents(room.quote.petFeeCents)} pet fee for the stay.
          </p>
        )}

        {room.amenities.length > 0 && (
          <p className="text-sm text-neutral-600">{room.amenities.join(' · ')}</p>
        )}

        <div className="mt-auto flex items-center gap-3 pt-2">
          <Link
            to={`/book/${room.slug}?${query}`}
            className="rounded-lg bg-neutral-900 px-4 py-2 text-sm font-medium text-white hover:bg-neutral-700"
          >
            Book this room
          </Link>
          <Link
            to={`/rooms/${room.slug}?${query}`}
            className="text-sm text-neutral-600 underline underline-offset-4"
          >
            See the room
          </Link>
        </div>
      </div>
    </article>
  )
}

export function describeBeds(room: Pick<Room, 'beds'>): string {
  if (room.beds.length === 0) return 'Bed details to come'

  return room.beds
    .map((bed) => {
      const name = bed.type.replace('_', ' ')
      const label = bed.count > 1 ? `${bed.count} ${name} beds` : `${name} bed`
      return bed.location ? `${label} (${bed.location})` : label
    })
    .join(', ')
}
