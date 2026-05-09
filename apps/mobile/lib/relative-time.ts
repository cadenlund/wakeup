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
