// Profile / settings tab. Top: the editable "me" card (avatar, display
// name, bio, status emoji). Below: collapsible sections —
//   - Appearance: the shared <ThemePicker> + light/dark/system row
//     (same components the onboarding carousel uses), persisted to the
//     local theme store.
//   - Notifications: per-category push toggles (roadmap 8.3).
//   - Account: log out — this is where the temp header logout pill
//     moved to.
//
// The Tabs navigator draws the "Profile" header; this screen is just
// the scrollable body.
import { useRouter } from 'expo-router';
import { Bell, LogOut, Palette } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { ThemePicker } from '@/components/onboarding/theme-picker';
import { NotificationPrefsSection } from '@/components/settings/notification-prefs';
import { ProfileCard } from '@/components/settings/profile-card';
import { Button } from '@/components/ui/button';
import { Collapsible } from '@/components/ui/collapsible';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { haptics } from '@/lib/haptics';
import { deregisterPushAsync } from '@/lib/push/register';
import { useThemeStore, type ModePreference } from '@/lib/theme/store';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const MODE_PREFS: ModePreference[] = ['light', 'dark', 'system'];

export default function ProfileScreen() {
  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerStyle={{ padding: 16, gap: 16, paddingBottom: 32 }}
      keyboardShouldPersistTaps="handled">
      <ProfileCard />

      <Collapsible title="Appearance" Icon={Palette} testID="settings-appearance">
        <AppearanceSection />
      </Collapsible>

      <Collapsible title="Notifications" Icon={Bell} testID="settings-notifications">
        <NotificationPrefsSection />
      </Collapsible>

      <Collapsible title="Account" Icon={LogOut} testID="settings-account">
        <AccountSection />
      </Collapsible>
    </ScrollView>
  );
}

function AppearanceSection() {
  const fg = useThemeColor('primary-foreground');
  const cardFg = useThemeColor('card-foreground');
  const selected = useThemeStore((s) => s.selected);
  const setScheme = useThemeStore((s) => s.setScheme);
  const mode = useThemeStore((s) => s.mode);
  const modePref = useThemeStore((s) => s.modePreference);
  const setModePref = useThemeStore((s) => s.setModePreference);
  return (
    <View className="gap-4">
      <ThemePicker
        selected={selected}
        mode={mode}
        onPick={(s) => {
          haptics.tap();
          void setScheme(s);
        }}
      />
      <View className="gap-2 border-t border-border pt-4">
        <Text variant="muted" className="text-xs uppercase">
          Light / dark
        </Text>
        <View className="flex-row gap-2">
          {MODE_PREFS.map((p) => (
            <Pressable
              key={p}
              accessibilityRole="button"
              accessibilityLabel={`Mode ${p}`}
              accessibilityState={{ selected: p === modePref }}
              onPress={() => {
                haptics.tap();
                void setModePref(p);
              }}
              className={`flex-1 items-center rounded-lg px-3 py-2 ${
                p === modePref ? 'bg-primary' : 'bg-muted'
              }`}>
              <Text
                style={{ color: p === modePref ? fg : cardFg }}
                className="text-sm font-medium capitalize">
                {p}
              </Text>
            </Pressable>
          ))}
        </View>
      </View>
    </View>
  );
}

function AccountSection() {
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      // Drop this device's push token while the session cookie is
      // still valid (the DELETE 401s once logout clears it).
      onMutate: () => {
        void deregisterPushAsync();
      },
      onSuccess: () => signedOut(qc, router),
      onError: (err) => {
        if (err instanceof APIError && err.status === 401) void signedOut(qc, router);
      },
    },
  });
  return (
    <Button
      variant="destructive"
      testID="settings-logout"
      accessibilityRole="button"
      accessibilityLabel="Log out"
      disabled={logout.isPending}
      onPress={() => logout.mutate()}>
      <Text>{logout.isPending ? 'Logging out…' : 'Log out'}</Text>
    </Button>
  );
}
