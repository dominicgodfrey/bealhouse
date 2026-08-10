import { useState, type ReactNode } from 'react'

import { ApiError } from '../../lib/api'
import {
  fetchDevices,
  fetchPasskeys,
  formatInstant,
  mintInvitation,
  revokePasskey,
  signOutEverywhere,
  type Device,
  type Invitation,
  type Passkey,
} from '../../lib/admin'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import {
  disablePush,
  enablePush,
  fetchPushSettings,
  pushEnabledHere,
  pushSupported,
} from '../../lib/push'
import { useConsole } from './Console'

/**
 * The phones that can open the console, and the phones currently in it.
 *
 * Two lists rather than one, because they are two different questions and the
 * answers come apart in exactly the case that matters: a handset that has gone
 * missing is a session to end *and* a key to strike off, and the owner needs to
 * see both to be sure they have done both.
 */
export function Account() {
  const { identity, refresh } = useConsole()

  const [nonce, setNonce] = useState(0)
  const reload = () => setNonce((n) => n + 1)

  const passkeys = useAsync(fetchPasskeys, [nonce])
  const devices = useAsync(fetchDevices, [nonce])

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Phones and sessions</h1>
        <p className="text-sm text-neutral-600">Signed in as {identity.name}.</p>
      </header>

      <Section title="Phones that can sign in">
        {passkeys.loading && <Loading what="your phones" />}
        {passkeys.error && <ErrorNote error={passkeys.error} />}
        {passkeys.data?.map((passkey) => (
          <PasskeyRow
            key={passkey.id}
            passkey={passkey}
            onChanged={reload}
            onSignedOut={refresh}
          />
        ))}
      </Section>

      <InvitePanel onSignedOut={refresh} />

      <NotificationsPanel />

      <Section title="Signed in now">
        {devices.loading && <Loading what="your sessions" />}
        {devices.error && <ErrorNote error={devices.error} />}
        {devices.data?.map((device) => (
          <DeviceRow key={`${device.passkeyId ?? 'revoked'}:${device.signedInAt}`} device={device} />
        ))}

        {devices.data && devices.data.length > 0 && (
          <SignOutEverywhere onSignedOut={refresh} />
        )}
      </Section>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-sm font-medium tracking-wide text-neutral-500 uppercase">{title}</h2>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  )
}

function Card({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-neutral-200 bg-white p-4">
      {children}
    </div>
  )
}

/**
 * A 401 is the session having gone, not something to put in a red box.
 *
 * Every action on this page routes its failures through here so the gate closes
 * and asks for the passkey again, rather than each button inventing its own way
 * of saying the same thing.
 */
function report(err: unknown, onSignedOut: () => void, setError: (e: Error | null) => void) {
  if (err instanceof ApiError && err.status === 401) {
    onSignedOut()
    return
  }
  setError(err instanceof Error ? err : new Error('Something went wrong.'))
}

function PasskeyRow({
  passkey,
  onChanged,
  onSignedOut,
}: {
  passkey: Passkey
  onChanged: () => void
  onSignedOut: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function remove() {
    setWorking(true)
    setError(null)
    try {
      await revokePasskey(passkey.id)
      onChanged()
    } catch (err) {
      // Removing the only passkey answers 409 with a sentence explaining that
      // it would leave a console nobody can open. It is the server's to word,
      // and it is already the right words.
      report(err, onSignedOut, setError)
      setWorking(false)
      setConfirming(false)
    }
  }

  return (
    <Card>
      <div>
        <p className="font-medium">{passkey.label}</p>
        <p className="text-sm text-neutral-600">
          Added {formatInstant(passkey.createdAt)}
          {passkey.lastUsedAt
            ? ` · last used ${formatInstant(passkey.lastUsedAt)}`
            : ' · not used yet'}
        </p>
      </div>

      {error && <ErrorNote error={error} />}

      {confirming ? (
        <div className="flex flex-col gap-2 sm:flex-row">
          <button
            type="button"
            onClick={remove}
            disabled={working}
            className="rounded-lg bg-red-700 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
          >
            {working ? 'Removing…' : `Yes, remove ${passkey.label}`}
          </button>
          <button
            type="button"
            onClick={() => setConfirming(false)}
            disabled={working}
            className="rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
          >
            Keep it
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setConfirming(true)}
          className="self-start rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
        >
          Remove this phone
        </button>
      )}

      {confirming && (
        <p className="text-sm text-neutral-600">
          This signs that phone out as well, immediately.
        </p>
      )}
    </Card>
  )
}

