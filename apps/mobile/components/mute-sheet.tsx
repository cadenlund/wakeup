// Mute-options bottom drawer for a conversation. Five durations
// (15 min · 1 hr · 8 hr · 24 hr · "Until I turn it back on" =
// 2099-01-01) plus an Unmute row when currently muted.
//
// Triggered from the conversation row's three-dots overflow menu
// (Phase 5.6) and — later — from the conversation header overflow
// (Phase 5.7+). Visual matches the friends-tab Unfriend/Block
// drawer so the two sheets read as one pattern.
import * as React from 'react';
import { Modal, Pressable, View } from 'react-native';
import { BellOff, Clock } from 'lucide-react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

// Far-future stamp. Server treats any far-future timestamp as
// forever and the row label drops the relative time when
// `muted_until > 1y from now` (per §4.12).
const FOREVER = '2099-01-01T00:00:00Z';

type Option = {
  label: string;
  // Resolver runs at tap time so offsets are computed against a
  // fresh `Date.now()` rather than baked in at render time —
  // matters when the sheet stays mounted across long sessions.
  resolveUntil: () => string;
};

const OPTIONS: Option[] = [
  { label: '15 minutes', resolveUntil: () => offsetIso(15 * 60 * 1000) },
  { label: '1 hour', resolveUntil: () => offsetIso(60 * 60 * 1000) },
  { label: '8 hours', resolveUntil: () => offsetIso(8 * 60 * 60 * 1000) },
  { label: '24 hours', resolveUntil: () => offsetIso(24 * 60 * 60 * 1000) },
  { label: 'Until I turn it back on', resolveUntil: () => FOREVER },
];

function offsetIso(ms: number): string {
  return new Date(Date.now() + ms).toISOString();
}

type Props = {
  visible: boolean;
  isMuted: boolean;
  onPickUntil: (until: string) => void;
  onUnmute: () => void;
  onClose: () => void;
  testID?: string;
};

export function MuteSheet({ visible, isMuted, onPickUntil, onUnmute, onClose, testID }: Props) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');
  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
      statusBarTranslucent>
      <Pressable
        accessibilityLabel="Dismiss mute options"
        onPress={onClose}
        className="flex-1 justify-end bg-black/40">
        <Pressable onPress={() => {}} className="rounded-t-3xl bg-card" testID={testID}>
          <View className="items-center pt-3">
            <View className="h-1 w-12 rounded-full bg-muted-foreground/30" />
          </View>
          <View className="px-4 pb-1 pt-3">
            <Text className="text-center text-base font-semibold">Mute conversation</Text>
            <Text variant="muted" className="pt-1 text-center text-xs">
              You&apos;ll still see new messages. We just won&apos;t ping you about them.
            </Text>
          </View>
          <View className="px-2 pb-6">
            {OPTIONS.map((opt) => (
              <Pressable
                key={opt.label}
                accessibilityRole="button"
                accessibilityLabel={`Mute for ${opt.label}`}
                testID={`mute-option-${opt.label.toLowerCase().replace(/\s+/g, '-')}`}
                onPress={() => onPickUntil(opt.resolveUntil())}
                className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
                <Clock size={18} color={fg} />
                <Text className="text-base">{opt.label}</Text>
              </Pressable>
            ))}
            {isMuted ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Unmute conversation"
                testID="mute-option-unmute"
                onPress={onUnmute}
                className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
                <BellOff size={18} color={destructive} />
                <Text style={{ color: destructive }} className="text-base font-medium">
                  Unmute
                </Text>
              </Pressable>
            ) : null}
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Cancel"
              testID="mute-option-cancel"
              onPress={onClose}
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
