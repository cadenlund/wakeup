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
//
// Trailing column also hosts the three-dots overflow trigger
// that opens <ConversationActionMenu> (Phase 5.6). Discoverable
// affordance > long-press: most users won't know to long-press.
import { BellOff, MoreVertical, Pin } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { PresenceDot } from '@/components/presence-dot';
import { Avatar, StackedAvatars, type StackedMember } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { formatMutedUntil, formatRelative } from '@/lib/relative-time';
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
  // Presence to overlay on the (single) avatar — direct DMs use the
  // other member's status. Group rows pass presence down per
  // stackedMember instead and leave this undefined.
  presence?: string | null;
  lastMessageAt?: string | null;
  isMuted?: boolean;
  isPinned?: boolean;
  // Raw `muted_until` ISO string. When present and `isMuted` is
  // true, the row renders a "muted until <X>" / "muted indefinitely"
  // hint underneath the subtitle so users can see at a glance how
  // long their silence lasts (per spec §4.12). Undefined = no hint.
  mutedUntil?: string | null;
  onPress?: () => void;
  // When provided, renders a three-dots overflow button on the
  // trailing edge that fires this. Phase 5.6 wires it to the
  // conversation action menu (pin / mute).
  onMorePress?: () => void;
  testID?: string;
};

function ConversationRow({
  title,
  subtitle,
  avatarUrl,
  fallbackInitial,
  stackedMembers,
  presence,
  lastMessageAt,
  isMuted,
  isPinned,
  mutedUntil,
  onPress,
  onMorePress,
  testID,
}: ConversationRowProps) {
  const mutedFg = useThemeColor('muted-foreground');
  const primary = useThemeColor('primary');
  const Container = onPress ? Pressable : View;
  const containerProps = onPress
    ? {
        onPress,
        accessibilityRole: 'button' as const,
        accessibilityLabel: title,
      }
    : {};
  const stamp = formatRelative(lastMessageAt);
  const muteHint = isMuted ? formatMutedUntil(mutedUntil) : '';
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
        // Pinned rows get a subtle primary-tinted band + a thin
        // accent stripe on the leading edge so they read as "this
        // is sticky" rather than just "this row happens to have a
        // tiny pin icon next to the title."
        isPinned && 'border-l-2 border-l-primary bg-primary/5',
        onPress && 'active:bg-muted'
      )}>
      {showStacked ? (
        <StackedAvatars members={stackedMembers!} size={40} />
      ) : (
        <View className="relative">
          <Avatar source={avatarUrl} fallbackName={fallbackInitial ?? title} size={40} />
          {presence ? (
            <View className="absolute -bottom-0.5 -right-0.5">
              <PresenceDot status={presence} size={10} />
            </View>
          ) : null}
        </View>
      )}
      <View className="min-w-0 flex-1">
        <View className="flex-row items-center gap-1.5">
          {isPinned ? <Pin size={12} color={primary} fill={primary} /> : null}
          <Text numberOfLines={1} className="flex-shrink text-base font-medium">
            {title}
          </Text>
        </View>
        {subtitle ? (
          <Text numberOfLines={1} variant="muted" className="text-sm">
            {subtitle}
          </Text>
        ) : null}
        {isMuted ? (
          <View className="flex-row items-center gap-1 pt-0.5">
            <BellOff size={11} color={mutedFg} />
            <Text variant="muted" className="text-xs italic" numberOfLines={1}>
              {muteHint ? `Muted ${muteHint}` : 'Muted'}
            </Text>
          </View>
        ) : null}
      </View>
      <View className="shrink-0 flex-row items-center gap-1.5">
        {stamp ? (
          <Text variant="muted" className="text-xs">
            {stamp}
          </Text>
        ) : null}
        {onMorePress ? (
          <Pressable
            onPress={onMorePress}
            accessibilityRole="button"
            accessibilityLabel={`More options for ${title}`}
            testID={testID ? `${testID}-more` : undefined}
            hitSlop={10}
            className="rounded-full p-1 active:bg-muted">
            <MoreVertical size={18} color={mutedFg} />
          </Pressable>
        ) : null}
      </View>
    </Container>
  );
}

export { ConversationRow };
export type { ConversationRowProps };
