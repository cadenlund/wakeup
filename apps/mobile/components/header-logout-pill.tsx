// Temporary in-app logout pill on the tabs / chats-list header.
// Real settings/account UX (with logout inside it) lands in Phase
// 11.6 — at that point this component goes away. Shared so the
// chats list (which owns its own header now that it lives inside a
// Stack) and the friends/profile tabs render the same control.
//
// Only `signedOut` on a definitive result: 2xx (clean) or 401 (the
// server session was already gone, so a local clear matches
// reality). Anything else leaves the user signed in and lets the
// mutationCache toast surface the failure — a 5xx mid-logout that
// still cleared the local cache would otherwise leave a "logged
// out" view that diverges from the live server session.
import { useRouter } from 'expo-router';
import { LogOut } from 'lucide-react-native';
import { Pressable } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export function HeaderLogoutPill() {
  const fg = useThemeColor('foreground');
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      onSuccess: () => signedOut(qc, router),
      onError: (err) => {
        if (err instanceof APIError && err.status === 401) void signedOut(qc, router);
      },
    },
  });
  return (
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
      <Text className="text-sm font-medium">{logout.isPending ? 'Logging out…' : 'Log out'}</Text>
    </Pressable>
  );
}
