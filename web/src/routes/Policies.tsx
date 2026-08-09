import { fetchPageCopy, fetchPolicyTerms, paragraphs, type PolicyTerms } from '../lib/site'
import { useAsync } from '../lib/useAsync'
import { ErrorNote, Layout, Loading, Prose } from '../components/Layout'
import { Gallery, fromPagePhotos } from '../components/Gallery'

/**
 * Beal House policies — the page the booking flow asks you to agree to.
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

  return (
    <Layout>
      <div className="flex flex-col gap-10">
        <h1 className="text-center text-3xl font-semibold tracking-tight sm:text-4xl">Beal House policies</h1>

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
          New Hampshire Meals &amp; Rooms tax of {terms.taxRatePercent}% is added to the room rate
          and to the pet fee. Every price you are shown before paying already includes it.
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

    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-4">
      {/* Left, with the rules under it. A centred heading over left-aligned
          body copy reads as two different pages stacked. */}
      <h2 className="border-b border-neutral-200 pb-2 text-2xl font-semibold tracking-tight">
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
