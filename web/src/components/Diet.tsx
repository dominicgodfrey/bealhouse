import type { MenuItem } from '../lib/site'

/**
 * The three dietary claims a dish can carry, in one place.
 *
 * One definition shared by the public menu, its key, and the console's toggle
 * buttons — so the letters on the page, the words in the key and the labels the
 * owner ticks can never drift apart. That drift is exactly what made passkey ids
 * unusable once already.
 *
 * **Letters rather than pictograms.** At the size a badge sits beside a dish
 * name, a leaf and a sprout are the same shape, and "is that vegan or
 * vegetarian?" is the one question these are meant to answer. GF / V / VG is
 * the convention a menu reader already knows, and the key below the menu spells
 * all three out.
 */
export const DIETS = [
  {
    key: 'glutenFree',
    abbr: 'GF',
    label: 'Gluten free',
    // Tailwind cannot see a class name assembled at runtime, so each is written
    // out whole.
    className: 'bg-amber-100 text-amber-900 ring-amber-300',
  },
  {
    key: 'vegetarian',
    abbr: 'V',
    label: 'Vegetarian',
    className: 'bg-green-100 text-green-900 ring-green-300',
  },
  {
    key: 'vegan',
    abbr: 'VG',
    label: 'Vegan',
    className: 'bg-sky-100 text-sky-900 ring-sky-300',
  },
] as const satisfies readonly {
  key: keyof Pick<MenuItem, 'glutenFree' | 'vegetarian' | 'vegan'>
  abbr: string
  label: string
  className: string
}[]

/**
 * The badges for one dish.
 *
 * Renders only what was ticked. An unmarked dish gets no badges and makes no
 * claim — the absence of a GF badge is not a statement that a dish contains
 * gluten, and the key says so out loud, because somebody with coeliac disease
 * may act on this.
 */
export function DietBadges({ item }: { item: MenuItem }) {
  const marks = DIETS.filter((d) => item[d.key])
  if (marks.length === 0) return null

  return (
    <span className="inline-flex shrink-0 gap-1 align-middle">
      {marks.map((d) => (
        <span
          key={d.key}
          className={`inline-flex h-5 min-w-5 items-center justify-center rounded px-1 text-[10px] font-semibold ring-1 ring-inset ${d.className}`}
        >
          {/*
            The letters are decorative once the full label is read out, so a
            screen reader gets "Gluten free" and not "G F".
          */}
          <span aria-hidden="true">{d.abbr}</span>
          <span className="sr-only">{d.label}</span>
        </span>
      ))}
    </span>
  )
}

/**
 * The key, at the foot of the menu.
 *
 * It carries the sentence that matters more than the letters do: an unmarked
 * dish has not been assessed, rather than been assessed and failed. Anything
 * else invites a guest to read a blank as a negative.
 */
export function DietKey() {
  return (
    <div className="flex flex-col items-center gap-3 border-t border-neutral-200 pt-6 text-center">
      <ul className="flex flex-wrap justify-center gap-x-6 gap-y-2">
        {DIETS.map((d) => (
          <li key={d.key} className="flex items-center gap-2 text-sm text-neutral-700">
            <span
              className={`inline-flex h-5 min-w-5 items-center justify-center rounded px-1 text-[10px] font-semibold ring-1 ring-inset ${d.className}`}
              aria-hidden="true"
            >
              {d.abbr}
            </span>
            {d.label}
          </li>
        ))}
      </ul>

      <p className="max-w-prose text-sm text-neutral-500">
        Please tell us about any allergies or dietary restrictions you may have when you come in,
        we will do our best to accommodate.
      </p>
    </div>
  )
}
