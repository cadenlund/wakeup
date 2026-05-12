// Three-tab navigator (Phase 4.1). Tab order — Chats, Friends,
// Profile — matches §5.1 / §16. Each tab's content lives in a
// sibling file/group; this layout only owns the chrome (icons,
// labels, header tint).
//
// The Chats tab is the `(home)` group — a nested <Stack> holding the
// conversations list + the thread — so opening a chat pushes (slides
// in like login↔register) instead of swapping tabs. That Stack owns
// its own headers, so the Chats tab here is `headerShown: false`;
// Friends / Profile keep the shared header (search left). Log out
// lives in the Profile tab's settings (Account section). On web the
// (tabs) layout is replaced by `_layout.web.tsx` (a sidebar), so none
// of this header chrome runs there.
import { Tabs } from 'expo-router';
import { MessageCircle, User, Users } from 'lucide-react-native';
import * as React from 'react';

import { HeaderSearchButton } from '@/components/header-search-button';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function TabLayout() {
  const primary = useThemeColor('primary');
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const bg = useThemeColor('background');
  const card = useThemeColor('card');
  const border = useThemeColor('border');

  return (
    <Tabs
      screenOptions={{
        tabBarActiveTintColor: primary,
        tabBarInactiveTintColor: mutedFg,
        // Header + tab bar pick up theme colours so dark mode doesn't
        // leave the safe-area chrome glaring white. `card` is the
        // slight elevation surface (one shade off background) — use
        // it for both bars so the border-line between them and the
        // screen body actually reads.
        headerStyle: { backgroundColor: card },
        headerTintColor: fg,
        headerShadowVisible: false,
        // React Navigation left-aligns the header title on Android by
        // default (centered only on iOS). Force center on both so the
        // tab title sits between the search icon and the logout pill
        // consistently across platforms.
        headerTitleAlign: 'center',
        sceneStyle: { backgroundColor: bg },
        tabBarStyle: {
          backgroundColor: card,
          borderTopColor: border,
        },
        headerLeft: () => <HeaderSearchButton />,
      }}>
      <Tabs.Screen
        name="(home)"
        options={{
          title: 'Chats',
          // The (home) Stack draws the header for both the list and
          // the thread, so the tab itself is headerless.
          headerShown: false,
          tabBarIcon: ({ color, size }) => <MessageCircle color={color} size={size} />,
        }}
      />
      <Tabs.Screen
        name="friends"
        options={{
          title: 'Friends',
          tabBarIcon: ({ color, size }) => <Users color={color} size={size} />,
        }}
      />
      <Tabs.Screen
        name="profile"
        options={{
          title: 'Profile',
          tabBarIcon: ({ color, size }) => <User color={color} size={size} />,
        }}
      />
    </Tabs>
  );
}
