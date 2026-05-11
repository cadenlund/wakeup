// Phase 6.2 — composer pinned at the bottom of the thread.
//
// Owns the draft text state, fires the optimistic-send hook on
// submit, and clears the input the moment the request is in flight
// (the placeholder bubble shows up in <MessageList> immediately
// via cache prepend, so the input feels like it "sent" instantly).
//
// On native, an autogrow multiline TextInput grows up to a small
// max-line cap and scrolls internally past that. The KeyboardAvoid
// behaviour lives on the screen, not here — the composer is just
// a row.
//
// Backend caps body at 10000 chars (§schema); we mirror that with
// `maxLength` so the user never sends something the server is
// guaranteed to reject.
import { Send } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, TextInput, View } from 'react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

const MAX_LENGTH = 10000;
// Hard upper bound on the visual height. Past this the input
// scrolls internally rather than pushing the thread out of view.
const MAX_HEIGHT = 120;

type Props = {
  // Caller wires this to useSendMessage's `send`. Composer trims
  // and noops on empty before calling — kept inside the hook for
  // symmetry, but a defensive guard here means the disabled-state
  // visually matches the noop-state.
  onSend: (body: string) => void;
  // Pending here is "request in flight" — the optimistic placeholder
  // is already visible at this point, but we still gate the send
  // button so a rapid double-tap doesn't double-fire the mutation.
  pending: boolean;
  testID?: string;
};

export function Composer({ onSend, pending, testID }: Props) {
  const [value, setValue] = React.useState('');
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const primaryFg = useThemeColor('primary-foreground');
  const border = useThemeColor('border');

  const trimmed = value.trim();
  const canSend = trimmed.length > 0 && !pending;

  const handleSend = React.useCallback(() => {
    if (!canSend) return;
    onSend(trimmed);
    setValue('');
  }, [canSend, onSend, trimmed]);

  return (
    <View
      testID={testID}
      style={{ borderTopColor: border, borderTopWidth: 1 }}
      className="flex-row items-end gap-2 bg-card px-3 py-2">
      <View className="min-h-10 flex-1 justify-center rounded-2xl bg-background px-3 py-1.5">
        <TextInput
          value={value}
          onChangeText={setValue}
          placeholder="Message"
          placeholderTextColor={mutedFg}
          multiline
          maxLength={MAX_LENGTH}
          // returnKeyType defaults to "default" on multiline so
          // Enter inserts a newline (matches every other chat app);
          // send happens through the explicit button. Don't wire
          // submitEditing — it fires per-press on iOS multiline
          // and is unreliable as a primary action.
          accessibilityLabel="Message"
          testID={testID ? `${testID}-input` : 'composer-input'}
          style={{
            color: fg,
            maxHeight: MAX_HEIGHT,
            // Hide the focus ring on web — the rounded container
            // already reads as the focused control.
            outlineWidth: 0,
          }}
          className="text-base"
        />
      </View>
      <Pressable
        onPress={handleSend}
        disabled={!canSend}
        accessibilityRole="button"
        accessibilityLabel="Send message"
        testID={testID ? `${testID}-send` : 'composer-send'}
        hitSlop={4}
        style={{ opacity: canSend ? 1 : 0.4 }}
        className="h-10 w-10 items-center justify-center rounded-full bg-primary active:bg-primary/90">
        <Send size={18} color={primaryFg} />
      </Pressable>
    </View>
  );
}
