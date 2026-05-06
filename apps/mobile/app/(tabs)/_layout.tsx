import { Tabs, useRouter } from 'expo-router';
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
  //
  // `removeQueries` (not `invalidateQueries`) is required — the auth-
  // gate hook trusts cached `me` over a transient error so a 401
  // refetch alone wouldn't flip `isAuthenticated` to false. After the
  // cache is cleared we route to /login imperatively; Stack.Protected
  // wouldn't redirect reliably from inside (tabs) on its own.
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      onSettled: async () => {
        // setQueryData(null) instead of removeQueries — removeQueries
        // signals every active observer of the me query that the data
        // is GONE, which TanStack interprets as "refetch immediately."
        // The refetch raced the cookie clear (URLSession sometimes
        // hadn't dropped the session cookie yet) and resurrected the
        // me cache before Stack.Protected could react. Setting null
        // is a definitive "no me" signal that doesn't trigger any
        // network activity. cancelQueries first pre-empts any in-
        // flight refetch from the logout response itself.
        await qc.cancelQueries({ queryKey: getGetV1AuthMeQueryKey() });
        qc.setQueryData(getGetV1AuthMeQueryKey(), null);
        // Imperative replace as belt-and-braces: Stack.Protected
        // SHOULD react to the cache flip and unmount (tabs) but the
        // transition isn't always reliable on iOS, so we explicitly
        // route to /login. Wrapped in setTimeout so React has
        // rendered the new guard state before the replace lands.
        setTimeout(() => router.replace('/(auth)/login'), 0);
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
