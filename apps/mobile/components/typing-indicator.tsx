// Phase 6.4 — typing indicator row for the conversation thread.
//
// Reads `useTypingUserIds` (the WS-fed typing store) and shows a
// pulsing three-dot animation. In a DM the dots stand alone (you
// already know who the peer is); in a group they're prefixed with
// who's typing ("{name}", "{a} and {b}", or "Several people").
// Renders nothing when nobody's typing — zero pixel cost in the
// common case. Sits between the message list and the composer.
import * as React from 'react';
import { Animated, View } from 'react-native';

import { Text } from '@/components/ui/text';
import type { InternalHandlerHttpConversationMemberRow } from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useTypingUserIds } from '@/lib/typing/store';

type Member = InternalHandlerHttpConversationMemberRow;

const DOT_MIN_OPACITY = 0.3;
const DOT_DURATION_MS = 360;
const DOT_STAGGER_MS = 180;

function nameFor(members: Member[] | undefined, userId: string): string {
  const u = members?.find((m) => m.user?.id === userId)?.user;
  return u?.display_name?.trim() || u?.username?.trim() || 'Someone';
}

function groupPrefix(members: Member[] | undefined, ids: string[]): string {
  if (ids.length === 1) return nameFor(members, ids[0]);
  if (ids.length === 2) return `${nameFor(members, ids[0])} and ${nameFor(members, ids[1])}`;
  return 'Several people';
}

// Three small circles whose opacity pulses in a staggered loop.
function TypingDots(): React.ReactElement {
  const color = useThemeColor('muted-foreground');
  // Lazily create the three Animated.Values once.
  const dotsRef = React.useRef<Animated.Value[] | null>(null);
  if (!dotsRef.current) {
    dotsRef.current = [
      new Animated.Value(DOT_MIN_OPACITY),
      new Animated.Value(DOT_MIN_OPACITY),
      new Animated.Value(DOT_MIN_OPACITY),
    ];
  }
  const dots = dotsRef.current;

  React.useEffect(() => {
    const loop = Animated.loop(
      Animated.stagger(
        DOT_STAGGER_MS,
        dots.map((d) =>
          Animated.sequence([
            Animated.timing(d, { toValue: 1, duration: DOT_DURATION_MS, useNativeDriver: true }),
            Animated.timing(d, {
              toValue: DOT_MIN_OPACITY,
              duration: DOT_DURATION_MS,
              useNativeDriver: true,
            }),
          ])
        )
      )
    );
    loop.start();
    return () => loop.stop();
  }, [dots]);

  return (
    <View className="flex-row items-center gap-1" accessibilityElementsHidden>
      {dots.map((d, i) => (
        <Animated.View
          key={i}
          style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: color, opacity: d }}
        />
      ))}
    </View>
  );
}

export function TypingIndicator({
  conversationId,
  members,
  isGroup,
}: {
  conversationId: string;
  members?: Member[];
  isGroup: boolean;
}): React.ReactElement | null {
  const ids = useTypingUserIds(conversationId);
  if (ids.length === 0) return null;

  const prefix = isGroup ? groupPrefix(members, ids) : undefined;
  const a11yLabel = isGroup ? `${prefix} is typing` : 'Typing';

  return (
    <View
      className="flex-row items-center gap-2 bg-background px-4 py-1.5"
      accessibilityLiveRegion="polite"
      accessibilityLabel={a11yLabel}
      testID="typing-indicator">
      {prefix ? <Text className="text-xs text-muted-foreground">{prefix}</Text> : null}
      <TypingDots />
    </View>
  );
}
