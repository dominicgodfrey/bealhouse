import { useState } from 'react'

import {
  fetchEmailCopy,
  resetEmailCopy,
  saveEmailCopy,
  type EmailCopy as Copy,
} from '../../lib/console'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Aside, Button, Card, Field, Input, Saved, Screen, Textarea, useReload, useSaving } from './ui'

/**
 * The seven messages the inn sends.
 *
 * The words are the owner's, the same way room descriptions and photos are, so
 * all seven ship blank — a subject marked PLACEHOLDER and one line saying what
 * the message is for. This screen is where the real sentences go, and it says
 * out loud which messages are still going out as placeholders, because a
 * placeholder presented as finished copy is one that reaches a guest.
 *
 * Two things are deliberately not editable here. The **layout** carries the
 * letterhead and the table scaffolding that survives Outlook, and one bad edit
 * to it breaks every message rather than the one on screen. The **fields** a
 * message can mention are fixed by the payload the server builds — the list
 * below each box is the whole of what is available.
 */
export function EmailCopy() {
  const [nonce, reload] = useReload()
  const copies = useAsync(fetchEmailCopy, [nonce])

  const unwritten = copies.data?.filter((c) => !c.edited).length ?? 0

  return (
    <Screen title="Email copy" subtitle="What guests read when the inn writes to them.">
      {copies.loading && <Loading what="the messages" />}
      {copies.error && <ErrorNote error={copies.error} />}

      {unwritten > 0 && (
        <p className="rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          {unwritten === 1
            ? 'One message has not been written yet and is going out as a placeholder.'
            : `${unwritten} messages have not been written yet and are going out as placeholders.`}
        </p>
      )}

      <Aside>
        A save applies to the very next message, not the next deploy. Copy that will not render is
        refused here rather than at send time — which would be after a guest’s card had been
        charged.
      </Aside>

      {copies.data?.map((copy) => <Message key={copy.name} copy={copy} onChanged={reload} />)}
    </Screen>
  )
}

const titles: Record<string, string> = {
  booking_confirmation: 'Booking confirmation',
  balance_warning: 'Balance coming off the card tomorrow',
  balance_receipt: 'Balance receipt',
  balance_failed: 'The card was refused',
  cancellation_refund: 'Cancellation and refund',
  owner_notification: 'Your own copy of a new booking',
  checkout_reminder: 'The morning they leave',
}

function Message({ copy, onChanged }: { copy: Copy; onChanged: () => void }) {
  const { refresh } = useConsole()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(copy)
  const saving = useSaving(refresh)

  if (!open) {
    return (
      <Card>
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <span className="font-medium">{titles[copy.name] ?? copy.name}</span>
          {!copy.edited && (
            <span className="rounded bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-900">
              still the placeholder
            </span>
          )}
        </div>
        <p className="text-sm text-neutral-600">{copy.subject}</p>
        <Button onClick={() => setOpen(true)}>Edit</Button>
      </Card>
    )
  }

  return (
    <Card>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="font-medium">{titles[copy.name] ?? copy.name}</span>
        <span className="font-mono text-xs text-neutral-500">{copy.name}</span>
      </div>

      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved. The next message uses it.</Saved>}

      <Field label="Subject">
        <Input
          value={draft.subject}
          onChange={(e) => {
            setDraft({ ...draft, subject: e.target.value })
            saving.clear()
          }}
        />
      </Field>

      <Field label="Body">
        <Textarea
          rows={12}
          value={draft.body}
          onChange={(e) => {
            setDraft({ ...draft, body: e.target.value })
            saving.clear()
          }}
          className="font-mono text-xs"
        />
      </Field>

      {/*
        Read off the payload struct on the server rather than listed here, so it
        cannot drift from what the message actually carries. A name not in this
        list renders as nothing, silently — which is exactly the mistake a stale
        hand-kept list would cause.
      */}
      <div className="rounded-lg bg-neutral-50 p-3 text-xs">
        <p className="mb-1 font-medium">What this message knows about the booking</p>
        <p className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-neutral-600">
          {copy.fields.map((field) => (
            <span key={field.name}>
              {field.list
                ? `{{range .Data.${field.name}}}…{{end}}`
                : `{{.Data.${field.name}}}`}
            </span>
          ))}
        </p>
        <p className="mt-2 text-neutral-600">
          Money and dates arrive already written out — “$1,240.00”, “Monday, June 14, 2027” — so
          they read the same here as they do on the booking page. A field that is empty on this
          particular booking, like the balance on a stay paid in full, is how the copy can tell the
          two cases apart: <code>{'{{if .Data.BalanceDue}}'}</code>.
        </p>
      </div>

      <div className="flex flex-wrap gap-2">
        <Button
          kind="primary"
          onClick={() =>
            saving.run(async () => {
              await saveEmailCopy(draft.name, draft.subject, draft.body)
              onChanged()
            })
          }
          disabled={saving.working}
        >
          {saving.working ? 'Saving…' : 'Save'}
        </Button>
        <Button onClick={() => setOpen(false)}>Close</Button>
        {copy.edited && (
          <Button
            onClick={() =>
              saving.run(async () => {
                await resetEmailCopy(draft.name)
                setOpen(false)
                onChanged()
              })
            }
            disabled={saving.working}
          >
            Put the original back
          </Button>
        )}
      </div>
    </Card>
  )
}
