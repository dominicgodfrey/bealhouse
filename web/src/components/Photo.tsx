/**
 * One photograph, at the size the reader's screen can actually use.
 *
 * The server stores every uploaded photograph at four widths in JPEG and WebP
 * (decision #16, internal/media) and hands back the `srcset` strings for the
 * ones it really wrote. This renders them; the browser picks.
 *
 * **The widths are the point, more than the formats.** A room card four hundred
 * CSS pixels wide was downloading the 2400px file — about 950 KB where 76 KB
 * would do. WebP saves a further half on top of that, and on mountain mobile
 * data both of those are the difference between a page that loads and one
 * somebody gives up on.
 *
 * Two rules hold this together:
 *
 *   - **`src` is always a whole file**, never a rung. A browser too old for
 *     `srcset` gets the canonical JPEG and a correct page.
 *   - **The srcsets come from the server**, never assembled here from the URL.
 *     A photograph smaller than a rung does not have that rung, and a `srcset`
 *     entry that 404s does *not* fall back — the browser has committed to that
 *     candidate and the result is a broken image. `media.Sources` is the single
 *     rule that decides which rungs exist; a second copy of it in TypeScript is
 *     exactly the kind of drift that made passkey ids unusable once already.
 */
export type PhotoSources = {
  /** srcset for the JPEG ladder. Absent for a photograph with only one size. */
  srcset?: string
  /** srcset for the WebP ladder. */
  webpSrcset?: string
}

export function Photo({
  src,
  alt,
  sources,
  sizes,
  className,
  loading = 'lazy',
}: {
  src: string
  alt: string
  sources?: PhotoSources
  /**
   * What width this will be rendered at, so the browser can choose before it
   * has laid the page out. Wrong here means the wrong rung downloaded, which is
   * the whole cost of getting it wrong — hence a caller-supplied value rather
   * than a default that is right for one page and wrong for the rest.
   */
  sizes: string
  className?: string
  /**
   * Lazy by default. The one to set eager is a photograph above the fold, where
   * lazy loading delays the thing the page is for.
   */
  loading?: 'lazy' | 'eager'
}) {
  return (
    <picture>
      {sources?.webpSrcset && (
        <source type="image/webp" srcSet={sources.webpSrcset} sizes={sizes} />
      )}
      <img
        src={src}
        srcSet={sources?.srcset}
        sizes={sources?.srcset ? sizes : undefined}
        alt={alt}
        loading={loading}
        decoding="async"
        className={className}
      />
    </picture>
  )
}
