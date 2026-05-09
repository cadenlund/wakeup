// Three-tab navigator (Phase 4.1). Tab order — Chats, Friends,
// Profile — matches §5.1 / §16. Each tab's content lives in a
// sibling file; this layout only owns the chrome (icons, labels,
// header tint) and the Phase-3 temporary logout button on the
// global header.
import { Tabs, useRouter } from 'expo-router';
import { LogOut, MessageCircle, User, Users } from 'lucide-react-native';
import * as React from 'react';
import { Pressable } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function TabLayout() {
  const primary = useThemeColor('primary');
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  // Temporary in-app logout for end-to-end testing of the auth +
  // onboarding flow. Real settings/logout UX lands in Phase 11.6.
  //
  // Only signedOut on definitive success or "already signed out"
  // (401). A 5xx mid-logout would have hit `onSettled` and cleared
  // the local cache — but the server session was still alive, so
  // the user would have ended up with a stale "logged out" view
  // that diverged from reality on the next page load. Now:
  //   - 2xx → signedOut (clean path).
  //   - 401 → signedOut (session was already gone server-side, so
  //     local clear matches reality).
  //   - everything else → mutationCache toast surfaces the failure
  //     and the user stays signed in.
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      onSuccess: () => signedOut(qc, router),
      onError: (err) => {
        if (err instanceof APIError && err.status === 401) {
          void signedOut(qc, router);
        }
      },
    },
  });

  return (
    <Tabs
      screenOptions={{
        tabBarActiveTintColor: primary,
        tabBarInactiveTintColor: mutedFg,
        // Right-side logout pill on the global tabs header. Removed
        // when settings/account lands in Phase 11.6 — at that point
        // logout moves into the account screen.
        headerRight: () => (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Log out"
            testID="header-logout"
            onPress={() => logout.mutate()}
            disabled={logout.isPending}
            hitSlop={8}
            style={{
              flexDirection: 'row',
              alignItems: 'center',
              gap: 6,
              marginRight: 14,
              opacity: logout.isPending ? 0.5 : 1,
            }}>
            <LogOut size={16} color={fg} />
            <Text className="text-sm font-medium">
              {logout.isPending ? 'Logging out…' : 'Log out'}
            </Text>
          </Pressable>
        ),
      }}>
      <Tabs.Screen
        name="index"
        options={{
          title: 'Chats',
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
