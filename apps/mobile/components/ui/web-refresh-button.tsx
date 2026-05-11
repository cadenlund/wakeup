// Web-only refresh affordance for list surfaces — substitutes for
// the pull-to-refresh gesture that doesn't exist on a desktop
// browser. Returns null on native; the existing RefreshControl
// handles those platforms.
//
// Visual matches the other small icon buttons that live next to
// search inputs (e.g. "New chat" on the chats tab): 40-square,
// rounded-md, subtle border, active state.
import * as React from 'react';
import { ActivityIndicator, Platform, Pressable } from 'react-native';
import { RotateCw } from 'lucide-react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export function WebRefreshButton({
  onPress,
  refreshing,
  testID,
}: {
  onPress: () => void;
  refreshing: boolean;
  testID?: string;
}) {
  const fg = useThemeColor('foreground');
  if (Platform.OS !== 'web') return null;
  return (
    <Pressable
      onPress={onPress}
      disabled={refreshing}
      accessibilityRole="button"
      accessibilityLabel="Refresh"
      accessibilityState={{ disabled: refreshing }}
      testID={testID}
      hitSlop={4}
      className="h-10 w-10 shrink-0 items-center justify-center rounded-md border border-border bg-card active:bg-muted">
      {refreshing ? <ActivityIndicator color={fg} /> : <RotateCw size={16} color={fg} />}
    </Pressable>
  );
}
