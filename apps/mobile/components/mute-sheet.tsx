// Mute-options bottom sheet for a conversation. Six rows: 15
// minutes, 1 hour, 8 hours, 24 hours, "Until I turn it back on"
// (= 2099-01-01 per backend convention), and Unmute (only when
// currently muted). Tapping an option resolves the parent's
// mute mutation with the right `until` timestamp and closes.
//
// Triggered from the conversation row's long-press menu (Phase
// 5.6) and — later — from the conversation header overflow menu
// (Phase 5.7+). The visual shape mirrors `status-emoji-picker`
// so the two custom sheets in the app feel like the same
// vocabulary.
import * as React from 'react';
import { Modal, Pressable, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { BellOff, Check } from 'lucide-react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

// "Until I turn it back on" — far-future stamp. Server treats
// any far-future timestamp as forever and the row label drops
// the relative time when `muted_until > 1y from now` (per §4.12).
const FOREVER = '2099-01-01T00:00:00Z';

type Option = {
  label: string;
  // Resolver runs at tap time so the offsets are computed against
  // a fresh `Date.now()` rather than baked in at render time —
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
  const insets = useSafeAreaInsets();
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');
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
          accessibilityLabel="Dismiss mute options"
          onPress={onClose}
          className="absolute inset-0"
        />
        <View className="flex-1 justify-end">
          <View
            testID={testID}
            className="overflow-hidden rounded-3xl bg-card shadow-2xl shadow-black/40">
            <View className="flex-row items-center gap-2 px-5 pb-2 pt-4">
              <BellOff size={16} color={mutedFg} />
              <Text className="text-base font-semibold text-card-foreground">
                Mute conversation
              </Text>
            </View>
            <Text variant="muted" className="px-5 pb-3 text-xs">
              You&apos;ll still see new messages. We just won&apos;t ping you about them.
            </Text>
            {OPTIONS.map((opt) => (
              <Pressable
                key={opt.label}
                accessibilityRole="button"
                accessibilityLabel={`Mute for ${opt.label}`}
                testID={`mute-option-${opt.label.toLowerCase().replace(/\s+/g, '-')}`}
                onPress={() => onPickUntil(opt.resolveUntil())}
                className="flex-row items-center justify-between border-t border-border px-5 py-4 active:bg-muted">
                <Text className="text-base">{opt.label}</Text>
                <Check size={16} color="transparent" />
              </Pressable>
            ))}
            {isMuted ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Unmute conversation"
                testID="mute-option-unmute"
                onPress={onUnmute}
                className="flex-row items-center gap-2 border-t border-border px-5 py-4 active:bg-muted">
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
              className="border-t border-border px-5 py-4 active:bg-muted">
              <Text className="text-center text-base text-muted-foreground">Cancel</Text>
            </Pressable>
          </View>
        </View>
      </View>
    </Modal>
  );
}
