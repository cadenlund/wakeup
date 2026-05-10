// Tiny relative-time helper for chat list timestamps. Output:
//   - <60s          → "now"
//   - <60m          → "5m ago"
//   - <24h          → "2h ago"
//   - <7d           → "3d ago"
//   - this year     → "Mar 5"
//   - older         → "Mar 5, 24"
//
// "ago" is appended only on the short relative forms — adding it
// to "now" or to absolute dates ("Mar 5 ago") reads wrong.
//
// Pulling in date-fns / dayjs just for one rendering on a row felt
// like overkill — this stays inline until a second caller needs it.
const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

export function formatRelative(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const delta = now.getTime() - t;
  if (delta < MINUTE) return 'now';
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)}m ago`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)}h ago`;
  if (delta < WEEK) return `${Math.floor(delta / DAY)}d ago`;
  const d = new Date(t);
  const month = MONTHS[d.getMonth()];
  const day = d.getDate();
  if (d.getFullYear() === now.getFullYear()) return `${month} ${day}`;
  return `${month} ${day}, ${String(d.getFullYear()).slice(-2)}`;
}

const YEAR = 365 * DAY;

// Forward-looking duration helper for mute-until timestamps.
// Returns the *remaining* duration relative to now:
//   - <1m   → "for less than a minute"
//   - <1h   → "for 15 minutes"
//   - <1d   → "for 3 hours"
//   - <7d   → "for 2 days"
//   - <1y   → "for 3 weeks"
//   - >1y   → "indefinitely"
//   - past  → ""
//
// Backend stores "forever" as `2099-01-01` so anything more than
// a year out reads as indefinite (per §4.12). Singular vs plural
// is handled inline.
export function formatMutedUntil(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const delta = t - now.getTime();
  if (delta <= 0) return '';
  if (delta > YEAR) return 'indefinitely';
  if (delta < MINUTE) return 'for less than a minute';
  if (delta < HOUR) return forUnit(Math.floor(delta / MINUTE), 'minute');
  if (delta < DAY) return forUnit(Math.floor(delta / HOUR), 'hour');
  if (delta < WEEK) return forUnit(Math.floor(delta / DAY), 'day');
  return forUnit(Math.floor(delta / WEEK), 'week');
}

function forUnit(n: number, unit: string): string {
  return `for ${n} ${unit}${n === 1 ? '' : 's'}`;
}
