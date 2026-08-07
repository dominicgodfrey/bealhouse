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

        {menu.data && menu.data.length > 0 && <MenuJsonLd sections={menu.data} />}
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

/**
 * The structured menu, for search results (decision #12).
 *
 * Built from the same rows the page just rendered rather than from a second
 * description of the menu, so the two cannot disagree — which is the entire
 * reason the menu is structured data and not a paragraph.
 *
 * Injected from the client for now. Architecture decision #3 has the server
 * writing per-route JSON-LD into the document it serves, which is what a
 * crawler that does not run JavaScript needs; this is the same data in the
 * meantime rather than nothing.
 */
function MenuJsonLd({ sections }: { sections: MenuSection[] }) {
  const menu = {
    '@context': 'https://schema.org',
    '@type': 'Menu',
    hasMenuSection: sections.map((section) => ({
      '@type': 'MenuSection',
      name: section.name,
      description: section.description || undefined,
      hasMenuItem: section.items.map((item) => ({
        '@type': 'MenuItem',
        name: item.name,
        description: item.description || undefined,
        offers:
          item.priceCents > 0
            ? { '@type': 'Offer', price: (item.priceCents / 100).toFixed(2), priceCurrency: 'USD' }
            : undefined,
      })),
    })),
  }

  return (
    <script
      type="application/ld+json"
      // The content is the inn's own menu rows, serialised by JSON.stringify,
      // so there is no path from a visitor to what goes in here.
      dangerouslySetInnerHTML={{ __html: JSON.stringify(menu) }}
    />
  )
}
