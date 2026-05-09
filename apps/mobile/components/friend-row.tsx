// One row in the friends list / requests list. Avatar with a
// presence dot overlay, identity column (display name + @username),
// and an optional trailing slot for actions (accept/decline buttons
// on a request, the friend's status emoji on an accepted row, etc).
//
// The row itself is a Pressable so future phases — Phase 4.6
// long-press menu (pin/mute/unfriend), Phase 5.x tap-to-DM — can
// hang behaviour off it without restructuring the layout. For
// Phase 4.2 the press handler is optional and most callers leave
// it absent.
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { PresenceDot, type PresenceStatus } from '@/components/presence-dot';
import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { cn } from '@/lib/utils';

type FriendRowProps = {
  displayName: string | null | undefined;
  username: string | null | undefined;
  avatarUrl?: string | null;
  statusEmoji?: string | null;
  // Pass undefined while presence is loading; the dot itself
  // normalises that to offline grey so the row never goes blank.
  presence?: PresenceStatus | string | null;
  // Render after the identity column (e.g. accept/decline button row).
  // When present the status emoji is hidden — actions take priority.
  trailing?: React.ReactNode;
  onPress?: () => void;
  onLongPress?: () => void;
  testID?: string;
  // Drop the presence-dot overlay on the avatar (e.g. on a request
  // row, where the relationship isn't accepted yet so presence isn't
  // meaningful).
  hidePresence?: boolean;
};

function FriendRow({
  displayName,
  username,
  avatarUrl,
  statusEmoji,
  presence,
  trailing,
  onPress,
  onLongPress,
  testID,
  hidePresence,
}: FriendRowProps) {
  const name = displayName?.trim() || username?.trim() || 'Unknown';
  const handle = username ? `@${username}` : undefined;
  const Container = onPress || onLongPress ? Pressable : View;
  const containerProps =
    onPress || onLongPress
      ? {
          onPress,
          onLongPress,
          accessibilityRole: 'button' as const,
          accessibilityLabel: name,
        }
      : {};
  return (
    <Container
      {...containerProps}
      testID={testID}
      className={cn(
        'flex-row items-center gap-3 px-4 py-3',
        (onPress || onLongPress) && 'active:bg-muted'
      )}>
      <View className="relative">
        <Avatar source={avatarUrl} fallbackName={displayName ?? username} size={40} />
        {hidePresence ? null : (
          <View className="absolute -bottom-0.5 -right-0.5">
            <PresenceDot status={presence} size={10} />
          </View>
        )}
      </View>

      <View className="min-w-0 flex-1">
        <View className="flex-row items-center gap-1.5">
          <Text numberOfLines={1} className="text-base font-medium">
            {name}
          </Text>
          {!trailing && statusEmoji ? (
            <Text numberOfLines={1} className="text-base">
              {statusEmoji}
            </Text>
          ) : null}
        </View>
        {handle ? (
          <Text numberOfLines={1} variant="muted" className="text-sm">
            {handle}
          </Text>
        ) : null}
      </View>

      {trailing ? <View className="shrink-0 flex-row items-center gap-2">{trailing}</View> : null}
    </Container>
  );
}

export { FriendRow };
export type { FriendRowProps };
