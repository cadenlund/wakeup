// Settings · Devices — registered Expo push tokens + active web
// sessions per the backend's device_tokens / sessions tables. Each
// row shows the device name, platform, and last-used time, with a
// per-row revoke + a "Sign out all other sessions" footer button.
import { LogOut, Monitor, Smartphone, Tablet } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type DeviceKind = 'phone' | 'tablet' | 'desktop';

type Device = {
  id: string;
  name: string;
  platform: string;
  kind: DeviceKind;
  lastUsed: string;
  current?: boolean;
  location?: string;
};

const DEVICES: Device[] = [
  {
    id: 'd1',
    name: 'iPhone 17 Pro',
    platform: 'iOS 26.0',
    kind: 'phone',
    lastUsed: 'Active now',
    current: true,
    location: 'Austin, TX',
  },
  {
    id: 'd2',
    name: 'MacBook Pro',
    platform: 'macOS · Safari',
    kind: 'desktop',
    lastUsed: '2 days ago',
    location: 'Austin, TX',
  },
  {
    id: 'd3',
    name: 'iPad Air',
    platform: 'iPadOS 26.0',
    kind: 'tablet',
    lastUsed: '1 week ago',
    location: 'Austin, TX',
  },
];

function DeviceIcon({ kind, color }: { kind: DeviceKind; color: string }) {
  if (kind === 'phone') return <Smartphone size={20} color={color} />;
  if (kind === 'tablet') return <Tablet size={20} color={color} />;
  return <Monitor size={20} color={color} />;
}

export default function DevicesScreen() {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');

  const others = DEVICES.filter((d) => !d.current);
  const current = DEVICES.find((d) => d.current);

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12">
      {current ? (
        <View className="gap-2">
          <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
            This device
          </Text>
          <Card style={{ borderColor: 'rgba(34, 197, 94, 0.35)' }}>
            <CardContent className="flex-row items-center gap-3 py-3.5">
              <View
                style={{
                  width: 40,
                  height: 40,
                  borderRadius: 20,
                  backgroundColor: 'rgba(34, 197, 94, 0.12)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                <DeviceIcon kind={current.kind} color="#15803d" />
              </View>
              <View className="flex-1 gap-0.5">
                <View className="flex-row items-center gap-2">
                  <Text className="font-semibold" style={{ color: fg }}>
                    {current.name}
                  </Text>
                  <Badge>
                    <Text>Active</Text>
                  </Badge>
                </View>
                <Text variant="small" style={{ color: mutedFg }}>
                  {current.platform} · {current.location}
                </Text>
              </View>
            </CardContent>
          </Card>
        </View>
      ) : null}

      <View className="gap-2">
        <View className="flex-row items-baseline justify-between">
          <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
            Other sessions
          </Text>
          <Text variant="small" style={{ color: mutedFg }}>
            ({others.length})
          </Text>
        </View>
        <Card>
          <CardContent className="p-0">
            {others.length === 0 ? (
              <View className="px-4 py-6 items-center">
                <Text variant="small" style={{ color: mutedFg }}>
                  No other active sessions
                </Text>
              </View>
            ) : (
              others.map((d, i) => (
                <React.Fragment key={d.id}>
                  {i > 0 ? <Separator /> : null}
                  <View className="flex-row items-center gap-3 px-4 py-3.5">
                    <View
                      style={{
                        width: 40,
                        height: 40,
                        borderRadius: 20,
                        backgroundColor: 'rgba(100, 116, 139, 0.12)',
                        alignItems: 'center',
                        justifyContent: 'center',
                      }}>
                      <DeviceIcon kind={d.kind} color={fg} />
                    </View>
                    <View className="flex-1 gap-0.5">
                      <Text className="font-medium" style={{ color: fg }}>
                        {d.name}
                      </Text>
                      <Text variant="small" style={{ color: mutedFg }}>
                        {d.platform} · {d.lastUsed}
                      </Text>
                    </View>
                    <Pressable accessibilityRole="button" accessibilityLabel={`Revoke ${d.name}`}>
                      <Text variant="small" style={{ color: destructive, fontWeight: '600' }}>
                        Revoke
                      </Text>
                    </Pressable>
                  </View>
                </React.Fragment>
              ))
            )}
          </CardContent>
        </Card>
      </View>

      {others.length > 0 ? (
        <View className="pt-2">
          <Button variant="outline" className="border-destructive">
            <LogOut size={16} color={destructive} />
            <Text style={{ color: destructive, fontWeight: '600' }}>Sign out all other sessions</Text>
          </Button>
        </View>
      ) : null}

      <View className="px-2 pt-2">
        <Text variant="small" style={{ color: mutedFg }}>
          We sign you out automatically after 90 days of inactivity. Revoke a session if you don&apos;t
          recognize it.
        </Text>
      </View>
    </ScrollView>
  );
}
