import { useEffect } from 'react'
import { useLocation } from 'react-router'

import { inn } from '../lib/contact'
import { fetchPageCopy, fetchPolicyTerms, paragraphs, type PolicyTerms } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'
import { Gallery, fromPagePhotos } from '../components/Gallery'

/**
 * The Beal House policies — the page the booking flow asks you to agree to.
 *
 * **The rules with numbers in them are not written here and are not written in
 * the console.** They are read from `/api/policies`, which reads settings and
 * the pricing package — the same values `pricing.Quote` and `pricing.Refund`
 * use to decide what happens to a guest's money. A deposit split typed into a
 * text box drifts from the code the first time somebody changes one and not the
 * other, and the guest has the stale one in writing, having been asked to tick
 * a box next to it.
 *
 * What the console owns is the prose: smoking, stairs, and whatever else the
 * owner needs to say. That half has no numbers in it on purpose.
 */
export function Policies() {
  const copy = useAsync(() => fetchPageCopy('policies'), [])
  const terms = useAsync(fetchPolicyTerms, [])

  // The forms and the confirm page link to `#privacy`. The browser tries the
  // anchor when the document loads, which is before the sections exist — they
  // render once the terms arrive — so the jump is made again at that moment.
  const { hash } = useLocation()
  useEffect(() => {
    if (!terms.data || !hash) return
    document.getElementById(hash.slice(1))?.scrollIntoView()
  }, [terms.data, hash])

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <h1 className="text-center text-3xl font-semibold tracking-tight sm:text-4xl">The Beal House policies</h1>

        {copy.data && (
          <>
            {/*
              The page editor offers a gallery on every page, so this renders
              one — a control in the console that quietly does nothing is worse
              than a picture nobody adds.
            */}
            <Gallery photos={fromPagePhotos(copy.data.photos)} />
            {/*
              Left-aligned, unlike the marketing pages. This is a document to be
              read down rather than a page to be looked at, and the rules below
              are left-aligned — a centred preamble on top of them read as a
              different page entirely.
            */}
            <Prose
              heading={copy.data.heading}
              paragraphs={paragraphs(copy.data.body)}
              align="left"
            />
          </>
        )}

        {terms.loading && <Loading what="the booking rules" />}
        {terms.error && <ErrorNote error={terms.error} />}
        {terms.data && <Rules terms={terms.data} />}
      </div>
    </Layout>
  )
}

