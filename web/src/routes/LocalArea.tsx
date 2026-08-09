import { fetchAttractions, fetchPageCopy, paragraphs, type Attraction } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { Gallery, fromPagePhotos } from '../components/Gallery'
import { Layout, Loading, Prose } from '../components/Layout'

/**
 * Littleton, and what there is to do in it.
 *
 * This replaced the About page. The owner's story moved to the bottom of the
 * home page, where somebody deciding whether to book actually reads it, and the
 * standalone page became the question a guest is really asking — what is around
 * the inn.
 *
 * Two sources, deliberately. The prose is `page_copy` like every other page; the
 * nearby list is `local_attractions`, because each entry has a name, a distance
 * and a link, and three fields do not survive being flattened into a paragraph
 * of plain text. That split is also what lets "Nearby Highlights" be a real
 * heading rather than a line of body copy that merely looks like one.
 */
export function LocalArea() {
  const copy = useAsync(() => fetchPageCopy('local-area'), [])
  const nearby = useAsync(fetchAttractions, [])

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <h1 className="text-center text-3xl font-semibold tracking-tight sm:text-4xl">The local area</h1>

        {copy.loading && <Loading what="the page" />}

        {copy.data && (
          <>
            <Gallery photos={fromPagePhotos(copy.data.photos)} eager />
            <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
          </>
        )}

        {nearby.data && nearby.data.length > 0 && (
          <section className="flex flex-col items-center gap-6">
            <h2 className="text-2xl font-semibold tracking-tight">Nearby highlights</h2>

            {/*
              Two columns off a phone, so the distances line up down the right
              and the eye can scan them.

              This was a <dl> while an entry was a place and a distance. It
              stopped being one when entries got a sentence each: an entry
              nobody has described yet would be a <dt> with no <dd>.
            */}
            <div className="grid w-full max-w-3xl gap-x-10 gap-y-5 sm:grid-cols-2">
              {nearby.data.map((place) => (
                <Nearby key={place.name} place={place} />
              ))}
            </div>
          </section>
        )}
      </div>
    </Layout>
  )
}

function Nearby({ place }: { place: Attraction }) {
  return (
    <article className="border-b border-neutral-100 pb-3 text-left">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="font-medium">
          {/*
            A row with no link renders as plain text rather than as an anchor
            going nowhere. Mount Washington is the one that has none: it could
            be the Cog, the Auto Road or the state park, and which the owner
            means is theirs to say.
          */}
          {place.url ? (
            <a
              href={place.url}
              target="_blank"
              rel="noopener noreferrer"
              className="underline decoration-neutral-300 underline-offset-4 hover:decoration-neutral-900"
            >
              {place.name}
            </a>
          ) : (
            place.name
          )}
        </h3>
        <p className="shrink-0 text-sm text-neutral-500">{place.distance}</p>
      </div>

      {/* Nothing at all where nobody has written one — the same rule the prose
          slots follow, and what every row looked like before the column. */}
      {place.description && (
        <p className="mt-1 text-sm text-neutral-600">{place.description}</p>
      )}
    </article>
  )
}
