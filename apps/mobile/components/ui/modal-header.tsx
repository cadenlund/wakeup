// Shared chrome strip for routed-modal screens — gives every modal
// in the app the same `[left icon] [title] [right icon]` shape so a
// user who learns one (close, confirm, back) immediately knows the
// others. Icon-only on both sides keeps the buttons compact and
// avoids the "Cancel"/"Create" text-wrapping bug on narrow phones.
//
// Conventions enforced by this component:
//   - Left slot is dismiss-ish: X to close the whole modal, or
//     ChevronLeft to back out of a sub-pane within the modal.
//   - Right slot is confirm-ish: Check to commit the action that
//     this pane represents. Render disabled (muted color) when the
//     CTA isn't ready; render an ActivityIndicator when it's
//     in-flight.
//   - Fixed-width 40px tap targets on each side so the centered
//     title can't shift when an icon swaps in or out.
import * as React from 'react';
import { ActivityIndicator, Pressable, View } from 'react-native';
import { Check, ChevronLeft, X } from 'lucide-react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export type ModalHeaderLeftKind = 'close' | 'back';

type RightAction = {
  // Single confirmation icon for every modal — keeps the language
  // consistent across surfaces. Pass a custom React node only if
  // a screen genuinely needs something else.
  icon?: 'check' | React.ReactNode;
  onPress: () => void;
  accessibilityLabel: string;
  disabled?: boolean;
  loading?: boolean;
  testID?: string;
};

type LeftAction = {
  kind: ModalHeaderLeftKind;
  onPress: () => void;
  testID?: string;
};

type Props = {
  title: string;
  left: LeftAction;
  right?: RightAction;
};

export function ModalHeader({ title, left, right }: Props) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const primary = useThemeColor('primary');
  const LeftIcon = left.kind === 'back' ? ChevronLeft : X;
  const leftLabel = left.kind === 'back' ? 'Back' : 'Close';

  return (
    <View className="flex-row items-center border-b border-border bg-card px-3 py-3">
      <Pressable
        onPress={left.onPress}
        accessibilityRole="button"
        accessibilityLabel={leftLabel}
        testID={left.testID}
        hitSlop={8}
        className="h-9 w-10 items-start justify-center rounded-md active:bg-muted">
        <LeftIcon size={left.kind === 'back' ? 22 : 20} color={fg} />
      </Pressable>
      <Text variant="h4" numberOfLines={1} className="flex-1 text-center">
        {title}
      </Text>
      <View className="w-10 items-end">
        {right ? (
          <Pressable
            onPress={right.onPress}
            disabled={right.disabled || right.loading}
            accessibilityRole="button"
            accessibilityLabel={right.accessibilityLabel}
            accessibilityState={{ disabled: !!(right.disabled || right.loading) }}
            testID={right.testID}
            hitSlop={8}
            className="h-9 w-9 items-center justify-center rounded-md active:bg-muted">
            {right.loading ? (
              <ActivityIndicator color={primary} />
            ) : right.icon == null || right.icon === 'check' ? (
              <Check size={22} color={right.disabled ? mutedFg : primary} />
            ) : (
              right.icon
            )}
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}
