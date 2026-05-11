// Chats tab's internal stack: the conversations list (`index`) and
// the thread (`conversations/[id]`). Nesting them in a <Stack> is
// what makes opening a chat *push* — so it slides in like the
// login↔register swap and gets a real back button — instead of the
// flat tab-swap it was before.
//
// `initialRouteName: 'index'` so a deep link straight to
// /conversations/<id> still mounts the list underneath it (Back
// works on a cold open). This is back-stack scaffolding only — safe
// in a nested layout, unlike at the root (see app/_layout.tsx).
//
// On web the (tabs) layout is a sidebar (`_layout.web.tsx`'s
// <Slot/>), which provides the chrome itself — so this Stack is
// headerless there and the list/thread render their own in-content
// header bars (the thread already does).
import { Stack } from 'expo-router';
import * as React from 'react';
import { Platform, View } from 'react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export const unstable_settings = { initialRouteName: 'index' };

export default function ChatStackLayout() {
  const fg = useThemeColor('foreground');
  const bg = useThemeColor('background');
  const card = useThemeColor('card');
  const border = useThemeColor('border');

  return (
    <Stack
      screenOptions={{
        // Web: the sidebar layout is the chrome — no navigator header.
        headerShown: Platform.OS !== 'web',
        headerStyle: { backgroundColor: card },
        headerTintColor: fg,
        headerShadowVisible: false,
        headerTitleAlign: 'center',
        // 1px hairline under the header so the themed border still
        // reads on dark mode where `headerShadowVisible: false`
        // already hides the native divider.
        headerBackground: () => (
          <View
            style={{
              flex: 1,
              backgroundColor: card,
              borderBottomWidth: 1,
              borderBottomColor: border,
            }}
          />
        ),
        contentStyle: { backgroundColor: bg },
      }}
    />
  );
}
