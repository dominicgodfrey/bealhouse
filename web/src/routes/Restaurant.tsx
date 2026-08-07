import { fetchMenu, fetchPageCopy, paragraphs, type MenuSection } from '../lib/site'
import { formatCents } from '../lib/money'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'

/**
 * The restaurant, and tonight's menu.
 *
 * The menu is live from the console rather than a PDF, which is the point of
 * decision #12: the owner turning a sold-out dish off at six o'clock changes
 * this page at six o'clock, and the same rows become the structured `Menu` that
 * search engines read.
 *
 * A menu nobody has entered yet renders as a sentence saying so, not as an
 * invented list of dishes. This inn's food is not ours to describe.
 */
export function Restaurant() {
  const menu = useAsync(fetchMenu, [])
  const copy = useAsync(() => fetchPageCopy('restaurant'), [])

  return (
    <Layout>
      <div className="flex flex-col gap-8">
        <div className="flex flex-col gap-3">
          <h1 className="text-4xl font-semibold tracking-tight">The restaurant</h1>
          {copy.data && (
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          )}
        </div>

        {menu.loading && <Loading what="the menu" />}
        {menu.error && <ErrorNote error={menu.error} />}

        {menu.data?.length === 0 && (
          <p className="rounded-lg border border-dashed border-neutral-300 px-6 py-10 text-center text-neutral-500">
            The menu changes with what the kitchen has, and tonight’s is not up yet. Please call the
            inn.
          </p>
        )}

        <div className="flex flex-col gap-10">
          {menu.data?.map((section) => <Course key={section.name} section={section} />)}
        </div>
      </div>
    </Layout>
  )
}

function Course({ section }: { section: MenuSection }) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1 border-b border-neutral-200 pb-2">
        <h2 className="text-2xl font-semibold tracking-tight">{section.name}</h2>
        {section.description && (
          <p className="text-sm text-neutral-600">{section.description}</p>
        )}
      </div>

      <ul className="flex flex-col gap-4">
        {section.items.map((item) => (
          <li key={item.name} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-4">
              <h3 className="font-medium">{item.name}</h3>
              {/*
                Zero means the dish carries no price of its own — market price,
                or a side inside a set menu — so nothing is printed rather than
                "$0.00".
              */}
              {item.priceCents > 0 && (
                <span className="text-neutral-600 tabular-nums">
                  {formatCents(item.priceCents)}
                </span>
              )}
            </div>
            {item.description && (
              <p className="max-w-prose text-sm text-neutral-600">{item.description}</p>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

/*
 * The structured `Menu` for search results (decision #12) used to be built here
 * and injected from the client. It is written by the server now — decision #3,
 * internal/httpx/meta.go — from the same rows this page renders, which is what
 * a crawler that does not run JavaScript needs and this never was.
 */
