// One row in the conversations list. Avatar + identity column +
// trailing timestamp / mute / pin column. Tap routes into the
// conversation thread (stub at app/conversations/[id].tsx until
// Phase 5.2 builds the real thread screen).
//
// Direct vs group rendering:
//   - direct: avatar + display_name of the OTHER member.
//   - group:  conversation.avatar_url + conversation.name. If
//     name is missing we fall back to a comma-joined preview of
//     up to three member names so an unnamed group still has
//     something to read.
//
// Pin / mute icons are surfaced inline when the caller's
// membership has those flags set — the conversation list endpoint
// already returns the caller's pinned_at / muted_until on each
// row, so we don't need a side query.
import { BellOff, Pin } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { formatRelative } from '@/lib/relative-time';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { cn } from '@/lib/utils';

type ConversationRowProps = {
  title: string;
  subtitle?: string | null;
  avatarUrl?: string | null;
  fallbackInitial?: string | null;
  lastMessageAt?: string | null;
  isMuted?: boolean;
  isPinned?: boolean;
  onPress?: () => void;
  onLongPress?: () => void;
  testID?: string;
};

function ConversationRow({
  title,
  subtitle,
  avatarUrl,
  fallbackInitial,
  lastMessageAt,
  isMuted,
  isPinned,
  onPress,
  onLongPress,
  testID,
}: ConversationRowProps) {
  const mutedFg = useThemeColor('muted-foreground');
  const Container = onPress || onLongPress ? Pressable : View;
  const containerProps =
    onPress || onLongPress
      ? {
          onPress,
          onLongPress,
          accessibilityRole: 'button' as const,
          accessibilityLabel: title,
        }
      : {};
  const stamp = formatRelative(lastMessageAt);
  return (
    <Container
      {...containerProps}
      testID={testID}
      className={cn(
        'flex-row items-center gap-3 px-4 py-3',
        (onPress || onLongPress) && 'active:bg-muted'
      )}>
      <Avatar source={avatarUrl} fallbackName={fallbackInitial ?? title} size={48} />
      <View className="min-w-0 flex-1">
        <View className="flex-row items-center gap-1.5">
          {isPinned ? <Pin size={12} color={mutedFg} /> : null}
          <Text numberOfLines={1} className="flex-shrink text-base font-medium">
            {title}
          </Text>
          {isMuted ? <BellOff size={12} color={mutedFg} /> : null}
        </View>
        {subtitle ? (
          <Text numberOfLines={1} variant="muted" className="text-sm">
            {subtitle}
          </Text>
        ) : null}
      </View>
      {stamp ? (
        <Text variant="muted" className="shrink-0 text-xs">
          {stamp}
        </Text>
      ) : null}
    </Container>
  );
}

export { ConversationRow };
export type { ConversationRowProps };
