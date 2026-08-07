import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'

import { beginEnrollment, finishEnrollment } from '../../lib/admin'
import { explain, passkeysSupported, register } from '../../lib/webauthn'
import { ErrorNote } from '../../components/Layout'

/**
 * Where a phone accepts an invitation (decision #15).
 *
 * Outside the console's gate, because a phone enrolling is by definition not
 * signed in yet. What authorises it is the single-use token in the fragment,
 * minted either by `bealhouse enroll` on the server or from an already-trusted
 * phone — there is no third way in, and a console with no passkeys and nobody
 * able to reach the box is a console that stays shut.
 *
 * Finishing signs the phone in. It has just completed a user-verified ceremony
 * against a token spendable exactly once, which is strictly more than a sign-in
 * proves, so sending the owner to the login page to do it again would be
 * friction bought with nothing.
 */
export function Enroll() {
  const location = useLocation()
  const navigate = useNavigate()

  // Held here rather than read from the URL where it is used, because the
  // effect below is about to take it out of the URL.
  const [token, setToken] = useState(() => window.location.hash.replace(/^#/, ''))

  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  // Capture the token, then take it out of the address bar.
  //
  // It never reaches the server in a fragment and so is in no access log, but
  // it is still a one-shot key to the admin console sitting in plain sight, in
  // this browser's history, and in any screenshot of the page. It has done its
  // job the moment this component holds it.
  //
  // The capture is here and not only in the initialiser above because a
  // fragment arriving on the page already open is a *same-document* navigation:
  // nothing re-mounts, so a mount-time read would miss it — and this effect
  // would then erase an invitation the owner had just pasted in.
  useEffect(() => {
    const arriving = location.hash.replace(/^#/, '')
    if (!arriving) return

    setToken(arriving)
    navigate({ pathname: location.pathname, hash: '' }, { replace: true })
  }, [location.hash, location.pathname, navigate])

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      // Both halves inside the click: the browser will not raise a passkey
      // prompt without a user gesture.
      await finishEnrollment(await register(await beginEnrollment(token)))
      navigate('/admin/account', { replace: true })
    } catch (err) {
      setError(explain(err))
    } finally {
      setWorking(false)
    }
  }

  return (
    <div className="mx-auto flex min-h-dvh max-w-2xl flex-col justify-center gap-4 bg-neutral-50 px-4 py-6 text-neutral-900">
      <div className="flex flex-col gap-4 rounded-lg border border-neutral-200 bg-white p-5">
        <div className="flex flex-col gap-1">
          <img src="/logo.svg" alt="" className="mb-2 h-5 w-auto self-start" />
          <h1 className="text-xl font-semibold tracking-tight">Set up this phone</h1>
          <p className="text-sm text-neutral-600">
            This adds the phone you are holding to the Beal House console. It will ask for Face ID,
            a fingerprint, or your passcode, and nothing is stored here that could be typed in
            somewhere else.
          </p>
        </div>

        {error && <ErrorNote error={error} />}

        {!token ? (
          <ErrorNote
            error={
              new Error(
                'This link has no invitation in it. An invitation can only be used once and expires after fifteen minutes, so ask for a fresh one.',
              )
            }
          />
        ) : !passkeysSupported() ? (
          <ErrorNote
            error={
              new Error(
                'This browser cannot create passkeys, so it cannot be enrolled. Open the link on your phone instead.',
              )
            }
          />
        ) : (
          <button
            type="button"
            onClick={submit}
            disabled={working}
            className="rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
          >
            {working ? 'Waiting for your phone…' : 'Set up this phone'}
          </button>
        )}
      </div>
    </div>
  )
}
