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

// Each inner avatar carries a 2px ring (rendered as padding around
// the Avatar inside <StackedSlot>); the wrapper's outer size is
// therefore inner + RING_PAD * 2. We need the wrapper size — not the
// raw inner size — to figure out the diagonal offset that keeps the
// cluster inside the outer square.
const RING_PAD = 2;

function StackedAvatars({ members, size = 48, className, testID }: StackedAvatarsProps) {
  // 0 → "G" placeholder so the row never goes blank during a flicker.
  // 1 → render the lone member at full outer size with a presence
  //     dot, identical to a direct-DM render. The earlier version
  //     dropped them in the bottom-left and left the rest of the
  //     square empty.
  // 2+ → the two-circle diagonal stack we actually came here for.
  if (members.length === 0) {
    return (
      <View
        testID={testID}
        style={{ width: size, height: size }}
        className={cn('items-center justify-center', className)}>
        <Avatar source={null} fallbackName="G" size={size} />
      </View>
    );
  }
  if (members.length === 1) {
    const only = members[0];
    const dotSize = Math.max(8, Math.round(size * 0.22));
    return (
      <View
        testID={testID}
        style={{ width: size, height: size }}
        className={cn('relative', className)}>
        <Avatar source={only.avatarUrl} fallbackName={only.fallbackName} size={size} />
        {only.presence ? (
          <View className="absolute -bottom-0.5 -right-0.5">
            <PresenceDot status={only.presence} size={dotSize} />
          </View>
        ) : null}
      </View>
    );
  }

  // Two-member diagonal layout. Wrapper size accounts for the ring
  // padding so the cluster fits flush within `size`.
  const inner = Math.round(size * 0.62);
  const wrapper = inner + RING_PAD * 2;
  const offset = size - wrapper;
  const dotSize = Math.max(8, Math.round(inner * 0.22));

  const a = members[0];
  const b = members[1];

  return (
    <View
      testID={testID}
      style={{ width: size, height: size }}
      className={cn('relative', className)}>
      {/* Bottom-left avatar — drawn first so the top-right one
          overlaps it on the diagonal. */}
      <StackedSlot
        left={0}
        top={offset}
        inner={inner}
        dotSize={dotSize}
        source={a.avatarUrl}
        fallbackName={a.fallbackName}
        presence={a.presence}
      />
      <StackedSlot
        left={offset}
        top={0}
        inner={inner}
        dotSize={dotSize}
        source={b.avatarUrl}
        fallbackName={b.fallbackName}
        presence={b.presence}
      />
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
