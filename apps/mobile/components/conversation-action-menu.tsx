// Three-dots overflow menu on a conversation row (Phase 5.6).
// Two actions for v1:
//   - Pin / Unpin — toggles `pinned_at` on the caller's
//     membership. Optimistic on the parent; this component just
//     fires the callback.
//   - Mute / Unmute — for unmuted conversations, opens the
//     <MuteSheet> for picking a duration; for muted
//     conversations, unmutes directly so it's one tap, not three.
//
// Phase 5.7 will add Leave Group / Delete DM beneath these.
//
// Visual matches the friends-tab Unfriend/Block drawer
// (FriendActionMenu) — rounded-top card pinned to the bottom
// safe-area, drag-handle pill, padded row buttons. Same
// vocabulary as that drawer so the two sheets feel like one
// pattern rather than two custom modals.
import * as React from 'react';
import { Modal, Pressable, View } from 'react-native';
import { BellOff, Pin, PinOff } from 'lucide-react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type Props = {
  visible: boolean;
  title: string;
  isPinned: boolean;
  isMuted: boolean;
  onTogglePin: () => void;
  // Tap on Mute when not muted: parent should switch to the
  // MuteSheet. Tap on Unmute when muted: parent should clear
  // muted_until directly (PATCH with `until: null`).
  onMutePress: () => void;
  onUnmute: () => void;
  onClose: () => void;
  testID?: string;
};

export function ConversationActionMenu({
  visible,
  title,
  isPinned,
  isMuted,
  onTogglePin,
  onMutePress,
  onUnmute,
  onClose,
  testID,
}: Props) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
      statusBarTranslucent>
      <Pressable
        accessibilityLabel="Dismiss menu"
        onPress={onClose}
        className="flex-1 justify-end bg-black/40">
        {/* Inner Pressable absorbs taps so they don't bubble to the
            backdrop and dismiss the sheet. */}
        <Pressable onPress={() => {}} className="rounded-t-3xl bg-card" testID={testID}>
          <View className="items-center pt-3">
            <View className="h-1 w-12 rounded-full bg-muted-foreground/30" />
          </View>
          <View className="px-4 pb-2 pt-3">
            <Text variant="muted" className="text-center text-sm" numberOfLines={1}>
              {title}
            </Text>
          </View>
          <View className="px-2 pb-6">
            <Pressable
              onPress={onTogglePin}
              accessibilityRole="button"
              accessibilityLabel={isPinned ? 'Unpin conversation' : 'Pin conversation'}
              testID="action-pin"
              className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
              {isPinned ? <PinOff size={18} color={fg} /> : <Pin size={18} color={fg} />}
              <Text className="text-base">{isPinned ? 'Unpin' : 'Pin to top'}</Text>
            </Pressable>
            <Pressable
              onPress={isMuted ? onUnmute : onMutePress}
              accessibilityRole="button"
              accessibilityLabel={isMuted ? 'Unmute conversation' : 'Mute conversation'}
              testID="action-mute"
              className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
              <BellOff size={18} color={fg} />
              <Text className="text-base">{isMuted ? 'Unmute' : 'Mute…'}</Text>
            </Pressable>
            <Pressable
              onPress={onClose}
              accessibilityRole="button"
              accessibilityLabel="Cancel"
              testID="action-cancel"
              className="mt-2 items-center rounded-lg px-3 py-3 active:bg-muted">
              <Text style={{ color: mutedFg }} className="text-sm">
                Cancel
              </Text>
            </Pressable>
          </View>
        </Pressable>
      </Pressable>
    </Modal>
  );
}
