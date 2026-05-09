// Display-only avatar primitive. Renders the user's `avatar_url`
// through expo-image (cached on memory + disk so a friends list
// doesn't re-download the same row's avatar on every refocus), or
// falls back to a coloured initial chip when the URL is missing.
//
// avatar-picker.tsx is the editable variant (tap to upload / take
// photo / remove). Anywhere we just want to *show* an avatar —
// friend list rows, message bubbles, mention chips — composes this
// instead of rolling another <Image> wrapper.
import { Image } from 'expo-image';
import * as React from 'react';
import { View, type ViewStyle } from 'react-native';

import { PresenceDot } from '@/components/presence-dot';
import { Text } from '@/components/ui/text';
import { cn } from '@/lib/utils';

type AvatarProps = {
  source?: string | null;
  // Used for the initial-letter fallback. Prefer display_name; pass
  // username as a secondary so the chip is still informative when
  // someone hasn't set a display name yet.
  fallbackName?: string | null;
  size?: number;
  className?: string;
  testID?: string;
};

function initialFor(name: string | null | undefined): string {
  if (!name) return '?';
  const trimmed = name.trim();
  if (!trimmed) return '?';
  // First glyph (handles emoji + non-ASCII without slicing a surrogate
  // pair in half).
  return Array.from(trimmed)[0]!.toUpperCase();
}

function Avatar({ source, fallbackName, size = 40, className, testID }: AvatarProps) {
  const dim = { width: size, height: size, borderRadius: size / 2 };
  const hasImage = !!source;
  return (
    <View
      testID={testID}
      style={dim as ViewStyle}
      className={cn('items-center justify-center overflow-hidden bg-muted', className)}>
      {hasImage ? (
        <Image
          source={{ uri: source! }}
          style={dim}
          contentFit="cover"
          cachePolicy="memory-disk"
          transition={120}
          accessible={false}
        />
      ) : (
        <Text
          style={{ fontSize: size * 0.42, lineHeight: size * 0.5, includeFontPadding: false }}
          className="font-semibold text-foreground/80">
          {initialFor(fallbackName)}
        </Text>
      )}
    </View>
  );
}

// Group-avatar fallback: two member avatars overlapping diagonally
// (top-right + bottom-left) inside a square the same outer size as a
// single avatar. The white ring around each inner avatar pops them
// off whatever surface they're rendered on (matches PresenceDot's
// ring trick). For groups with more than two members we don't try to
// fan out a third — the row's subtitle already announces the count
// ("3 members"), the avatar just needs to read as "this is a group".
//
// Used when conversation.avatar_url is missing on a group row; when
// avatar_url is set, callers render the regular <Avatar> instead.
//
// Each stacked member can carry an optional presence status so the
// conversation list can show "who's online right now" at a glance
// even on a group fallback. Statuses bubble up from the conversation
// list response join with /v1/presence/friends.
type StackedMember = {
  avatarUrl?: string | null;
  fallbackName?: string | null;
  // Same string union PresenceDot accepts, kept loose so it tolerates
  // an unfamiliar string from the server without crashing.
  presence?: string | null;
};

type StackedAvatarsProps = {
  members: StackedMember[];
  size?: number;
  className?: string;
  testID?: string;
};

function StackedAvatars({ members, size = 48, className, testID }: StackedAvatarsProps) {
  // The two highlighted members. We render at most two — the
  // count-in-subtitle handles communicating "more".
  const a = members[0];
  const b = members[1];

  // Inner avatar size = ~62% of outer, ring inset 2px on each side.
  const inner = Math.round(size * 0.62);
  const offset = size - inner;
  // Dot size scales with the inner avatar (~22% with a floor of 8px
  // so it still reads at very small sizes).
  const dotSize = Math.max(8, Math.round(inner * 0.22));

  return (
    <View
      testID={testID}
      style={{ width: size, height: size }}
      className={cn('relative', className)}>
      {/* Bottom-left avatar — drawn first so the top-right one
          overlaps it on the diagonal. */}
      {a ? (
        <StackedSlot
          left={0}
          top={offset}
          inner={inner}
          dotSize={dotSize}
          source={a.avatarUrl}
          fallbackName={a.fallbackName}
          presence={a.presence}
        />
      ) : null}
      {b ? (
        <StackedSlot
          left={offset}
          top={0}
          inner={inner}
          dotSize={dotSize}
          source={b.avatarUrl}
          fallbackName={b.fallbackName}
          presence={b.presence}
        />
      ) : // Group of one (rare but possible during creation flicker) —
      // fall back to a single avatar so the cell isn't half-empty.
      a ? null : (
        <Avatar source={null} fallbackName="G" size={size} />
      )}
    </View>
  );
}

function StackedSlot({
  left,
  top,
  inner,
  dotSize,
  source,
  fallbackName,
  presence,
}: {
  left: number;
  top: number;
  inner: number;
  dotSize: number;
  source?: string | null;
  fallbackName?: string | null;
  presence?: string | null;
}) {
  return (
    <View style={{ position: 'absolute', left, top }} className="rounded-full bg-card p-[2px]">
      <View className="relative">
        <Avatar source={source} fallbackName={fallbackName} size={inner} />
        {presence ? (
          <View className="absolute -bottom-0.5 -right-0.5">
            <PresenceDot status={presence} size={dotSize} />
          </View>
        ) : null}
      </View>
    </View>
  );
}

export { Avatar, StackedAvatars };
export type { AvatarProps, StackedAvatarsProps, StackedMember };
