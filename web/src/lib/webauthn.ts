/**
 * The browser half of a passkey ceremony (decision #15).
 *
 * WebAuthn's options and responses carry raw bytes; JSON does not. So every
 * value has to be base64url on the wire and an ArrayBuffer in the call, and
 * something has to convert between them. Both conversions are the platform's
 * own now — `PublicKeyCredential.parse*OptionsFromJSON()` going in and
 * `credential.toJSON()` coming back — and they produce exactly the encoding
 * go-webauthn already writes and reads on the other side. That leaves this file
 * as the two calls and the sentences a person gets when one fails, with no
 * library in between doing base64 twice.
 *
 * A browser without them is told so plainly rather than shown a button that
 * fails. Every browser that can hold a passkey at all has had these since 2024,
 * and the console is opened from two phones the owners are holding.
 */

/**
 * The static half of PublicKeyCredential, which lib.dom does not describe in
 * every TypeScript version. Declared narrowly — the two methods actually used —
 * rather than reached through `any`, so a typo here is still a type error.
 */
type OptionParsers = {
  parseCreationOptionsFromJSON(json: unknown): PublicKeyCredentialCreationOptions
  parseRequestOptionsFromJSON(json: unknown): PublicKeyCredentialRequestOptions
}

/** The response shape that goes back to the server, whatever it contains. */
type CredentialJSON = Record<string, unknown>

/**
 * Read off globalThis rather than the bare global, so a browser that has never
 * heard of WebAuthn gives `undefined` here instead of throwing on this module's
 * first line and taking the whole console down with it.
 */
function parsers(): Partial<OptionParsers> | undefined {
  return (globalThis as { PublicKeyCredential?: Partial<OptionParsers> }).PublicKeyCredential
}

export function passkeysSupported(): boolean {
  const api = parsers()
  return (
    typeof api?.parseCreationOptionsFromJSON === 'function' &&
    typeof api.parseRequestOptionsFromJSON === 'function' &&
    typeof navigator.credentials?.create === 'function'
  )
}

/** Both ceremonies arrive from the server wrapped the way the spec writes them. */
type Wrapped = { publicKey: unknown }

/**
 * Enrols this phone against a registration challenge.
 *
 * Must be called from a click. Browsers require a user gesture for both
 * ceremonies, so anything that ran this on mount would be refused before the
 * owner ever saw a prompt.
 */
export async function register(options: Wrapped): Promise<CredentialJSON> {
  const api = parsers() as OptionParsers
  return finish(
    await navigator.credentials.create({
      publicKey: api.parseCreationOptionsFromJSON(options.publicKey),
    }),
  )
}

/** Signs in with a passkey this phone already holds. */
export async function authenticate(options: Wrapped): Promise<CredentialJSON> {
  const api = parsers() as OptionParsers
  return finish(
    await navigator.credentials.get({
      publicKey: api.parseRequestOptionsFromJSON(options.publicKey),
    }),
  )
}

function finish(credential: Credential | null): CredentialJSON {
  if (!credential) throw new Error('Your phone did not return a passkey. Please try again.')
  return (credential as PublicKeyCredential & { toJSON(): CredentialJSON }).toJSON()
}

/**
 * Turns what the browser throws into something the owner can act on.
 *
 * A DOMException's own message is written for a developer and is often empty,
 * so the name is what carries the meaning. The two that matter are the two that
 * are not faults: a cancelled or timed-out prompt, and a phone that is already
 * enrolled — which the server's exclusion list is what produces, and which
 * reads as a failure unless it is named.
 */
export function explain(err: unknown): Error {
  if (err instanceof DOMException) {
    switch (err.name) {
      case 'NotAllowedError':
        return new Error('That was cancelled, or it timed out. Try again when you are ready.')
      case 'InvalidStateError':
        return new Error('This phone is already enrolled. Sign in with it instead.')
      case 'SecurityError':
        return new Error(
          'This page is not being served from the address the passkey belongs to, so your phone will not use it.',
        )
      case 'NotSupportedError':
        return new Error('This phone cannot create the kind of passkey the console needs.')
    }
  }
  return err instanceof Error ? err : new Error('Something went wrong.')
}
