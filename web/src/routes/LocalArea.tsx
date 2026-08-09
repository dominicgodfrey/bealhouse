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
              A definition list, because that is what this is: a place and how
              far away it is. Two columns on anything but a phone so the
              distances line up down the right and the eye can scan them —
              which was the whole complaint about the run-on paragraph this
              replaced.
            */}
            <dl className="grid w-full max-w-2xl gap-x-10 gap-y-3 sm:grid-cols-2">
              {nearby.data.map((place) => (
                <Nearby key={place.name} place={place} />
              ))}
            </dl>
          </section>
        )}
      </div>
    </Layout>
  )
}

function Nearby({ place }: { place: Attraction }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-neutral-100 pb-2">
      <dt className="font-medium">
        {/*
          A row with no link renders as plain text rather than as an anchor
          going nowhere. Mount Washington is the one that has none: it could be
          the Cog, the Auto Road or the state park, and which the owner means is
          theirs to say.
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
      </dt>
      <dd className="shrink-0 text-sm text-neutral-500">{place.distance}</dd>
    </div>
  )
}
