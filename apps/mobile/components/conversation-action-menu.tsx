// Long-press menu on a conversation row (Phase 5.6). Two
// actions for v1:
//   - Pin / Unpin — toggles `pinned_at` on the caller's
//     membership. Optimistic on the parent; this component just
//     fires the callback.
//   - Mute / Unmute — for unmuted conversations, opens the
//     <MuteSheet> for picking a duration; for muted
//     conversations, unmutes directly so the long-press is one
//     tap, not three.
//
// Phase 5.7 will add Leave Group / Delete DM beneath these.
//
// Visual: a card pinned to the bottom safe-area, like
// status-emoji-picker — that's the established custom-sheet
// shape in this app (no bottom-sheet library installed).
import * as React from 'react';
import { Modal, Pressable, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
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
  const insets = useSafeAreaInsets();
  const fg = useThemeColor('foreground');
  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={onClose}>
      <View
        className="flex-1 bg-black/50"
        style={{
          paddingBottom: insets.bottom + 16,
          paddingTop: insets.top + 32,
          paddingHorizontal: 16,
        }}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Dismiss menu"
          onPress={onClose}
          className="absolute inset-0"
        />
        <View className="flex-1 justify-end">
          <View
            testID={testID}
            className="overflow-hidden rounded-3xl bg-card shadow-2xl shadow-black/40">
            <View className="px-5 pb-2 pt-4">
              <Text variant="muted" className="text-xs uppercase">
                Conversation
              </Text>
              <Text numberOfLines={1} className="text-base font-semibold text-card-foreground">
                {title}
              </Text>
            </View>

            <Pressable
              accessibilityRole="button"
              accessibilityLabel={isPinned ? 'Unpin conversation' : 'Pin conversation'}
              testID="action-pin"
              onPress={onTogglePin}
              className="flex-row items-center gap-3 border-t border-border px-5 py-4 active:bg-muted">
              {isPinned ? <PinOff size={18} color={fg} /> : <Pin size={18} color={fg} />}
              <Text className="text-base">{isPinned ? 'Unpin' : 'Pin to top'}</Text>
            </Pressable>

            <Pressable
              accessibilityRole="button"
              accessibilityLabel={isMuted ? 'Unmute conversation' : 'Mute conversation'}
              testID="action-mute"
              onPress={isMuted ? onUnmute : onMutePress}
              className="flex-row items-center gap-3 border-t border-border px-5 py-4 active:bg-muted">
              <BellOff size={18} color={fg} />
              <Text className="text-base">{isMuted ? 'Unmute' : 'Mute…'}</Text>
            </Pressable>

            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Cancel"
              testID="action-cancel"
              onPress={onClose}
              className="border-t border-border px-5 py-4 active:bg-muted">
              <Text className="text-center text-base text-muted-foreground">Cancel</Text>
            </Pressable>
          </View>
        </View>
      </View>
    </Modal>
  );
}