function Rules({ terms }: { terms: PolicyTerms }) {
  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-8">
      <Section title="Booking a room">
        <Rule label="Length of stay">
          The shortest stay is {nights(terms.minStayNights)}. Some dates carry a longer minimum,
          and the date picker greys out anything it cannot sell you. The longest stay you can book
          here is {nights(terms.maxStayNights)} — for anything longer, please contact the inn and
          we will do our best to accommodate your needs.
        </Rule>
        <Rule label="Arriving and leaving">
          Check-in from {clock(terms.checkinTime)}, check-out by {clock(terms.checkoutTime)}.
        </Rule>
        <Rule label="Holding a room">
          Choosing a room holds it for {terms.holdMinutes} minutes while you pay. If the payment is
          not completed in that time the room goes back on sale.
        </Rule>
        <Rule label="Tax">
          New Hampshire Meals &amp; Rooms tax of {terms.taxRatePercent}% is added to the room
          rate. There are no other fees, and every price you are shown before paying already
          includes the tax.
        </Rule>
      </Section>

      <Section title="Paying">
        <Rule label="Deposit">
          A deposit of {terms.depositPercent}% of the total is taken when you book. The balance is
          charged automatically to the same card {days(terms.balanceLeadDays)} before you arrive,
          and we email you the day before that happens.
        </Rule>
        <Rule label="Arriving soon">
          If you book within {days(terms.shortNoticeDays)} of arrival there is no time for that
          schedule, so the stay is charged in full at the time of booking.
        </Rule>
      </Section>

      <Section title="Changing your mind">
        <Rule label="Cancelling">
          Cancel more than {days(terms.freeCancellationLeadDays)} before you arrive and you are
          refunded in full, less the card processing cost below. Cancel inside{' '}
          {days(terms.freeCancellationLeadDays)} and the deposit is kept; anything paid above it is
          returned.
        </Rule>
        <Rule label="Processing cost">
          Our card processor keeps its fee — {terms.refundProcessingPercent}% — on a payment even
          when it is refunded, so that much is retained on any refund. It is not a charge we
          receive.
        </Rule>
        <Rule label="Once your stay has started">
          A stay that has already begun cannot be cancelled here. Please speak to us.
        </Rule>
        <Rule label="If we cannot honour a booking">
          In the rare case we cannot give you the room you booked, you are refunded in full,
          including the processing cost.
        </Rule>
      </Section>

      {/*
        The privacy policy, as one section of this page rather than a page of
        its own. Written from what the system actually keeps — the guest row,
        the Stripe reference, the two forms, the console's notes — and naming
        only the third parties this site has. A generic policy that mentions
        cookies this site never sets would be the same kind of lie as an
        invented sentence about the food. Deletion is a person, not a button:
        the guest writes or rings and the owner removes the rows, which is the
        honest description of a seven-room inn.
      */}
      <Section id="privacy" title="Your details">
        <Rule label="What we keep">
          Your name, email address and telephone number, with the dates, room, price and tax rate
          of each booking; what you send through the contact and events forms; and any short note
          we make about your stay so a returning guest is remembered. Card details go straight to
          Stripe, our card processor, and never reach us — we keep only Stripe's reference and
          the amount. Where a balance is due later, Stripe holds the card, not us.
        </Rule>
        <Rule label="Who sees it">
          The people who run the inn; Stripe, for payments; and our email provider, for the
          messages we send you about a booking. We do not sell or share your details, we send no
          newsletter, and this site sets no cookies and runs no trackers for visitors. Stripe's
          own payment form may set its cookies on the payment page.
        </Rule>
        <Rule label="How long">
          Booking and payment records for as long as tax and accounting law requires. Messages
          and notes until they are no longer useful. Server logs are overwritten as they fill,
          and backups are discarded after two weeks.
        </Rule>
        <Rule label="The link in your confirmation">
          It shows your booking and can cancel it, and anyone holding it can do both until it
          expires — so do not forward the email.
        </Rule>
        <Rule label="Seeing, correcting or deleting your details">
          Email{' '}
          <a href={`mailto:${inn.email}`} className="underline underline-offset-4 hover:text-neutral-900">
            {inn.email}
          </a>{' '}
          or call{' '}
          <a href={inn.phoneHref} className="underline underline-offset-4 hover:text-neutral-900">
            {inn.phone}
          </a>
          . Once we have checked the request is yours, we will tell you what we hold, correct it,
          or delete it — your name, contact details, messages and notes — keeping only the
          amounts and dates of payments the law requires us to keep, with your details removed.
        </Rule>
      </Section>
    </div>
  )
}

/** `id` is an anchor: the forms and the confirm page link straight to a section. */
function Section({ id, title, children }: { id?: string; title: string; children: React.ReactNode }) {
  return (
    <section id={id} className="flex flex-col gap-4 scroll-mt-6">
      {/* Left, with the rules under it. A centred heading over left-aligned
          body copy reads as two different pages stacked. */}
      <h2 className="border-b border-sienna-line pb-2 text-2xl font-semibold tracking-tight">
        {title}
      </h2>
      <dl className="flex flex-col gap-4">{children}</dl>
    </section>
  )
}

function Rule({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="font-medium">{label}</dt>
      <dd className="text-neutral-700">{children}</dd>
    </div>
  )
}

const nights = (n: number) => `${n} ${n === 1 ? 'night' : 'nights'}`
const days = (n: number) => `${n} ${n === 1 ? 'day' : 'days'}`

/** "15:00" as "3pm" — the register the rest of the site writes in. */
function clock(hhmm: string): string {
  const [h, m] = hhmm.split(':').map(Number)
  if (Number.isNaN(h)) return hhmm

  const suffix = h < 12 ? 'am' : 'pm'
  const hour = h % 12 === 0 ? 12 : h % 12
  return m === 0 ? `${hour}${suffix}` : `${hour}.${String(m).padStart(2, '0')}${suffix}`
}
