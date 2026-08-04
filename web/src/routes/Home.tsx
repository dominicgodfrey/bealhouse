import { Link } from 'react-router-dom'

export function Home() {
  return (
    <main className="mx-auto flex min-h-dvh max-w-2xl flex-col justify-center gap-4 p-8">
      <h1 className="text-4xl font-semibold tracking-tight">Beal House</h1>
      <p className="text-neutral-600">
        Foundation is up: one Go binary serving this bundle. Booking engine,
        marketing site, and admin console land on top of it.
      </p>
      <Link className="text-blue-700 underline underline-offset-4" to="/health">
        System health
      </Link>
    </main>
  )
}
