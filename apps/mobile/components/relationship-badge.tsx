// Tiny muted-text label that sits beside trailing affordances in
// search results. Used to surface the caller's relationship to a
// matched user — "Friend" for accepted friendships, "Added" for
// outgoing pending requests — so a row reads with Instagram-style
// vocabulary instead of relying on the action button alone.
//
// Renders as a plain Text so it slots into the existing row layout
// (no shadow, no border, no background) and stays unobtrusive next
// to the action pills.
import * as React from 'react';

import { Text } from '@/components/ui/text';

export function RelationshipBadge({ label }: { label: string }) {
  return (
    <Text variant="muted" className="text-xs">
      {label}
    </Text>
  );
}
