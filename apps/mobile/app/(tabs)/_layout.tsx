// Three-tab navigator (Phase 4.1). Tab order — Chats, Friends,
// Profile — matches §5.1 / §16. Each tab's content lives in a
// sibling file; this layout only owns the chrome (icons, labels,
// header tint) and the Phase-3 temporary logout button on the
// global header.
import { Tabs, useRouter } from 'expo-router';
import { LogOut, MessageCircle, Search, User, Users } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';
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
  const bg = useThemeColor('background');
  const card = useThemeColor('card');
  const border = useThemeColor('border');

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
        // Header + tab bar pick up theme colours so dark mode doesn't
        // leave the safe-area chrome glaring white. `card` is the
        // slight elevation surface (one shade off background) — use
        // it for both bars so the border-line between them and the
        // screen body actually reads.
        headerStyle: { backgroundColor: card },
        headerTintColor: fg,
        headerShadowVisible: false,
        sceneStyle: { backgroundColor: bg },
        tabBarStyle: {
          backgroundColor: card,
          borderTopColor: border,
        },
        // Right-side logout pill on the global tabs header. Removed
        // when settings/account lands in Phase 11.6 — at that point
        // logout moves into the account screen.
        headerRight: () => (
          <LogoutPill onPress={() => logout.mutate()} pending={logout.isPending} fg={fg} />
        ),
      }}>
      <Tabs.Screen
        name="index"
        options={{
          title: 'Chats',
          tabBarIcon: ({ color, size }) => <MessageCircle color={color} size={size} />,
          // Chats tab gets a search icon next to the logout pill.
          // Per §5.1 the global /search modal is "triggered by a
          // header search icon on the conversations tab" — an icon,
          // not a tappable input row, so it doesn't get visually
          // confused with the friends-tab inline search input
          // (which is friend-discovery, a different flow).
          headerRight: () => (
            <View className="flex-row items-center" style={{ gap: 4 }}>
              <Pressable
                onPress={() => router.push('/search')}
                accessibilityRole="button"
                accessibilityLabel="Search people, chats, messages"
                testID="header-search"
                hitSlop={8}
                className="h-9 w-9 items-center justify-center rounded-full active:bg-muted">
                <Search size={18} color={fg} />
              </Pressable>
              <LogoutPill onPress={() => logout.mutate()} pending={logout.isPending} fg={fg} />
            </View>
          ),
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

function LogoutPill({
  onPress,
  pending,
  fg,
}: {
  onPress: () => void;
  pending: boolean;
  fg: string;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel="Log out"
      testID="header-logout"
      onPress={onPress}
      disabled={pending}
      hitSlop={8}
      style={{
        flexDirection: 'row',
        alignItems: 'center',
        gap: 6,
        marginRight: 14,
        opacity: pending ? 0.5 : 1,
      }}>
      <LogOut size={16} color={fg} />
      <Text className="text-sm font-medium">{pending ? 'Logging out…' : 'Log out'}</Text>
    </Pressable>
  );
}