/**
 * Minting the invitation that adds the second phone.
 *
 * The link is shown here and sent by the owner, rather than emailed by the
 * server: it is a permanent way into the console, and the person minting it is
 * the one who knows which handset is about to be handed it.
 */
function InvitePanel({ onSignedOut }: { onSignedOut: () => void }) {
  const [label, setLabel] = useState('')
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [invitation, setInvitation] = useState<Invitation | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      setInvitation(await mintInvitation(label.trim()))
    } catch (err) {
      report(err, onSignedOut, setError)
    } finally {
      setWorking(false)
    }
  }

  return (
    <Section title="Add a phone">
      <Card>
        {invitation ? (
          <>
            <div>
              <p className="font-medium">Invitation for {invitation.label}</p>
              <p className="text-sm text-neutral-600">
                Open this on the new phone. It works once and expires at{' '}
                {formatInstant(invitation.expiresAt)}.
              </p>
            </div>

            {/*
              Readable and selectable rather than a link to tap: this page is
              open on the phone that minted it, and the address belongs on the
              other one.
            */}
            <input
              readOnly
              value={invitation.url}
              onFocus={(e) => e.currentTarget.select()}
              className="w-full rounded-lg border border-neutral-300 bg-neutral-50 px-3 py-3 font-mono text-xs"
            />

            <div className="flex flex-col gap-2 sm:flex-row">
              <CopyButton text={invitation.url} />
              <button
                type="button"
                onClick={() => setInvitation(null)}
                className="rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
              >
                Done
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="text-sm text-neutral-600">
              Creates a one-time link that enrols one more phone. Anyone who opens it can sign in
              from then on, so send it the way you would send a key.
            </p>

            {error && <ErrorNote error={error} />}

            <label className="flex flex-col gap-1 text-sm">
              <span className="font-medium">Whose phone is it?</span>
              <input
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="New phone"
                className="rounded-lg border border-neutral-300 px-3 py-3"
              />
            </label>

            <button
              type="button"
              onClick={submit}
              disabled={working}
              className="self-start rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
            >
              {working ? 'Creating…' : 'Create an invitation'}
            </button>
          </>
        )}
      </Card>
    </Section>
  )
}

/**
 * Best effort, and never the only way to get the link.
 *
 * navigator.clipboard needs a secure context and the owner's permission, so it
 * fails on plain HTTP and in some browsers — which is why the address is in a
 * selectable field above this whatever happens here.
 */
function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => setCopied(false),
        )
      }}
      className="rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
    >
      {copied ? 'Copied' : 'Copy the link'}
    </button>
  )
}

function DeviceRow({ device }: { device: Device }) {
  return (
    <Card>
      <div>
        <p className="font-medium">
          {device.label}
          {device.current && (
            <span className="ml-2 rounded bg-neutral-900 px-2 py-0.5 text-xs font-medium text-white">
              This phone
            </span>
          )}
        </p>
        <p className="text-sm text-neutral-600">
          Signed in {formatInstant(device.signedInAt)} · last used{' '}
          {formatInstant(device.lastSeenAt)}
        </p>
        {/*
          The browser's own description of itself. Ugly, and the only thing that
          tells two identically-labelled sessions apart, so it is shown as it
          came rather than guessed at.
        */}
        <p className="mt-1 text-xs break-words text-neutral-500">{device.userAgent}</p>
      </div>
    </Card>
  )
}

/**
 * The button for a phone that went missing with the console open on it.
 *
 * It leaves every passkey alone, so the owner signs straight back in on the
 * phone they still have — which is also why it ends this session too, and says
 * so before it does.
 */
