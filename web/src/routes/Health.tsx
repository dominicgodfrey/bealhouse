import { useEffect, useState } from 'react'
import { Link } from 'react-router'

type Health = { status: string; db: string; time: string }

// Deliberately hand-rolled: this route exists to prove the SPA can reach the Go
// API and that a deep link survives a hard refresh. Real data fetching arrives
// with TanStack Query and the generated OpenAPI client in step 3.
export function Health() {
  const [health, setHealth] = useState<Health | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/health')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(setHealth)
      .catch((e: Error) => setError(e.message))
  }, [])

  return (
    <main className="mx-auto flex min-h-dvh max-w-2xl flex-col justify-center gap-4 p-8">
      <h1 className="text-2xl font-semibold tracking-tight">System health</h1>

      {error && <p className="text-red-700">Could not reach the API: {error}</p>}

      {health && (
        <dl className="grid grid-cols-[6rem_1fr] gap-y-1 font-mono text-sm">
          <dt className="text-neutral-500">status</dt>
          <dd>{health.status}</dd>
          <dt className="text-neutral-500">db</dt>
          <dd>{health.db}</dd>
          <dt className="text-neutral-500">time</dt>
          <dd>{health.time}</dd>
        </dl>
      )}

      {!health && !error && <p className="text-neutral-500">Checking…</p>}

      <Link className="text-blue-700 underline underline-offset-4" to="/">
        Back
      </Link>
    </main>
  )
}
