// Tap-to-pick emoji input for the status field. The bare <Input>
// it replaces was a keyboard text entry, which on mobile means
// users have to dig through their keyboard's emoji panel — clunky
// and easy to bail on. Instead we show the chosen emoji as a chip;
// tapping opens a modal with a curated grid of common "what I'm
// up to" emojis. Tap one to set + close, or "Clear" to wipe.
//
// Curated, not full-keyboard: the goal is a fast pick, not an
// emoji search engine. Categories cover the obvious status moods
// (sleep, food, work, play, vibes, travel) so most users find
// something in two taps.
import * as React from 'react';
import { Modal, Pressable, ScrollView, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { Text } from '@/components/ui/text';

const EMOJI_GROUPS: { label: string; emojis: string[] }[] = [
  { label: 'Sleep', emojis: ['🛌', '😴', '💤', '🌙', '⭐', '☁️'] },
  { label: 'Food + drink', emojis: ['☕', '🍕', '🍔', '🥗', '🍣', '🥤', '🍪', '🍷'] },
  { label: 'Work', emojis: ['💻', '📱', '🎧', '📚', '✏️', '📝', '💼', '🧠'] },
  { label: 'Play', emojis: ['🎮', '🎨', '🎵', '🎬', '📺', '🎲', '🎤', '🎸'] },
  { label: 'Body', emojis: ['🏃', '💪', '🧘', '🚴', '🏋️', '⛹️', '🤸', '🧗'] },
  { label: 'Vibes', emojis: ['😎', '🤔', '😊', '😂', '❤️', '🔥', '✨', '🥹'] },
  { label: 'Out', emojis: ['🚗', '✈️', '🏖️', '🌲', '🏔️', '🌆', '🍻', '🎉'] },
];

type Props = {
  value: string;
  onChange: (next: string) => void;
  disabled?: boolean;
  testID?: string;
};

export function StatusEmojiPicker({ value, onChange, disabled, testID }: Props) {
  const [open, setOpen] = React.useState(false);
  const insets = useSafeAreaInsets();

  const close = React.useCallback(() => setOpen(false), []);
  const pick = (emoji: string) => {
    onChange(emoji);
    close();
  };
  const clear = () => {
    onChange('');
    close();
  };

  return (
    <>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={
          value ? `Status emoji: ${value}. Tap to change.` : 'Pick a status emoji'
        }
        testID={testID}
        disabled={disabled}
        onPress={() => setOpen(true)}
        className={`flex-row items-center gap-3 rounded-md border border-input bg-background px-3 py-2 ${
          disabled ? 'opacity-50' : ''
        }`}>
        <View className="h-9 w-9 items-center justify-center rounded-md bg-muted">
          {value ? (
            <Text className="text-2xl">{value}</Text>
          ) : (
            <Text className="text-2xl opacity-40">🛌</Text>
          )}
        </View>
        <Text className={`flex-1 text-sm ${value ? 'text-foreground' : 'text-muted-foreground'}`}>
          {value ? 'Tap to change' : 'Pick an emoji'}
        </Text>
        {value ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Clear status emoji"
            hitSlop={10}
            onPress={clear}>
            <Text className="text-xs font-medium text-muted-foreground">Clear</Text>
          </Pressable>
        ) : null}
      </Pressable>

      <Modal visible={open} transparent animationType="fade" onRequestClose={close}>
        {/* Sibling layout: backdrop covers the screen for tap-to-
            dismiss, card sits on top inside its own View. Wrapping
            the card in a Pressable broke ScrollView gestures inside
            it, so the card MUST be a plain View. Safe-area padding
            keeps the Done button clear of the iPhone notch/home pill
            while a max-h cap leaves visible breathing room above and
            below — full-height looked oppressive on the test sim. */}
        <View
          className="flex-1 bg-black/50"
          style={{
            paddingTop: insets.top + 32,
            paddingBottom: insets.bottom + 32,
            paddingHorizontal: 16,
          }}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Dismiss emoji picker"
            onPress={close}
            className="absolute inset-0"
          />
          <View className="flex-1 items-center justify-center">
            <View
              className="w-full max-w-md overflow-hidden rounded-3xl bg-card shadow-2xl shadow-black/40"
              style={{ maxHeight: 480 }}>
              <View className="flex-row items-center justify-between px-5 pb-2 pt-4">
                <Text className="text-base font-semibold text-card-foreground">
                  What are you up to?
                </Text>
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Close emoji picker"
                  hitSlop={10}
                  onPress={close}>
                  <Text className="text-sm font-medium text-muted-foreground">Done</Text>
                </Pressable>
              </View>
              <ScrollView
                className="px-5"
                contentContainerClassName="gap-4 pb-6 pt-2"
                showsVerticalScrollIndicator>
                {EMOJI_GROUPS.map((group) => (
                  <View key={group.label} className="gap-2">
                    <Text variant="muted" className="text-xs uppercase">
                      {group.label}
                    </Text>
                    <View className="flex-row flex-wrap" style={{ marginHorizontal: -4 }}>
                      {group.emojis.map((emoji) => {
                        const isSelected = emoji === value;
                        return (
                          <Pressable
                            key={emoji}
                            accessibilityRole="button"
                            accessibilityLabel={`Pick ${emoji}`}
                            accessibilityState={{ selected: isSelected }}
                            onPress={() => pick(emoji)}
                            style={{ width: '12.5%', padding: 4 }}>
                            <View
                              className={`aspect-square items-center justify-center rounded-xl ${
                                isSelected ? 'bg-primary/15' : 'bg-muted'
                              }`}>
                              <Text className="text-2xl">{emoji}</Text>
                            </View>
                          </Pressable>
                        );
                      })}
                    </View>
                  </View>
                ))}
              </ScrollView>
            </View>
          </View>
        </View>
      </Modal>
    </>
  );
}
