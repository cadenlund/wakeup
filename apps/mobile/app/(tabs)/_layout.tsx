import { Tabs, useRouter } from 'expo-router';
import { Image } from 'expo-image';
import { LogOut, User, Users } from 'lucide-react-native';
import * as React from 'react';
import { Pressable } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey, usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function TabLayout() {
  const primary = useThemeColor('primary');
  const fg = useThemeColor('foreground');
  const bg = useThemeColor('background');
  const mutedFg = useThemeColor('muted-foreground');
  const border = useThemeColor('border');

  // Temporary in-app logout for end-to-end testing of the auth +
  // onboarding flow. Real settings/logout UX lands in Phase 6.
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      // Run on success AND error: even if the network call to
      // /v1/auth/logout fails, the user wants out. Clear the
      // cached me + push to /login regardless.
      onSettled: () => {
        // Just invalidating isn't enough — apiFetch throws on the
        // resulting 401, TanStack keeps the previous successful
        // data, and AuthGate's "trust cached me" behaviour leaves
        // the user on the tabs. Explicitly null the cache + remove
        // the query so the next mount refetches fresh, then push
        // the user to /login directly so they don't sit on a stale
        // tab while the gate is making up its mind.
        qc.setQueryData(getGetV1AuthMeQueryKey(), null);
        qc.removeQueries({ queryKey: getGetV1AuthMeQueryKey() });
        router.replace('/(auth)/login');
      },
    },
  });

  return (
    <Tabs
      screenOptions={{
        // Theme the navigation chrome — without this, React
        // Navigation falls back to its default light theme so dark
        // schemes get a white header + tab bar against dark content.
        headerStyle: { backgroundColor: bg },
        headerTintColor: fg,
        headerTitleStyle: { color: fg },
        headerShadowVisible: false,
        tabBarStyle: {
          backgroundColor: bg,
          borderTopColor: border,
        },
        tabBarActiveTintColor: primary,
        tabBarInactiveTintColor: mutedFg,
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
          title: 'Messages',
          // Brand mark on the home tab. Logo is multi-color so a
          // tintColor would flatten the gradient — instead we go
          // big (32px vs the default ~24) and dim to ~40% opacity
          // when not focused so the active/inactive state still
          // reads at a glance.
          tabBarIcon: ({ focused }) => (
            <Image
              source={require('../../assets/logo.png')}
              style={{
                width: 68,
                height: 68,
                opacity: focused ? 1 : 0.4,
              }}
              contentFit="contain"
              accessibilityLabel="Wakeup messages"
            />
          ),
        }}
      />
      <Tabs.Screen
        name="two"
        options={{
          title: 'Friends',
          tabBarIcon: ({ color, size }) => <Users size={size} color={color} />,
        }}
      />
      <Tabs.Screen
        name="profile"
        options={{
          title: 'Profile',
          tabBarIcon: ({ color, size }) => <User size={size} color={color} />,
        }}
      />
    </Tabs>
  );
}
