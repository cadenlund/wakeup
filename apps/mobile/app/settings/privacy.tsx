// Settings · Privacy — biometric lock + lock-after timeout, blocked
// users list, and visibility toggles. Mock state only; real
// biometric wiring lands at Phase 10.x via expo-local-authentication.
import { ChevronRight, Shield, ShieldOff } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Card, CardContent } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const LOCK_AFTER_OPTIONS = [
  { value: 0, label: 'Immediately' },
  { value: 30, label: '30 seconds' },
  { value: 60, label: '1 minute' },
  { value: 300, label: '5 minutes' },
  { value: 900, label: '15 minutes' },
];

const MOCK_BLOCKED = [
  { id: 'b1', name: 'someone-rude', blockedAt: 'Mar 12' },
];

export default function PrivacyScreen() {
  const [biometricOn, setBiometricOn] = React.useState(true);
  const [lockAfter, setLockAfter] = React.useState(60);
  const [showLastSeen, setShowLastSeen] = React.useState(true);
  const [showInSearch, setShowInSearch] = React.useState(true);
  const [readReceipts, setReadReceipts] = React.useState(true);

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12">
      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          App lock
        </Text>
        <Card>
          <CardContent className="p-0">
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View
                style={{
                  width: 36,
                  height: 36,
                  borderRadius: 18,
                  backgroundColor: biometricOn ? 'rgba(30, 64, 175, 0.10)' : 'rgba(100, 116, 139, 0.10)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                {biometricOn ? (
                  <Shield size={18} color="#1e40af" />
                ) : (
                  <ShieldOff size={18} color={mutedFg} />
                )}
              </View>
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Require Face ID
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  Unlock the app with Face ID, Touch ID, or your device passcode.
                </Text>
              </View>
              <Switch checked={biometricOn} onCheckedChange={setBiometricOn} />
            </View>

            {biometricOn ? (
              <>
                <Separator />
                <View className="px-4 py-3 gap-2">
                  <Text variant="small" className="font-medium" style={{ color: fg }}>
                    Lock after
                  </Text>
                  <View className="flex-row flex-wrap gap-2">
                    {LOCK_AFTER_OPTIONS.map((opt) => {
                      const selected = lockAfter === opt.value;
                      return (
                        <Pressable
                          key={opt.value}
                          onPress={() => setLockAfter(opt.value)}
                          accessibilityRole="button"
                          accessibilityState={{ selected }}
                          style={{
                            paddingHorizontal: 12,
                            paddingVertical: 6,
                            borderRadius: 999,
                            backgroundColor: selected ? '#1e40af' : 'transparent',
                            borderWidth: 1,
                            borderColor: selected ? '#1e40af' : 'rgba(100, 116, 139, 0.25)',
                          }}>
                          <Text
                            variant="small"
                            style={{
                              color: selected ? '#ffffff' : fg,
                              fontWeight: '500',
                            }}>
                            {opt.label}
                          </Text>
                        </Pressable>
                      );
                    })}
                  </View>
                </View>
              </>
            ) : null}
          </CardContent>
        </Card>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Visibility
        </Text>
        <Card>
          <CardContent className="p-0">
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Show last seen
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  Friends see when you were last online.
                </Text>
              </View>
              <Switch checked={showLastSeen} onCheckedChange={setShowLastSeen} />
            </View>
            <Separator />
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Read receipts
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  Show when you&apos;ve read messages. Turning this off also hides theirs.
                </Text>
              </View>
              <Switch checked={readReceipts} onCheckedChange={setReadReceipts} />
            </View>
            <Separator />
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View className="flex-1 gap-0.5">
                <Text className="font-medium" style={{ color: fg }}>
                  Allow contact discovery
                </Text>
                <Text variant="small" style={{ color: mutedFg }}>
                  Let friends find you by email when they sync contacts.
                </Text>
              </View>
              <Switch checked={showInSearch} onCheckedChange={setShowInSearch} />
            </View>
          </CardContent>
        </Card>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Blocked accounts
        </Text>
        <Card>
          <CardContent className="p-0">
            {MOCK_BLOCKED.length === 0 ? (
              <View className="px-4 py-6 items-center">
                <Text variant="small" style={{ color: mutedFg }}>
                  No blocked accounts
                </Text>
              </View>
            ) : (
              MOCK_BLOCKED.map((b, i) => (
                <React.Fragment key={b.id}>
                  {i > 0 ? <Separator /> : null}
                  <Pressable className="flex-row items-center gap-3 px-4 py-3.5 active:bg-muted">
                    <View
                      style={{
                        width: 36,
                        height: 36,
                        borderRadius: 18,
                        backgroundColor: '#94a3b8',
                        alignItems: 'center',
                        justifyContent: 'center',
                      }}>
                      <Text className="text-sm font-semibold text-white">
                        {b.name.slice(0, 2).toUpperCase()}
                      </Text>
                    </View>
                    <View className="flex-1 gap-0.5">
                      <Text className="font-medium" style={{ color: fg }}>
                        @{b.name}
                      </Text>
                      <Text variant="small" style={{ color: mutedFg }}>
                        Blocked {b.blockedAt}
                      </Text>
                    </View>
                    <ChevronRight size={18} color={mutedFg} />
                  </Pressable>
                </React.Fragment>
              ))
            )}
          </CardContent>
        </Card>
      </View>
    </ScrollView>
  );
}
