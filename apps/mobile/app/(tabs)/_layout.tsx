import { Tabs } from 'expo-router';
import { LogOut } from 'lucide-react-native';
import * as React from 'react';
import { Pressable } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey, usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function TabLayout() {
  const primary = useThemeColor('primary');
  const fg = useThemeColor('foreground');

  // Temporary in-app logout for end-to-end testing of the auth +
  // onboarding flow. Real settings/logout UX lands in Phase 6.
  const qc = useQueryClient();
  const logout = usePostV1AuthLogout({
    mutation: {
      onSettled: async () => {
        await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
        // AuthGate sees the 401-shaped me result and bounces to /login.
      },
    },
  });

  return (
    <Tabs
      screenOptions={{
        tabBarActiveTintColor: primary,
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
          title: 'Gallery',
        }}
      />
      <Tabs.Screen
        name="two"
        options={{
          title: 'Tab Two',
        }}
      />
    </Tabs>
  );
}
