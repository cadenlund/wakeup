// In-content header for the Chats list (search left, "Chats"
// centered) — a plain Pressable row, not the native nav-bar header,
// so the chrome presses the same as the Friends / Profile tab
// headers and the chat-thread bar.
//
// Renders nothing on web: there the (tabs) layout is the sidebar
// (`_layout.web.tsx`), which already carries the search trigger, so
// an in-content copy here would just duplicate it. Same pattern as
// `<WebRefreshButton>` / `<ComposeFab>` — the platform divergence is
// encapsulated so the screen renders one shared path.
import { Platform, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { HeaderSearchButton } from '@/components/header-search-button';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export function ChatsHeaderBar() {
  const card = useThemeColor('card');
  const border = useThemeColor('border');
  const insets = useSafeAreaInsets();
  if (Platform.OS === 'web') return null;
  return (
    <View
      style={{ paddingTop: insets.top, backgroundColor: card, borderBottomColor: border }}
      className="border-b">
      {/* No px on this row — HeaderSearchButton carries its own edge
          margin (shared with the tab header). */}
      <View className="h-12 flex-row items-center">
        <HeaderSearchButton />
        <View className="flex-1" />
      </View>
      <View
        pointerEvents="none"
        style={{ top: insets.top }}
        className="absolute inset-x-0 h-12 items-center justify-center">
        <Text className="text-base font-semibold">Chats</Text>
      </View>
    </View>
  );
}
