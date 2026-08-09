import {
  fetchMenu,
  fetchPageCopy,
  paragraphs,
  type MenuSection,
  type PagePhoto,
} from '../lib/site'
import { formatCents } from '../lib/money'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'
import { DietBadges, DietKey } from '../components/Diet'
import { Photo } from '../components/Photo'

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

  const sides = split(copy.data?.photos ?? [])
  // The key explains marks against dishes, so it belongs to a menu that has
  // some. On a night with nothing up it would be a legend for an empty page.
  const hasMenu = (menu.data?.length ?? 0) > 0

  return (
    <Layout>
      <div className="flex flex-col gap-8">
        <div className="flex flex-col items-center gap-3 text-center">
          <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">The restaurant</h1>
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

        {/*
          The menu down the middle with the kitchen's photographs either side.

          Three columns only from lg, where there is genuinely room for a narrow
          menu and two flanking pictures. Below that the arrangement collapses to
          one column with the menu FIRST and the photographs after it — the menu
          is what somebody came to read, and on a phone a screenful of food
          photography before the first dish is the thing that buries it.

          The middle column is capped rather than fractional so the menu stays
          the same comfortable width whatever the viewport does around it.
        */}
        <div className="grid gap-8 lg:grid-cols-[1fr_minmax(0,26rem)_1fr] lg:items-start lg:gap-10">
          <SideColumn photos={sides.left} />

          {/*
            order-first below lg puts the menu above both photo columns. The
            photographs are the SAME elements either way — a second copy behind
            `lg:hidden` would still be fetched by the browser, so a phone would
            download four pictures it never shows.
          */}
          <div className="order-first flex flex-col gap-10 lg:order-none">
            {menu.data?.map((section) => <Course key={section.name} section={section} />)}
            {hasMenu && <DietKey />}
          </div>

          <SideColumn photos={sides.right} />
        </div>
      </div>
    </Layout>
  )
}

/**
 * Splits the gallery down the middle, alternating.
 *
 * Alternating rather than first-half/second-half so the two columns stay within
 * one photograph of each other however many there are, and so the order the
 * owner arranged them in still reads top-to-bottom down the page.
 */
function split(photos: PagePhoto[]) {
  return {
    left: photos.filter((_, i) => i % 2 === 0),
    right: photos.filter((_, i) => i % 2 === 1),
  }
}

/**
 * One flanking column of photographs — and, below lg, a two-across row of the
 * same pictures underneath the menu.
 */
function SideColumn({ photos }: { photos: PagePhoto[] }) {
  if (photos.length === 0) return null

  return (
    <div className="grid grid-cols-2 gap-4 lg:flex lg:flex-col lg:gap-6">
      {photos.map((photo, i) => (
        <Photo
          key={i}
          src={photo.path}
          alt={photo.alt}
          sources={photo}
          // A flanking column is roughly a quarter of a wide viewport; two
          // across is half a narrow one.
          sizes="(min-width: 1024px) 25vw, 50vw"
          className="aspect-[3/4] w-full rounded-lg object-cover"
        />
      ))}
    </div>
  )
}

function Course({ section }: { section: MenuSection }) {
  return (
    <section className="flex flex-col gap-4">
      {/*
        The course heading is centred with the rest of the page; the dishes
        below it are not. A name on the left and a price on the right is what a
        menu looks like, and centring that would make the prices unscannable.
      */}
      <div className="flex flex-col items-center gap-1 border-b border-sienna-line pb-2 text-center">
        <h2 className="text-2xl font-semibold tracking-tight">{section.name}</h2>
        {section.description && (
          <p className="text-sm text-neutral-600">{section.description}</p>
        )}
      </div>

      <ul className="flex flex-col gap-4">
        {section.items.map((item) => (
          <li key={item.name} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-4">
              <h3 className="font-medium">
                {item.name} <DietBadges item={item} />
              </h3>
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
