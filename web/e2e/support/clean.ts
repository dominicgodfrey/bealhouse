import { Client } from 'pg'

import { windowBounds } from './window'

/**
 * Empties this suite's stretch of the calendar.
 *
 * Run as both globalSetup and globalTeardown. Teardown because a suite that
 * books rooms and leaves them booked takes them off sale for good, and the next
 * run finds nothing available — the failure would read as a bug in
 * availability. Setup for the same reason from the other end: a run killed
 * partway through leaves exactly that mess behind, and the next one should not
 * inherit it.
 *
 * Only inside the window in e2e/support/window.ts. A cleanup that truncated
 * `bookings` would take the Go suite's fixtures and anything the owner had been
 * clicking through with it, and the whole point of every package having its own
 * window is that nothing has to know about anybody else's rows.
 *
 * Deleting the booking is enough: `room_occupancy.booking_id` is ON DELETE
 * CASCADE, so the hold or the confirmed stay behind it goes with it and the
 * room is back on sale. The guest row is left — guests outlive bookings by
 * design, and one named "E2E Guest" is not in anybody's way.
 */
export default async function clean() {
  const client = new Client({
    connectionString:
      process.env.DATABASE_URL ??
      'postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable',
  })

  const { from, to } = windowBounds()

  try {
    await client.connect()
    const { rowCount } = await client.query(
      'DELETE FROM bookings WHERE checkin >= $1 AND checkin < $2',
      [from, to],
    )
    if (rowCount) {
      console.log(`e2e: cleared ${rowCount} booking(s) from ${from}–${to}`)
    }
  } catch (err) {
    // Loud, and not fatal. Without a database the webServer will fail to start
    // and every test will say so much more clearly than a connection error out
    // of a global hook that has not run a test yet.
    console.warn(`e2e: could not clear ${from}–${to}: ${(err as Error).message}`)
  } finally {
    await client.end().catch(() => {})
  }
}
