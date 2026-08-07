import { fetchPageCopy, paragraphs } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { Layout, Loading, Prose } from '../components/Layout'

/**
 * The owner's story.
 *
 * The page with the least of ours in it and the most of theirs: a heading, some
 * paragraphs, and nothing else. Every word comes out of the console, and when
 * none has been written the page says so plainly rather than showing invented
 * prose about a family and a house neither of which we know anything about.
 */
export function About() {
  const copy = useAsync(() => fetchPageCopy('about'), [])

  return (
    <Layout>
      <div className="flex max-w-prose flex-col gap-6">
        <h1 className="text-4xl font-semibold tracking-tight">About Beal House</h1>

        {copy.loading && <Loading what="the page" />}

        {copy.data?.written ? (
          <Prose heading={copy.data.heading} paragraphs={paragraphs(copy.data.body)} />
        ) : (
          copy.data && (
            <p className="text-neutral-600">
              Beal House is a seven-room inn in Littleton, New Hampshire. More about it soon — in
              the meantime, the rooms and the dates you can book are all real.
            </p>
          )
        )}
      </div>
    </Layout>
  )
}
