import { useEffect, useState } from 'react'

import { fetchSettings, saveSettings, type Settings } from '../../lib/console'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Aside, Button, Card, Field, Input, Saved, Screen, Section, Textarea, useSaving } from './ui'

/**
 * The numbers the whole system runs on.
 *
 * Two of them are rates, and they cross the wire pre-scaled to
 * hundred-thousandths — matching pricing.Rate on the server — so a percentage
 * never becomes a float on the way to or from the database. The conversion to
 * something a person types happens here and nowhere else.
 *
 * Changing any of these is safe for stays already sold: every booking snapshots
 * its tax rate and its nightly prices when it is made, so nothing on this screen
 * can re-price a confirmed stay.
 */
export function SettingsScreen() {
  const { refresh } = useConsole()
  const loaded = useAsync(fetchSettings, [])
  const saving = useSaving(refresh)

  const [draft, setDraft] = useState<Settings | null>(null)

  useEffect(() => {
    if (loaded.data) setDraft(loaded.data)
  }, [loaded.data])

  function set<K extends keyof Settings>(key: K, value: Settings[K]) {
    setDraft((d) => (d ? { ...d, [key]: value } : d))
    saving.clear()
  }

  return (
    <Screen title="Settings" subtitle="The house rules the booking engine enforces.">
      {loaded.loading && <Loading what="settings" />}
      {loaded.error && <ErrorNote error={loaded.error} />}
      {saving.error && <ErrorNote error={saving.error} />}
      {saving.saved && <Saved>Saved.</Saved>}

      {draft && (
        <>
          <Section title="Stays">
            <Card>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  label="Shortest stay"
                  hint="Nights. A season can raise this; nothing lowers it."
                >
                  <Input
                    type="number"
                    min={1}
                    value={draft.defaultMinStay}
                    onChange={(e) => set('defaultMinStay', Number(e.target.value))}
                  />
                </Field>
                <Field
                  label="Longest stay"
                  hint="Beyond this, guests are told to contact you — the deposit split and cleaning a month-plus booking needs are not what this engine does."
                >
                  <Input
                    type="number"
                    min={1}
                    value={draft.maxStayNights}
                    onChange={(e) => set('maxStayNights', Number(e.target.value))}
                  />
                </Field>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Check-in from">
                  <Input
                    type="time"
                    value={draft.checkinTime}
                    onChange={(e) => set('checkinTime', e.target.value)}
                  />
                </Field>
                <Field label="Check-out by" hint="The departure-morning email quotes this.">
                  <Input
                    type="time"
                    value={draft.checkoutTime}
                    onChange={(e) => set('checkoutTime', e.target.value)}
                  />
                </Field>
              </div>
            </Card>
          </Section>

          <Section title="Money">
            <Card>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  label="Tax rate (%)"
                  hint="NH Meals & Rooms. Every booking keeps the rate in force when it was made."
                >
                  <Input
                    inputMode="decimal"
                    value={percent(draft.taxRateScaled)}
                    onChange={(e) => set('taxRateScaled', scaled(e.target.value))}
                  />
                </Field>
                <Field
                  label="Refund retention (%)"
                  hint="What the card processor keeps on the way in and does not return. Every refund keeps at least this much, so a cancellation never leaves the inn out of pocket."
                >
                  <Input
                    inputMode="decimal"
                    value={percent(draft.refundProcessingRateScaled)}
                    onChange={(e) => set('refundProcessingRateScaled', scaled(e.target.value))}
                  />
                </Field>
              </div>
            </Card>
          </Section>

          <Section title="Checkout">
            <Card>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  label="Hold (minutes)"
                  hint="How long a room is reserved while somebody pays. Too short and a slow card loses the room; too long and an abandoned checkout keeps it off sale."
                >
                  <Input
                    type="number"
                    min={1}
                    value={draft.holdTtlMinutes}
                    onChange={(e) => set('holdTtlMinutes', Number(e.target.value))}
                  />
                </Field>
                <Field
                  label="Payment grace (minutes)"
                  hint="How long a booking mid-payment is left alone. A guest working through a bank's security check can outlive the hold."
                >
                  <Input
                    type="number"
                    min={0}
                    value={draft.paymentGraceMinutes}
                    onChange={(e) => set('paymentGraceMinutes', Number(e.target.value))}
                  />
                </Field>
              </div>
            </Card>
          </Section>

          <Section title="Accessibility notice">
            <Card>
              <Aside>
                Shown with every search. Every room here requires stairs, and a guest with mobility
                needs must not find that out on arrival — this sentence is the only place the site
                says so.
              </Aside>
              <Textarea
                rows={4}
                value={draft.accessibilityNotice}
                onChange={(e) => set('accessibilityNotice', e.target.value)}
              />
            </Card>
          </Section>

          <Button
            kind="primary"
            onClick={() => saving.run(() => saveSettings(draft))}
            disabled={saving.working}
          >
            {saving.working ? 'Saving…' : 'Save settings'}
          </Button>
        </>
      )}
    </Screen>
  )
}

/**
 * Hundred-thousandths to a percentage and back.
 *
 * 8500 is 8.5%. The scaled integer is what crosses the wire and what the
 * database stores; the percentage exists only inside these two functions and the
 * box between them.
 */
function percent(scaledRate: number): string {
  return String(scaledRate / 1000)
}

function scaled(value: string): number {
  const parsed = Number.parseFloat(value.replace(/[^0-9.]/g, ''))
  return Number.isFinite(parsed) ? Math.round(parsed * 1000) : 0
}
