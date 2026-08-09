import type { Quote } from '../lib/api'
import { addDays, formatLong, formatShort } from '../lib/dates'
import { formatCents } from '../lib/money'

type Props = {
  quote: Quote
  nightlyCents: number[]
  checkin: string
  /** Shown on the confirm screen, where what happens next is the whole point. */
  payment?: { chargeNowCents: number; balanceChargeOn?: string }
}

/**
 * What the stay costs, itemised.
 *
 * The pet fee gets its own line because it is its own field all the way down —
 * decision #23 is that a guest can see exactly what the extra $50 is for rather
 * than finding a subtotal that does not match the nightly rate.
 */
export function PriceBreakdown({ quote, nightlyCents, checkin, payment }: Props) {
  const flat = nightlyCents.every((cents) => cents === nightlyCents[0])

  return (
    <div className="flex flex-col gap-2 text-sm">
      <Row
        label={
          flat && nightlyCents.length > 0
            ? `${formatCents(nightlyCents[0])} × ${quote.nights} ${quote.nights === 1 ? 'night' : 'nights'}`
            : `${quote.nights} nights`
        }
        value={formatCents(quote.roomSubtotalCents)}
      />

      {!flat && (
        <ul className="ml-4 flex flex-col gap-1 text-xs text-neutral-500">
          {nightlyCents.map((cents, i) => (
            <li key={i} className="flex justify-between">
              <span>{nightLabel(checkin, i)}</span>
              <span>{formatCents(cents)}</span>
            </li>
          ))}
        </ul>
      )}

      {quote.petFeeCents > 0 && (
        <Row label="Pet fee (per stay)" value={formatCents(quote.petFeeCents)} />
      )}

      <Row label="NH Meals & Rooms tax" value={formatCents(quote.taxCents)} />

      <div className="mt-1 border-t border-sienna-line pt-2">
        <Row label="Total" value={formatCents(quote.totalCents)} strong />
      </div>

      {payment && (
        <div className="mt-2 flex flex-col gap-2 rounded-lg bg-white/60 p-3">
          <Row label="Due at booking" value={formatCents(payment.chargeNowCents)} strong />
          {payment.balanceChargeOn ? (
            <p className="text-xs text-neutral-600">
              The remaining {formatCents(quote.totalCents - payment.chargeNowCents)} is charged
              automatically on {formatLong(payment.balanceChargeOn)}, seven days before you
              arrive.
            </p>
          ) : (
            <p className="text-xs text-neutral-600">
              Your stay begins soon, so it is paid in full at booking rather than split.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

function Row({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className={`flex justify-between ${strong ? 'font-medium' : 'text-neutral-600'}`}>
      <span>{label}</span>
      <span className={strong ? '' : 'text-neutral-900'}>{value}</span>
    </div>
  )
}

function nightLabel(checkin: string, offset: number): string {
  return formatShort(addDays(checkin, offset))
}
