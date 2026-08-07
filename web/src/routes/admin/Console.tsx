import { useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useOutletContext } from 'react-router'

import { ApiError } from '../../lib/api'
import { fetchIdentity, beginSignIn, finishSignIn, signOut, type Identity } from '../../lib/admin'
import { useAsync } from '../../lib/useAsync'
import { authenticate, explain, passkeysSupported } from '../../lib/webauthn'
import { ErrorNote, Loading } from '../../components/Layout'

/**
 * The console's frame, and the gate in front of it (decision #15).
 *
 * The gate is the session cookie and nothing else. There is no client-side
 * check of who the caller is and no route that hides a screen from an
 * unauthorised one — every endpoint under /api/admin is closed on the server,
 * so the worst a forged front end can do is show empty boxes.
 *
 * A 401 renders the sign-in **in place** rather than redirecting to a login
 * address. That keeps the URL the owner was heading for intact, so signing in
 * lands them where they were going, and it means a session expiring under an
 * open console is a prompt rather than a page that navigated out from under
 * them.
 */
export function Console() {
  const [nonce, setNonce] = useState(0)
  const refresh = () => setNonce((n) => n + 1)

  const session = useAsync<Identity | null>(async () => {
    try {
      return await fetchIdentity()
    } catch (err) {
      // Not an error to display. Nobody is signed in, which is the ordinary
      // state of a console nobody has opened yet.
      if (err instanceof ApiError && err.status === 401) return null
      throw err
    }
  }, [nonce])

  if (session.loading) {
    return (
      <Shell>
        <Loading what="the console" />
      </Shell>
    )
  }

  // A console with no origin to verify a passkey against answers 503 with a
  // sentence saying so, and it is the one worth putting on screen unchanged.
  if (session.error) {
    return (
      <Shell>
        <ErrorNote error={session.error} />
      </Shell>
    )
  }

  if (!session.data) {
    return (
      <Shell>
        <SignIn onSignedIn={refresh} />
      </Shell>
    )
  }

  return (
    <Shell identity={session.data} onSignedOut={refresh}>
      <Outlet context={{ identity: session.data, refresh } satisfies ConsoleContext} />
    </Shell>
  )
}

export type ConsoleContext = {
  identity: Identity
  /**
   * Re-checks the session. A screen that gets a 401 mid-action calls this and
   * the gate closes, rather than each screen inventing its own way to say the
   * session has gone.
   */
  refresh: () => void
}

export function useConsole(): ConsoleContext {
  return useOutletContext<ConsoleContext>()
}

/**
 * The frame, signed in or not.
 *
 * Phone-first because that is where it is used: one column, tap targets that
 * do not need aiming, and a header that stays put so signing out is never a
 * scroll away.
 */
function Shell({
  children,
  identity,
  onSignedOut,
}: {
  children: ReactNode
  identity?: Identity
  onSignedOut?: () => void
}) {
  return (
    <div className="flex min-h-dvh flex-col bg-neutral-50 text-neutral-900">
      <header className="sticky top-0 z-10 border-b border-neutral-200 bg-white">
        <div className="mx-auto flex max-w-2xl items-center justify-between gap-4 px-4 py-3">
          <Link to="/admin" className="flex items-center gap-2.5 font-semibold tracking-tight">
            <img src="/logo.svg" alt="" className="h-5 w-auto" />
            Console
          </Link>
          {identity && onSignedOut && <SignOutButton onSignedOut={onSignedOut} />}
        </div>

        {identity && (
          <nav className="mx-auto flex max-w-2xl gap-1 overflow-x-auto px-4 pb-2 text-sm">
            {/*
              One entry, because one screen is built. The frame is here so the
              rest — today, upcoming, the calendar, rates — land in it rather
              than each arriving with its own idea of where the tabs go.
            */}
            <Tab to="/admin/account">Phones</Tab>
          </nav>
        )}
      </header>

      <main className="mx-auto w-full max-w-2xl flex-1 px-4 py-6">{children}</main>
    </div>
  )
}

function Tab({ to, children }: { to: string; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        `rounded-lg px-3 py-2 font-medium ${
          isActive ? 'bg-neutral-900 text-white' : 'text-neutral-600 hover:bg-neutral-100'
        }`
      }
    >
      {children}
    </NavLink>
  )
}

function SignOutButton({ onSignedOut }: { onSignedOut: () => void }) {
  const [working, setWorking] = useState(false)

  async function submit() {
    setWorking(true)
    // Whatever happens, re-check: signing out is idempotent on the server and
    // clears the cookie either way, so there is nothing here worth reporting.
    try {
      await signOut()
    } finally {
      setWorking(false)
      onSignedOut()
    }
  }

  return (
    <button
      type="button"
      onClick={submit}
      disabled={working}
      className="rounded-lg border border-neutral-300 px-3 py-2 text-sm font-medium disabled:opacity-60"
    >
      {working ? 'Signing out…' : 'Sign out'}
    </button>
  )
}

/**
 * One button, and nothing to type.
 *
 * The credential is discoverable, so the phone already holds the account handle
 * beside the key and offers both — there is no username field here because
 * there is nothing a person could usefully put in one.
 */
function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      // Begun and finished inside the click. Browsers require a user gesture
      // for the prompt, and one that started on mount would be refused before
      // anybody saw it.
      await finishSignIn(await authenticate(await beginSignIn()))
      onSignedIn()
    } catch (err) {
      setError(explain(err))
    } finally {
      setWorking(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 rounded-lg border border-neutral-200 bg-white p-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Beal House console</h1>
        <p className="text-sm text-neutral-600">
          Sign in with the phone you enrolled. It will ask for Face ID, a fingerprint, or your
          passcode.
        </p>
      </div>

      {error && <ErrorNote error={error} />}

      {passkeysSupported() ? (
        <button
          type="button"
          onClick={submit}
          disabled={working}
          className="rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
        >
          {working ? 'Waiting for your phone…' : 'Sign in with your passkey'}
        </button>
      ) : (
        <ErrorNote
          error={
            new Error(
              'This browser cannot use passkeys, so it cannot open the console. Try it on your phone, or in an up-to-date Safari, Chrome or Firefox.',
            )
          }
        />
      )}
    </div>
  )
}
