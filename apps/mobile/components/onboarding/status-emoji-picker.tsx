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

      <Modal
        visible={open}
        transparent
        animationType="fade"
        onRequestClose={close}
        statusBarTranslucent>
        {/* Visual chrome matches the DrawerSheet primitive used
            elsewhere — drag-handle pill, rounded-top card, bg-card
            surface, bg-black/40 backdrop. The picker uses its own
            Modal (instead of DrawerSheet) because the grid needs a
            ScrollView, and wrapping the card in Pressable breaks
            scroll gestures on iOS. */}
        <Pressable
          accessibilityLabel="Dismiss emoji picker"
          onPress={close}
          className="flex-1 justify-end bg-black/40">
          <View
            className="rounded-t-3xl bg-card"
            style={{ maxHeight: 480, paddingBottom: insets.bottom }}>
            <View className="items-center pt-3">
              <View className="h-1 w-12 rounded-full bg-muted-foreground/30" />
            </View>
            <View className="flex-row items-center justify-between px-5 pb-2 pt-3">
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
              contentContainerClassName="gap-4 pb-4 pt-2"
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
        </Pressable>
      </Modal>
    </>
  );
}
