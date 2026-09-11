/**
 * A field no person sees, on the two public forms.
 *
 * The cheapest thing that keeps a script out of the owner's inbox: a text box
 * moved off the edge of the page, skipped by the keyboard and hidden from
 * screen readers, that a visitor never reaches and never fills in. A script
 * filling every box it finds fills this one, and the server discards the
 * message — still answering thank you, because a refusal is a signal to route
 * around and a thank-you is not.
 *
 * Off-screen rather than `display: none`, on purpose: the better scripts skip
 * fields the page does not display, and a box that is merely out of view still
 * looks to them like one to fill. `autoComplete="off"` keeps a browser from
 * putting a real person's saved address in it, which would make an honest
 * message vanish.
 *
 * The name is a plausible one, because the field's whole value is looking like
 * it belongs. It is not a URL anybody is asked for.
 */
export function Honeypot({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div aria-hidden="true" className="absolute -left-[9999px] top-0 h-px w-px overflow-hidden">
      <label>
        Website
        <input
          type="text"
          name="website"
          tabIndex={-1}
          autoComplete="off"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      </label>
    </div>
  )
}
