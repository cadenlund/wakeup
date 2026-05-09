// Small coloured circle indicating presence status, designed to sit
// on top of an avatar's bottom-right corner (or render inline next
// to a name where space is tight). Status comes from the presence
// service: online / away / sleeping / offline. Anything we don't
// recognise — including undefined while the presence query loads —
// renders as offline grey, so a row never goes blank during refetch.
//
// The white outer ring is what makes the dot pop when it's overlaid
// on a darker avatar — without it the green/amber bleeds into the
// avatar background and the affordance disappears.
import * as React from 'react';
import { View } from 'react-native';

import { cn } from '@/lib/utils';

export type PresenceStatus = 'online' | 'away' | 'sleeping' | 'offline';

type PresenceDotProps = {
  status: PresenceStatus | string | null | undefined;
  size?: number;
  ring?: boolean;
  className?: string;
};

const STATUS_BG: Record<PresenceStatus, string> = {
  online: 'bg-emerald-500',
  away: 'bg-amber-500',
  sleeping: 'bg-indigo-400',
  offline: 'bg-muted-foreground/40',
};

function normalize(status: PresenceDotProps['status']): PresenceStatus {
  if (status === 'online' || status === 'away' || status === 'sleeping') return status;
  return 'offline';
}

function PresenceDot({ status, size = 10, ring = true, className }: PresenceDotProps) {
  const s = normalize(status);
  const bg = STATUS_BG[s];
  // Ring is rendered as extra padding + bg-card on a wrapper; the
  // inner View carries the actual status colour. Keeps the visible
  // dot exactly `size` px regardless of whether ring is on.
  const inner = (
    <View
      style={{ width: size, height: size, borderRadius: size / 2 }}
      className={cn(bg)}
      accessibilityElementsHidden
      importantForAccessibility="no"
    />
  );
  if (!ring) {
    return <View className={className}>{inner}</View>;
  }
  const ringPad = 2;
  const ringSize = size + ringPad * 2;
  return (
    <View
      style={{
        width: ringSize,
        height: ringSize,
        borderRadius: ringSize / 2,
        padding: ringPad,
      }}
      className={cn('bg-card', className)}>
      {inner}
    </View>
  );
}

export { PresenceDot };
export type { PresenceDotProps };
