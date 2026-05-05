// Settings stack — presented as a modal from the Profile tab
// (§5.1 / §2). Each sub-screen is a sibling under /settings, but
// they share this stack so internal pushes (e.g. privacy → blocked
// users sub-list) keep the modal chrome and respect a single back
// path back to the tab.
import { Stack, useRouter } from 'expo-router';
import { X } from 'lucide-react-native';
import { Pressable } from 'react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function SettingsLayout() {
  const router = useRouter();
  const fg = useThemeColor('foreground');
  const bg = useThemeColor('background');

  return (
    <Stack
      screenOptions={{
        headerStyle: { backgroundColor: bg },
        headerTransparent: false,
        headerShadowVisible: false,
        headerTintColor: fg,
        headerTitleStyle: { color: fg, fontWeight: '600' },
        headerRight: () => (
          // Each settings screen surfaces a single "close the whole
          // modal" affordance in the same spot. Internal pushes (if
          // we add submenus later) get the default back arrow on
          // the left from expo-router automatically.
          <Pressable
            onPress={() => router.dismissAll()}
            hitSlop={10}
            accessibilityLabel="Close settings"
            style={{ marginRight: 12 }}>
            <X size={22} color={fg} strokeWidth={2.25} />
          </Pressable>
        ),
      }}>
      <Stack.Screen name="account" options={{ title: 'Account' }} />
      <Stack.Screen name="privacy" options={{ title: 'Privacy' }} />
      <Stack.Screen name="notifications" options={{ title: 'Notifications' }} />
      <Stack.Screen name="devices" options={{ title: 'Devices' }} />
      <Stack.Screen name="theme" options={{ title: 'Appearance' }} />
    </Stack>
  );
}
