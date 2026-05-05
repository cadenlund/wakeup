// Settings · Notifications — category toggles per the backend's
// notification_preferences table. Quiet-hours UI is visual-only;
// real time-of-day persistence + suppression lands at Phase 11.
import { Bell, BellOff, Moon } from 'lucide-react-native';
import * as React from 'react';
import { ScrollView, View } from 'react-native';

import { Card, CardContent } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type CategoryRow = {
  id: string;
  title: string;
  subtitle: string;
};

const CATEGORIES: CategoryRow[] = [
  { id: 'messages', title: 'Direct messages', subtitle: 'New messages from friends' },
  { id: 'group', title: 'Group activity', subtitle: 'New group messages and @mentions' },
  { id: 'calls', title: 'Voice & video calls', subtitle: 'Incoming calls and ringback' },
  { id: 'friends', title: 'Friend requests', subtitle: 'When someone wants to add you' },
  { id: 'voiceRoom', title: 'Voice rooms', subtitle: 'When a friend opens a room' },
];

export default function NotificationsScreen() {
  const [pushEnabled, setPushEnabled] = React.useState(true);
  const [prefs, setPrefs] = React.useState<Record<string, boolean>>({
    messages: true,
    group: true,
    calls: true,
    friends: true,
    voiceRoom: false,
  });
  const [quietHours, setQuietHours] = React.useState(false);

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12">
      <View className="gap-2">
        <Card>
          <CardContent className="p-0">
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View
                style={{
                  width: 36,
                  height: 36,
                  borderRadius: 18,
                  backgroundColor: pushEnabled
                    ? 'rgba(30, 64, 175, 0.10)'
                    : 'rgba(100, 116, 139, 0.10)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                {pushEnabled ? (
                  <Bell size={18} color="#1e40af" />
                ) : (
                  <BellOff size={18} color={mutedFg} />
                )}
              </View>
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Push notifications
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  {pushEnabled
                    ? 'Wakeup can send notifications to this device.'
                    : 'All notifications muted on this device.'}
                </Text>
              </View>
              <Switch checked={pushEnabled} onCheckedChange={setPushEnabled} />
            </View>
          </CardContent>
        </Card>
      </View>

      <View className="gap-2" style={{ opacity: pushEnabled ? 1 : 0.5 }}>
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Categories
        </Text>
        <Card>
          <CardContent className="p-0">
            {CATEGORIES.map((c, i) => (
              <React.Fragment key={c.id}>
                {i > 0 ? <Separator /> : null}
                <View className="flex-row items-center gap-3 px-4 py-3.5">
                  <View className="flex-1 gap-0.5">
                    <Text className="font-medium" style={{ color: fg }}>
                      {c.title}
                    </Text>
                    <Text variant="small" style={{ color: mutedFg }}>
                      {c.subtitle}
                    </Text>
                  </View>
                  <Switch
                    disabled={!pushEnabled}
                    checked={prefs[c.id] ?? false}
                    onCheckedChange={(v) => setPrefs((p) => ({ ...p, [c.id]: v }))}
                  />
                </View>
              </React.Fragment>
            ))}
          </CardContent>
        </Card>
      </View>

      <View className="gap-2" style={{ opacity: pushEnabled ? 1 : 0.5 }}>
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Quiet hours
        </Text>
        <Card>
          <CardContent className="p-0">
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View
                style={{
                  width: 36,
                  height: 36,
                  borderRadius: 18,
                  backgroundColor: 'rgba(168, 85, 247, 0.12)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                <Moon size={18} color="#a855f7" />
              </View>
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Quiet hours
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  Suppress everything except calls during a window each day.
                </Text>
              </View>
              <Switch disabled={!pushEnabled} checked={quietHours} onCheckedChange={setQuietHours} />
            </View>

            {quietHours ? (
              <>
                <Separator />
                <View className="flex-row items-center justify-between px-4 py-3.5">
                  <View>
                    <Text variant="small" style={{ color: mutedFg }}>
                      Start
                    </Text>
                    <Text className="font-medium" style={{ color: fg }}>
                      10:00 PM
                    </Text>
                  </View>
                  <Text style={{ color: mutedFg }}>→</Text>
                  <View>
                    <Text variant="small" style={{ color: mutedFg }}>
                      End
                    </Text>
                    <Text className="font-medium" style={{ color: fg }}>
                      7:00 AM
                    </Text>
                  </View>
                </View>
              </>
            ) : null}
          </CardContent>
        </Card>
      </View>

      <View className="px-2">
        <Text variant="small" style={{ color: mutedFg }}>
          DND status from your profile takes precedence — even with push on, no notifications fire while
          you&apos;re set to Do Not Disturb.
        </Text>
      </View>
    </ScrollView>
  );
}
