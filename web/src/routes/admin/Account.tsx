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
