// Phase 6.5 — long-press context menu on a message bubble.
//
// Rows (in order):
//   - Copy        — copies the message body to the clipboard
//                   (expo-clipboard). Hidden for deleted rows
//                   (nothing to copy).
//   - React       — v2 stub. Toasts a neutral "coming soon" so the
//                   row reads as deliberate, not broken.
//   - Report      — no moderation backend yet; toasts a neutral
//                   acknowledgement. Hidden on the caller's own
//                   messages (you don't report yourself).
//   - Delete      — own, non-deleted messages only. Soft-deletes
//                   via DELETE /v1/messages/{id}; the parent owns
//                   the optimistic cache patch.
//
// Same DrawerSheet chrome as ConversationActionMenu — bottom drawer
// on native, centered modal card on web. The opening long-press
// fires haptics.tap() at the call site (MessageBubble), not here.
import * as Clipboard from 'expo-clipboard';
import { Copy, Flag, SmilePlus, Trash2 } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { DrawerSheet } from '@/components/ui/drawer-sheet';
import { Text } from '@/components/ui/text';
import { toast } from '@/lib/toast';
import { useThemeColor } from '@/lib/theme/use-theme-color';

// The bubble the menu was opened on. `mine` gates Delete + hides
// Report; `isDeleted` hides Copy. Body is what Copy writes.
export type MessageMenuTarget = {
  id: string;
  body: string;
  mine: boolean;
  isDeleted: boolean;
};

type Props = {
  target: MessageMenuTarget | null;
  onClose: () => void;
  // Caller owns the optimistic cache update + the DELETE call.
  onDelete: (messageId: string) => void;
  testID?: string;
};

export function MessageContextMenu({ target, onClose, onDelete, testID }: Props) {
  const fg = useThemeColor('foreground');
  const destructive = useThemeColor('destructive');
  const mutedFg = useThemeColor('muted-foreground');

  const handleCopy = React.useCallback(async () => {
    if (!target) return;
    try {
      await Clipboard.setStringAsync(target.body);
      toast.info('Copied');
    } catch {
      toast.error("Couldn't copy");
    } finally {
      onClose();
    }
  }, [target, onClose]);

  return (
    <DrawerSheet
      visible={!!target}
      onClose={onClose}
      dismissLabel="Dismiss message menu"
      testID={testID}>
      <View className="px-2 pb-6 pt-3">
        {target && !target.isDeleted ? (
          <Pressable
            onPress={handleCopy}
            accessibilityRole="button"
            accessibilityLabel="Copy message"
            testID="message-action-copy"
            className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
            <Copy size={18} color={fg} />
            <Text className="text-base">Copy</Text>
          </Pressable>
        ) : null}

        <Pressable
          onPress={() => {
            // Reactions ship in v2; acknowledge the tap so the row
            // doesn't read as a dead button.
            toast.info('Reactions coming soon');
            onClose();
          }}
          accessibilityRole="button"
          accessibilityLabel="React to message"
          testID="message-action-react"
          className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
          <SmilePlus size={18} color={fg} />
          <Text className="text-base">React</Text>
        </Pressable>

        {target && !target.mine ? (
          <Pressable
            onPress={() => {
              // No moderation backend yet — surface a neutral
              // acknowledgement. Wired to a real endpoint when
              // moderation lands.
              toast.info('Thanks — we’ll take a look.');
              onClose();
            }}
            accessibilityRole="button"
            accessibilityLabel="Report message"
            testID="message-action-report"
            className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
            <Flag size={18} color={fg} />
            <Text className="text-base">Report</Text>
          </Pressable>
        ) : null}

        {target && target.mine && !target.isDeleted ? (
          <Pressable
            onPress={() => {
              onDelete(target.id);
              onClose();
            }}
            accessibilityRole="button"
            accessibilityLabel="Delete message"
            testID="message-action-delete"
            className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
            <Trash2 size={18} color={destructive} />
            <Text style={{ color: destructive }} className="text-base font-medium">
              Delete
            </Text>
          </Pressable>
        ) : null}

        <Pressable
          onPress={onClose}
          accessibilityRole="button"
          accessibilityLabel="Cancel"
          testID="message-action-cancel"
          className="mt-2 items-center rounded-lg px-3 py-3 active:bg-muted">
          <Text style={{ color: mutedFg }} className="text-sm">
            Cancel
          </Text>
        </Pressable>
      </View>
    </DrawerSheet>
  );
}
