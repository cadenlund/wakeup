// Apple-Messages-style time divider rendered between message
// bursts. The label is computed from the absolute ISO timestamp
// and formatted with progressively less precision the further
// back you go:
//
//   today        → "Today  10:23 AM"
//   yesterday    → "Yesterday  10:23 PM"
//   this week    → "Mon  10:23 PM"
//   this year    → "Mar 5,  10:23 PM"
//   older        → "Mar 5, 2024,  10:23 PM"
//
// The list builder (`<MessageList>`) inserts a divider between
// consecutive messages whose timestamps span more than
// AGGREGATE_GAP_MS, and one at the head of the list as long as the
// thread has at least one message.
import * as React from 'react';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// Minimum gap between consecutive message timestamps before we
// stamp a new divider. 30 minutes lines up with how Apple Messages
// behaves in practice — a tight back-and-forth reads as one
// burst, a pause of half an hour or more gets its own header.
export const AGGREGATE_GAP_MS = 30 * MINUTE;

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

function formatTimeOfDay(d: Date): string {
  let hours = d.getHours();
  const minutes = d.getMinutes();
  const period = hours >= 12 ? 'PM' : 'AM';
  hours = hours % 12;
  if (hours === 0) hours = 12;
  return `${hours}:${pad(minutes)} ${period}`;
}

function sameCalendarDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export function formatTimeDividerLabel(
  iso: string | null | undefined,
  now: Date = new Date()
): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const then = new Date(t);
  const timeOfDay = formatTimeOfDay(then);

  if (sameCalendarDay(then, now)) {
    return `Today  ${timeOfDay}`;
  }
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameCalendarDay(then, yesterday)) {
    return `Yesterday  ${timeOfDay}`;
  }
  const ageMs = now.getTime() - t;
  if (ageMs < 7 * DAY) {
    return `${WEEKDAYS[then.getDay()]}  ${timeOfDay}`;
  }
  const month = MONTHS[then.getMonth()];
  const day = then.getDate();
  if (then.getFullYear() === now.getFullYear()) {
    return `${month} ${day},  ${timeOfDay}`;
  }
  return `${month} ${day}, ${then.getFullYear()},  ${timeOfDay}`;
}

export function TimeDivider({ iso }: { iso: string | null | undefined }) {
  const label = formatTimeDividerLabel(iso);
  if (!label) return null;
  return (
    <View className="items-center py-3">
      <Text variant="muted" className="text-[11px] font-semibold tracking-wider">
        {label}
      </Text>
    </View>
  );
}
