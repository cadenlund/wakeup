// Shared "more actions" affordance for any row that represents a
// FRIEND. Both the friends tab and the global search modal use it
// so the user sees one menu vocabulary across surfaces.
//
//   - <FriendRowMenuButton>: the trailing 3-dots tap target.
//   - <FriendActionMenu>:    the bottom-sheet with Unfriend / Block.
//
// Wiring: parent owns `target` state. Tap the button → set target.
// Sheet renders open on non-null target. Picking an action calls
// the parent-supplied callback (which fires the mutation), then
// the parent clears target to dismiss.
import * as React from 'react';
import { MoreVertical, ShieldOff, UserMinus } from 'lucide-react-native';
import { Pressable, View } from 'react-native';

import { DrawerSheet } from '@/components/ui/drawer-sheet';
import { Text } from '@/components/ui/text';
import type { InternalHandlerHttpUserResponse } from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type UserRow = InternalHandlerHttpUserResponse;

export function FriendRowMenuButton({
  disabled,
  onPress,
  testID,
}: {
  disabled: boolean;
  onPress: () => void;
  testID?: string;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      accessibilityRole="button"
      accessibilityLabel="More actions"
      testID={testID ?? 'friend-row-menu'}
      hitSlop={6}
      className="h-8 w-8 items-center justify-center rounded-md active:bg-muted">
      <MoreVertical size={18} color={mutedFg} />
    </Pressable>
  );
}

export function FriendActionMenu({
  target,
  pendingAction,
  onClose,
  onUnfriend,
  onBlock,
}: {
  target: UserRow | null;
  // Set keyed by user_id; entries get the menu items disabled while
  // a mutation flight is in progress.
  pendingAction: Set<string>;
  onClose: () => void;
  onUnfriend: (u: UserRow) => void;
  onBlock: (u: UserRow) => void;
}) {
  const fg = useThemeColor('foreground');
  const destructive = useThemeColor('destructive');
  const mutedFg = useThemeColor('muted-foreground');
  const handle = target?.username ? `@${target.username}` : (target?.display_name ?? '');
  const inFlight = target?.id ? pendingAction.has(target.id) : false;
  return (
    <DrawerSheet visible={!!target} onClose={onClose}>
      <View className="px-4 pb-2 pt-3">
        <Text variant="muted" className="text-center text-sm">
          {handle}
        </Text>
      </View>
      <View className="px-2 pb-6">
        <Pressable
          onPress={() => target && onUnfriend(target)}
          disabled={inFlight}
          accessibilityRole="button"
          accessibilityLabel="Unfriend"
          testID="friend-menu-unfriend"
          className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
          <UserMinus size={18} color={fg} />
          <Text className="text-base">Unfriend</Text>
        </Pressable>
        <Pressable
          onPress={() => target && onBlock(target)}
          disabled={inFlight}
          accessibilityRole="button"
          accessibilityLabel="Block"
          testID="friend-menu-block"
          className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
          <ShieldOff size={18} color={destructive} />
          <Text style={{ color: destructive }} className="text-base font-medium">
            Block
          </Text>
        </Pressable>
        <Pressable
          onPress={onClose}
          accessibilityRole="button"
          accessibilityLabel="Cancel"
          testID="friend-menu-cancel"
          className="mt-2 items-center rounded-lg px-3 py-3 active:bg-muted">
          <Text style={{ color: mutedFg }} className="text-sm">
            Cancel
          </Text>
        </Pressable>
      </View>
    </DrawerSheet>
  );
}
