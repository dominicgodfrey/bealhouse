// Money is integer cents on the wire, as it is everywhere else in this system.
// It becomes a string here, at the last possible moment, and never becomes a
// number with a decimal point on the way.

const dollars = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
})

const wholeDollars = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

/** '$1,234.56' */
export function formatCents(cents: number): string {
  return dollars.format(cents / 100)
}

/**
 * '$1,235', for prices that happen to be round.
 *
 * Falls back to the exact figure when there are cents to show, so a total is
 * never quietly rounded in front of the person paying it.
 */
export function formatCentsShort(cents: number): string {
  return cents % 100 === 0 ? wholeDollars.format(cents / 100) : formatCents(cents)
}
