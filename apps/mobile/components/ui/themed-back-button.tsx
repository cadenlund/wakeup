// Themed back button for any Stack.Screen that wants its left header
// affordance to read in the same visual language as the rest of the
// app (muted-text label + small chevron) rather than React
// Navigation's default iOS-style chrome chevron + tinted title.
//
// Pass to a Stack.Screen as `headerLeft: () => <ThemedBackButton />`
// alongside `headerBackVisible: false` so the native chevron
// doesn't render under the custom one.
import { useRouter } from 'expo-router';
import { ChevronLeft } from 'lucide-react-native';
import * as React from 'react';
import { Pressable } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type ThemedBackButtonProps = {
  // Defaults to "Back". Pass the parent screen's title for the
  // standard iOS "← Chats" affordance.
  label?: string;
  // Override the default router.back() behaviour (e.g. to clear
  // intermediate state before going back).
  onPress?: () => void;
  testID?: string;
};

function ThemedBackButton({ label = 'Back', onPress, testID }: ThemedBackButtonProps) {
  const router = useRouter();
  const mutedFg = useThemeColor('muted-foreground');
  const handlePress = onPress ?? (() => router.back());
  return (
    <Pressable
      onPress={handlePress}
      accessibilityRole="button"
      accessibilityLabel={label}
      testID={testID ?? 'themed-back-button'}
      hitSlop={8}
      className="ml-1 flex-row items-center gap-0.5 active:opacity-60">
      <ChevronLeft size={20} color={mutedFg} />
      <Text style={{ color: mutedFg }} className="text-base">
        {label}
      </Text>
    </Pressable>
  );
}

export { ThemedBackButton };
export type { ThemedBackButtonProps };
