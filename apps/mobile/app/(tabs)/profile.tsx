// Phase 1.4 Profile tab (§5.2). Settings sub-screens live as modals
// under /settings/* — this tab is the user's identity surface plus
// the entry points into each settings page. Replaced by the real
// profile + settings routes when Phase 10 lands.
import { Stack, useRouter } from 'expo-router';
import { Image } from 'expo-image';
import {
  Bell,
  ChevronRight,
  Palette,
  Settings as SettingsIcon,
  Shield,
  Smartphone,
} from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useThemeStore } from '@/lib/theme/store';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type SettingsRowProps = {
  icon: React.ReactNode;
  title: string;
  subtitle?: string;
  trailing?: React.ReactNode;
  onPress?: () => void;
};

function SettingsRow({ icon, title, subtitle, trailing, onPress }: SettingsRowProps) {
  const mutedFg = useThemeColor('muted-foreground');
  const fg = useThemeColor('foreground');
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      className="flex-row items-center gap-3 px-4 py-3.5 active:bg-muted">
      <View
        style={{
          width: 36,
          height: 36,
          borderRadius: 18,
          backgroundColor: 'rgba(100, 116, 139, 0.10)',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
        {icon}
      </View>
      <View className="flex-1 gap-0.5">
        <Text className="font-medium" style={{ color: fg }}>
          {title}
        </Text>
        {subtitle ? (
          <Text variant="small" style={{ color: mutedFg }} numberOfLines={1}>
            {subtitle}
          </Text>
        ) : null}
      </View>
      {trailing ?? <ChevronRight size={18} color={mutedFg} />}
    </Pressable>
  );
}

export default function ProfileScreen() {
  const router = useRouter();
  const selected = useThemeStore((s) => s.selected);
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="pb-12"
      keyboardShouldPersistTaps="handled">
      <Stack.Screen
        options={{
          headerTitleAlign: 'center',
          headerTitle: () => (
            <Image
              source={require('../../assets/logo.png')}
              style={{ width: 144, height: 144 }}
              contentFit="contain"
              accessibilityLabel="Wakeup"
            />
          ),
        }}
      />

      <View className="items-center gap-3 px-4 pt-6">
        <View
          style={{
            width: 88,
            height: 88,
            borderRadius: 44,
            backgroundColor: '#1e40af',
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <Text className="text-3xl font-bold text-white">CL</Text>
        </View>
        <View className="items-center gap-1">
          <View className="flex-row items-center gap-1.5">
            <Text variant="h3">Caden Lund</Text>
            <Text className="text-2xl">🌙</Text>
          </View>
          <Text variant="muted">@cadenlund · sleeping in a bit</Text>
        </View>
        <View className="flex-row gap-2 pt-1">
          <Button size="sm" variant="outline" onPress={() => router.push('/settings/account')}>
            <Text>Edit profile</Text>
          </Button>
          <Button size="sm" variant="outline">
            <Text>Share</Text>
          </Button>
        </View>
      </View>

      <View className="px-4 pt-8">
        <Text
          variant="small"
          className="px-1 pb-2 font-semibold uppercase tracking-wider text-muted-foreground">
          Settings
        </Text>
        <Card>
          <CardContent className="p-0">
            <SettingsRow
              icon={<Palette size={18} color={fg} />}
              title="Appearance"
              subtitle={`Scheme: ${selected}`}
              trailing={
                <View className="flex-row items-center gap-2">
                  <Badge variant="secondary">
                    <Text>{selected}</Text>
                  </Badge>
                  <ChevronRight size={18} color={mutedFg} />
                </View>
              }
              onPress={() => router.push('/settings/theme')}
            />
            <Separator />
            <SettingsRow
              icon={<Shield size={18} color={fg} />}
              title="Privacy"
              subtitle="Biometric lock, blocked accounts"
              onPress={() => router.push('/settings/privacy')}
            />
            <Separator />
            <SettingsRow
              icon={<Bell size={18} color={fg} />}
              title="Notifications"
              subtitle="Messages, calls, friend requests"
              onPress={() => router.push('/settings/notifications')}
            />
            <Separator />
            <SettingsRow
              icon={<Smartphone size={18} color={fg} />}
              title="Devices"
              subtitle="2 active sessions"
              onPress={() => router.push('/settings/devices')}
            />
            <Separator />
            <SettingsRow
              icon={<SettingsIcon size={18} color={fg} />}
              title="Account"
              subtitle="Email, password, delete account"
              onPress={() => router.push('/settings/account')}
            />
          </CardContent>
        </Card>
      </View>

      <View className="items-center pt-8">
        <Text variant="small" style={{ color: mutedFg }}>
          Wakeup · v1.0.0 · Phase 1.4 preview
        </Text>
      </View>
    </ScrollView>
  );
}
