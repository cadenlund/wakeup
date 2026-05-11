// Phase 6.4 — typing indicator for the conversation thread.
//
// Renders the WS-fed typing state (`useTypingUserIds`) as an
// incoming message bubble: the exact "theirs" chrome — `bg-card`,
// `rounded-2xl rounded-bl-sm`, `px-3 py-2`, the group avatar gutter
// — sized to a single-line bubble (the dots sit in a 24px row). Sits
// below the message list, above the composer; the list is `flex-1`
// so it shrinks to make room when the bubble appears. Inside: a
// staggered three-dot pulse. In a DM the dots stand alone (you know
// the peer); in a group they get a "{name}" / "{a} and {b}" /
// "Several people" label above, like a sender label. Renders nothing
// when quiet.
import * as React from 'react';
import { AccessibilityInfo, Animated, View } from 'react-native';

import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import type { InternalHandlerHttpConversationMemberRow } from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useTypingUserIds } from '@/lib/typing/store';

type Member = InternalHandlerHttpConversationMemberRow;

const DOT_MIN_OPACITY = 0.3;
const DOT_DURATION_MS = 360;
const DOT_STAGGER_MS = 180;

function userFor(members: Member[] | undefined, userId: string) {
  return members?.find((m) => m.user?.id === userId)?.user;
}
function nameFor(members: Member[] | undefined, userId: string): string {
  const u = userFor(members, userId);
  return u?.display_name?.trim() || u?.username?.trim() || 'Someone';
}

function groupLabel(members: Member[] | undefined, ids: string[]): string {
  if (ids.length === 1) return nameFor(members, ids[0]);
  if (ids.length === 2) return `${nameFor(members, ids[0])} and ${nameFor(members, ids[1])}`;
  return 'Several people';
}

// Three small circles whose opacity pulses in a staggered loop.
// Honours the OS "reduce motion" setting — when it's on the dots
// stay fully visible and the loop never starts (matches the
// reduced-motion handling elsewhere in the app).
function TypingDots(): React.ReactElement {
  const color = useThemeColor('muted-foreground');
  const [reduceMotion, setReduceMotion] = React.useState(false);
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
    let mounted = true;
    void AccessibilityInfo.isReduceMotionEnabled().then((enabled) => {
      if (mounted) setReduceMotion(enabled);
    });
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduceMotion);
    return () => {
      mounted = false;
      sub.remove();
    };
  }, []);

  React.useEffect(() => {
    if (reduceMotion) return;
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
  }, [dots, reduceMotion]);

  return (
    <View className="flex-row items-center gap-1" accessibilityElementsHidden>
      {dots.map((d, i) => (
        <Animated.View
          key={i}
          style={{
            width: 6,
            height: 6,
            borderRadius: 3,
            backgroundColor: color,
            opacity: reduceMotion ? 1 : d,
          }}
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

  const label = isGroup ? groupLabel(members, ids) : undefined;
  // In a group, show the typing user's avatar in the gutter (the
  // first one, when several are typing) — same slot/size as a
  // "theirs" message bubble's avatar.
  const headUser = isGroup ? userFor(members, ids[0]) : undefined;

  return (
    // Mirrors <MessageBubble>'s "theirs" row: avatar gutter in groups
    // so the bubble lines up with incoming messages; left-aligned.
    <View
      className="flex-row items-end gap-2 px-3 py-1"
      accessibilityLiveRegion="polite"
      accessibilityLabel={label ? `${label} is typing` : 'Typing'}
      testID="typing-indicator">
      {isGroup ? (
        <View className="w-8">
          <Avatar source={headUser?.avatar_url} fallbackName={nameFor(members, ids[0])} size={32} />
        </View>
      ) : null}
      <View className="max-w-[80%] items-start">
        {label ? (
          <Text variant="muted" className="mb-0.5 px-1 text-xs">
            {label}
          </Text>
        ) : null}
        {/* Same chrome AND footprint as a one-line "theirs" bubble:
            `bg-card`, `px-3 py-2`, and the dots sit in a 24px row —
            text-base's line height — so the bubble is exactly the
            size of a single-line incoming message. */}
        <View className="rounded-2xl rounded-bl-sm bg-card px-3 py-2">
          <View className="h-6 justify-center">
            <TypingDots />
          </View>
        </View>
      </View>
    </View>
  );
}
