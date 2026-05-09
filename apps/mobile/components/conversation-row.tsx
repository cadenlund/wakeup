// One row in the conversations list. Avatar + identity column +
// trailing timestamp / mute / pin column. Tap routes into the
// conversation thread (stub at app/conversations/[id].tsx until
// Phase 5.2 builds the real thread screen).
//
// Direct vs group rendering:
//   - direct: avatar + display_name of the OTHER member.
//   - group with avatar_url: conversation.avatar_url + name (or
//     comma-joined member preview for unnamed groups).
//   - group without avatar_url: stacked-avatars cluster of two
//     member avatars overlapping diagonally so the cell still
//     reads as "this is a group" without showing a placeholder
//     "G" chip.
//
// Pin / mute icons are surfaced inline when the caller's
// membership has those flags set — the conversation list endpoint
// already returns the caller's pinned_at / muted_until on each
// row, so we don't need a side query.
import { BellOff, Pin } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { Avatar, StackedAvatars, type StackedMember } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { formatRelative } from '@/lib/relative-time';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { cn } from '@/lib/utils';

type ConversationRowProps = {
  title: string;
  subtitle?: string | null;
  avatarUrl?: string | null;
  fallbackInitial?: string | null;
  // Group fallback: when avatarUrl is empty and stackedMembers has
  // at least one entry, the row renders <StackedAvatars> instead of
  // the single-initial fallback chip.
  stackedMembers?: StackedMember[];
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
  stackedMembers,
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
  // Avatar branch:
  //   1. avatarUrl present → single image (most precise — the user
  //      uploaded a deliberate group photo or it's a DM).
  //   2. no avatarUrl, stackedMembers given → cluster fallback.
  //   3. neither → single initial chip via Avatar's own fallback.
  const showStacked = !avatarUrl && (stackedMembers?.length ?? 0) > 0;
  return (
    <Container
      {...containerProps}
      testID={testID}
      className={cn(
        'flex-row items-center gap-3 px-4 py-3',
        (onPress || onLongPress) && 'active:bg-muted'
      )}>
      {showStacked ? (
        <StackedAvatars members={stackedMembers!} size={48} />
      ) : (
        <Avatar source={avatarUrl} fallbackName={fallbackInitial ?? title} size={48} />
      )}
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
