import { useEffect, useState } from 'react'

// A minimal replacement for a data-fetching library, which this stage of the
// booking flow does not yet earn: four screens, each loading one thing.
//
// TanStack Query is in the architecture for when caching, retries and
// background refresh start paying for themselves — the admin console's calendar
// especially. Reaching for it now would be a dependency carrying a hook.

export type Async<T> = {
  data: T | null
  error: Error | null
  loading: boolean
}

/**
 * Runs `load` whenever `deps` change, discarding the result of a request that
 * has been superseded — a guest editing dates fires several of these, and the
 * slowest must not be the one that wins.
 */
export function useAsync<T>(load: () => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ data: null, error: null, loading: true })

  useEffect(() => {
    let current = true
    setState((prev) => ({ ...prev, loading: true, error: null }))

    load().then(
      (data) => current && setState({ data, error: null, loading: false }),
      (error: Error) => current && setState({ data: null, error, loading: false }),
    )

    return () => {
      current = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return state
}