function SignOutEverywhere({ onSignedOut }: { onSignedOut: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await signOutEverywhere()
      onSignedOut()
    } catch (err) {
      report(err, onSignedOut, setError)
      setWorking(false)
    }
  }

  if (!confirming) {
    return (
      <>
        {error && <ErrorNote error={error} />}
        <button
          type="button"
          onClick={() => setConfirming(true)}
          className="self-start rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
        >
          Sign out every phone
        </button>
      </>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm text-neutral-600">
        This ends every session, including this one. Your phones keep their passkeys and can sign
        straight back in.
      </p>
      <div className="flex flex-col gap-2 sm:flex-row">
        <button
          type="button"
          onClick={submit}
          disabled={working}
          className="rounded-lg bg-red-700 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
        >
          {working ? 'Signing out…' : 'Yes, sign out everywhere'}
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          disabled={working}
          className="rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

/**
 * Notifications on this handset.
 *
 * On the phones screen rather than in settings because that is what it is about:
 * a subscription belongs to the browser in your hand, not to the inn, and the
 * question "is this phone going to buzz" is asked in the same breath as "can
 * this phone sign in".
 *
 * The switch reports on *this* browser and the count reports on all of them,
 * which is the pair worth showing. A handset that was cleared or reinstalled
 * stops receiving without saying so, and an owner who can see "2 phones" when
 * they know there are three has learned something.
 */
function NotificationsPanel() {
  const [nonce, setNonce] = useState(0)
  const settings = useAsync(fetchPushSettings, [nonce])
  const here = useAsync(pushEnabledHere, [nonce])

  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const supported = pushSupported()

  async function toggle() {
    setWorking(true)
    setError(null)
    try {
      if (here.data) {
        await disablePush()
      } else {
        await enablePush(settings.data?.publicKey ?? '', 'This phone')
      }
      setNonce((n) => n + 1)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setWorking(false)
    }
  }

  return (
    <Section title="Notifications">
      <Card>
        <div className="flex flex-col gap-1">
          <span className="font-medium">A booking or a message reaches this phone</span>
          <span className="text-sm text-neutral-600">
            Arrives whether or not the console is open. It says who and when, and nothing else —
            tapping it opens the booking.
          </span>
        </div>

        {settings.error && <ErrorNote error={settings.error} />}
        {error && <ErrorNote error={error} />}

        {/*
          Three ways this cannot work, and each says which one rather than
          offering a switch that fails. A browser too old for the Push API, a
          deployment with no VAPID keys, and a permission the owner has already
          denied — the last of which the browser will never re-prompt for, so
          the message has to send them to site settings.
        */}
        {!supported ? (
          <p className="text-sm text-neutral-600">
            This browser cannot receive notifications. On a phone, open the console in Chrome or
            Samsung Internet.
          </p>
        ) : settings.data && !settings.data.configured ? (
          <p className="text-sm text-neutral-600">
            No notification keys are configured on this server, so there is nothing to subscribe
            to. Run <code className="font-mono">bealhouse vapid</code> and set the two keys it
            prints.
          </p>
        ) : (
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="button"
              onClick={toggle}
              disabled={working || here.loading}
              className={`rounded-lg px-4 py-2 text-sm font-medium disabled:opacity-60 ${
                here.data
                  ? 'border border-neutral-300 bg-white text-neutral-900'
                  : 'bg-neutral-900 text-white'
              }`}
            >
              {working
                ? 'Just a moment…'
                : here.data
                  ? 'Turn off on this phone'
                  : 'Turn on for this phone'}
            </button>

            {settings.data && (
              <span className="text-sm text-neutral-600">
                {settings.data.subscribers === 0
                  ? 'No phones are receiving notifications.'
                  : `${settings.data.subscribers} ${
                      settings.data.subscribers === 1 ? 'phone is' : 'phones are'
                    } receiving them.`}
              </span>
            )}
          </div>
        )}
      </Card>
    </Section>
  )
}
