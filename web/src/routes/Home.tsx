import { Layout } from '../components/Layout'
import { SearchForm } from '../components/SearchForm'

/**
 * The home page, anchored on booking.
 *
 * The copy and photography are the owner's and arrive with build-order step 7.
 * What is load-bearing here is the search: it is the first thing on the page,
 * and its date picker already knows what can actually be sold.
 */
export function Home() {
  return (
    <Layout>
      <section className="flex flex-col gap-8">
        <div className="flex flex-col gap-3">
          <h1 className="text-4xl font-semibold tracking-tight">
            Seven rooms in the White Mountains
          </h1>
          <p className="max-w-prose text-neutral-600">
            Book directly with the inn. No booking fees, and the dates you can
            choose are the ones we can genuinely give you.
          </p>
        </div>

        <SearchForm alwaysOpen />
      </section>
    </Layout>
  )
}
