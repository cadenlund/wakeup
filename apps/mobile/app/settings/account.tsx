// Settings · Account — display name, email, password change, and
// the App-Store-required Delete Account path (§10.5). Mock state
// only for now; mutations wire up at Phase 6 against /v1/users/me.
import * as React from 'react';
import { ScrollView, View } from 'react-native';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function AccountScreen() {
  const [displayName, setDisplayName] = React.useState('Caden Lund');
  const [email, setEmail] = React.useState('caden@example.com');

  const [currentPwd, setCurrentPwd] = React.useState('');
  const [newPwd, setNewPwd] = React.useState('');
  const [confirmPwd, setConfirmPwd] = React.useState('');

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');

  const passwordsMatch = newPwd.length > 0 && newPwd === confirmPwd;
  const passwordValid = newPwd.length >= 8 && passwordsMatch;

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12"
      keyboardShouldPersistTaps="handled">
      <View className="items-center gap-2 pb-2">
        <View
          style={{
            width: 80,
            height: 80,
            borderRadius: 40,
            backgroundColor: '#1e40af',
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <Text className="text-2xl font-bold text-white">CL</Text>
        </View>
        <Button size="sm" variant="outline">
          <Text>Change photo</Text>
        </Button>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Profile
        </Text>
        <Card>
          <CardContent className="gap-4 py-4">
            <View className="gap-1.5">
              <Label nativeID="displayName">Display name</Label>
              <Input
                aria-labelledby="displayName"
                value={displayName}
                onChangeText={setDisplayName}
                placeholder="Your name"
                autoCapitalize="words"
              />
              <Text variant="small" className="text-muted-foreground">
                Visible on your profile and in chats. 2–32 characters.
              </Text>
            </View>

            <Separator />

            <View className="gap-1.5">
              <Label nativeID="email">Email</Label>
              <View style={{ position: 'relative' }}>
                <Input
                  aria-labelledby="email"
                  value={email}
                  onChangeText={setEmail}
                  placeholder="you@example.com"
                  keyboardType="email-address"
                  autoCapitalize="none"
                />
                <View
                  style={{
                    position: 'absolute',
                    right: 10,
                    top: 10,
                    paddingHorizontal: 8,
                    paddingVertical: 2,
                    borderRadius: 999,
                    backgroundColor: 'rgba(34, 197, 94, 0.14)',
                  }}>
                  <Text className="text-[11px] font-semibold" style={{ color: '#15803d' }}>
                    Verified
                  </Text>
                </View>
              </View>
              <Text variant="small" className="text-muted-foreground">
                Used to sign in and recover your account.
              </Text>
            </View>
          </CardContent>
        </Card>
        <View className="flex-row justify-end gap-2 pt-1">
          <Button size="sm">
            <Text>Save changes</Text>
          </Button>
        </View>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Change password
        </Text>
        <Card>
          <CardContent className="gap-4 py-4">
            <View className="gap-1.5">
              <Label nativeID="currentPwd">Current password</Label>
              <Input
                aria-labelledby="currentPwd"
                value={currentPwd}
                onChangeText={setCurrentPwd}
                secureTextEntry
                placeholder="••••••••"
              />
            </View>
            <View className="gap-1.5">
              <Label nativeID="newPwd">New password</Label>
              <Input
                aria-labelledby="newPwd"
                value={newPwd}
                onChangeText={setNewPwd}
                secureTextEntry
                placeholder="At least 8 characters"
              />
            </View>
            <View className="gap-1.5">
              <Label nativeID="confirmPwd">Confirm new password</Label>
              <Input
                aria-labelledby="confirmPwd"
                value={confirmPwd}
                onChangeText={setConfirmPwd}
                secureTextEntry
                placeholder="Re-enter new password"
              />
              {confirmPwd.length > 0 && !passwordsMatch ? (
                <Text variant="small" style={{ color: destructive }}>
                  Passwords don&apos;t match
                </Text>
              ) : null}
            </View>
          </CardContent>
        </Card>
        <View className="flex-row justify-end gap-2 pt-1">
          <Button size="sm" disabled={!passwordValid || currentPwd.length === 0}>
            <Text>Update password</Text>
          </Button>
        </View>
      </View>

      <View className="gap-2 pt-4">
        <Text
          variant="small"
          className="font-semibold uppercase tracking-wider"
          style={{ color: destructive }}>
          Danger zone
        </Text>
        <Card style={{ borderColor: 'rgba(220, 38, 38, 0.35)' }}>
          <CardContent className="gap-3 py-4">
            <View className="gap-1">
              <Text className="font-semibold" style={{ color: fg }}>
                Delete account
              </Text>
              <Text variant="small" style={{ color: mutedFg }}>
                Permanently removes your account, all conversations you started, and your messages.
                Friends will see [redacted] in past chats.
              </Text>
            </View>
            <View className="flex-row justify-end">
              <Button size="sm" variant="destructive">
                <Text>Delete account</Text>
              </Button>
            </View>
          </CardContent>
        </Card>
      </View>
    </ScrollView>
  );
}
