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

export { Avatar };
export type { AvatarProps };
