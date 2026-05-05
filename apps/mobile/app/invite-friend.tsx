// Invite-friend modal — search by email or username, preview the
// match if it lands, send invite. Mock-only for now: hits the local
// friends store rather than POST /v1/friends/invite. Production
// wiring lands at Phase 7 alongside the rest of the friends mutations.
import { Stack, useRouter } from 'expo-router';
import { AtSign, Check, Mail, Send, Sparkles, X } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useFriendsStore } from '@/lib/mock/friends';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const USERNAME_RE = /^@?[a-zA-Z0-9_.-]{2,32}$/;

// Mock directory the search is matched against — stand-in for
// /v1/users?q= until Phase 6 wires the real one.
const MOCK_DIRECTORY: { handle: string; name: string; bio: string }[] = [
  { handle: 'lily', name: 'Lily Bennett', bio: 'designer · plant mom' },
  { handle: 'owen', name: 'Owen Walters', bio: 'guitar + late-night code' },
  { handle: 'noor', name: 'Noor Hassan', bio: 'sleep researcher' },
  { handle: 'theo', name: 'Theo Lin', bio: 'just here for the voice rooms' },
  { handle: 'jules', name: 'Jules Park', bio: '🌙' },
];

export default function InviteFriendScreen() {
  const router = useRouter();
  const acceptInvite = useFriendsStore((s) => s.acceptInvite);
  const [query, setQuery] = React.useState('');
  const [sentTo, setSentTo] = React.useState<string | null>(null);

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const bg = useThemeColor('background');
  const ring = useThemeColor('ring');

  const trimmed = query.trim();
  const isEmail = EMAIL_RE.test(trimmed);
  const isUsername = !isEmail && USERNAME_RE.test(trimmed);
  const isValid = isEmail || isUsername;

  const directoryMatches = React.useMemo(() => {
    if (!trimmed || isEmail) return [];
    const q = trimmed.replace(/^@/, '').toLowerCase();
    return MOCK_DIRECTORY.filter(
      (u) => u.handle.toLowerCase().includes(q) || u.name.toLowerCase().includes(q),
    );
  }, [trimmed, isEmail]);

  const onSend = React.useCallback(() => {
    if (!isValid) return;
    acceptInvite(trimmed);
    setSentTo(trimmed);
  }, [isValid, trimmed, acceptInvite]);

  const onAccept = React.useCallback(
    (handle: string) => {
      acceptInvite(handle);
      setSentTo(`@${handle.replace(/^@/, '')}`);
    },
    [acceptInvite],
  );

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen
        options={{
          presentation: 'modal',
          headerShown: true,
          title: 'Invite a friend',
          headerStyle: { backgroundColor: bg },
          headerShadowVisible: false,
          headerTintColor: fg,
          headerTitleStyle: { color: fg, fontWeight: '600' },
          headerLeft: () => (
            <Pressable
              onPress={() => router.back()}
              hitSlop={10}
              accessibilityLabel="Close"
              style={{ marginLeft: 8 }}>
              <X size={22} color={fg} strokeWidth={2.25} />
            </Pressable>
          ),
        }}
      />

      <ScrollView
        contentContainerClassName="px-4 py-6 gap-6 pb-12"
        keyboardShouldPersistTaps="handled">
        {sentTo ? (
          <Card style={{ borderColor: 'rgba(34, 197, 94, 0.35)' }}>
            <CardContent className="items-center gap-3 py-6">
              <View
                style={{
                  width: 56,
                  height: 56,
                  borderRadius: 28,
                  backgroundColor: 'rgba(34, 197, 94, 0.14)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                <Check size={26} color="#15803d" strokeWidth={2.5} />
              </View>
              <View className="items-center gap-1">
                <Text variant="h4">Invite sent</Text>
                <Text variant="small" style={{ color: mutedFg, textAlign: 'center' }}>
                  We&apos;ve sent an invite to{' '}
                  <Text className="font-semibold" style={{ color: fg }}>
                    {sentTo}
                  </Text>
                  . They&apos;ll show up in your Friends list once they accept.
                </Text>
              </View>
              <View className="flex-row gap-2 pt-1">
                <Button
                  size="sm"
                  variant="outline"
                  onPress={() => {
                    setSentTo(null);
                    setQuery('');
                  }}>
                  <Text>Invite another</Text>
                </Button>
                <Button size="sm" onPress={() => router.back()}>
                  <Text>Done</Text>
                </Button>
              </View>
            </CardContent>
          </Card>
        ) : (
          <>
            <View className="gap-2">
              <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
                Search by email or @username
              </Text>
              <View style={{ position: 'relative' }}>
                <View style={{ position: 'absolute', top: 12, left: 12, zIndex: 1 }}>
                  {isEmail ? (
                    <Mail size={18} color={ring} />
                  ) : isUsername ? (
                    <AtSign size={18} color={ring} />
                  ) : (
                    <AtSign size={18} color={mutedFg} />
                  )}
                </View>
                <Input
                  value={query}
                  onChangeText={(v) => {
                    setQuery(v);
                    if (sentTo) setSentTo(null);
                  }}
                  placeholder="alice@example.com or @alice"
                  keyboardType="email-address"
                  autoCapitalize="none"
                  autoCorrect={false}
                  className="pl-10"
                />
              </View>
              {trimmed.length > 0 && !isValid ? (
                <Text variant="small" style={{ color: mutedFg }}>
                  Enter a valid email or a username (letters, numbers, _ . -, 2–32 chars).
                </Text>
              ) : null}
            </View>

            {isUsername && directoryMatches.length > 0 ? (
              <View className="gap-2">
                <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
                  On Wakeup
                </Text>
                <Card>
                  <CardContent className="p-0">
                    {directoryMatches.map((u, i) => (
                      <React.Fragment key={u.handle}>
                        {i > 0 ? <Separator /> : null}
                        <View className="flex-row items-center gap-3 px-4 py-3.5">
                          <View
                            style={{
                              width: 40,
                              height: 40,
                              borderRadius: 20,
                              backgroundColor: '#a855f7',
                              alignItems: 'center',
                              justifyContent: 'center',
                            }}>
                            <Text className="text-sm font-semibold text-white">
                              {u.name
                                .split(/\s+/)
                                .map((p) => p[0])
                                .join('')
                                .slice(0, 2)
                                .toUpperCase()}
                            </Text>
                          </View>
                          <View className="flex-1 gap-0.5">
                            <Text className="font-medium" style={{ color: fg }}>
                              {u.name}
                            </Text>
                            <Text variant="small" style={{ color: mutedFg }} numberOfLines={1}>
                              @{u.handle} · {u.bio}
                            </Text>
                          </View>
                          <Button size="sm" onPress={() => onAccept(u.handle)}>
                            <Text>Add</Text>
                          </Button>
                        </View>
                      </React.Fragment>
                    ))}
                  </CardContent>
                </Card>
              </View>
            ) : null}

            {isEmail ? (
              <View className="gap-2">
                <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
                  Send invite
                </Text>
                <Card>
                  <CardContent className="gap-3 py-4">
                    <View className="flex-row items-center gap-3">
                      <View
                        style={{
                          width: 40,
                          height: 40,
                          borderRadius: 20,
                          backgroundColor: 'rgba(30, 64, 175, 0.10)',
                          alignItems: 'center',
                          justifyContent: 'center',
                        }}>
                        <Mail size={18} color={ring} />
                      </View>
                      <View className="flex-1 gap-0.5">
                        <Text className="font-medium" style={{ color: fg }}>
                          {trimmed}
                        </Text>
                        <Text variant="small" style={{ color: mutedFg }}>
                          We&apos;ll email them an invite link.
                        </Text>
                      </View>
                    </View>
                    <Button onPress={onSend}>
                      <Send size={16} color="#ffffff" />
                      <Text>Send invite</Text>
                    </Button>
                  </CardContent>
                </Card>
              </View>
            ) : null}

            {!trimmed ? (
              <View className="gap-2">
                <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
                  Suggestions
                </Text>
                <Card>
                  <CardContent className="p-0">
                    {MOCK_DIRECTORY.slice(0, 3).map((u, i) => (
                      <React.Fragment key={u.handle}>
                        {i > 0 ? <Separator /> : null}
                        <View className="flex-row items-center gap-3 px-4 py-3.5">
                          <View
                            style={{
                              width: 40,
                              height: 40,
                              borderRadius: 20,
                              backgroundColor: '#22d3ee',
                              alignItems: 'center',
                              justifyContent: 'center',
                            }}>
                            <Text className="text-sm font-semibold text-white">
                              {u.name
                                .split(/\s+/)
                                .map((p) => p[0])
                                .join('')
                                .slice(0, 2)
                                .toUpperCase()}
                            </Text>
                          </View>
                          <View className="flex-1 gap-0.5">
                            <View className="flex-row items-center gap-1.5">
                              <Text className="font-medium" style={{ color: fg }}>
                                {u.name}
                              </Text>
                              <Badge variant="secondary">
                                <Text>2 mutuals</Text>
                              </Badge>
                            </View>
                            <Text variant="small" style={{ color: mutedFg }} numberOfLines={1}>
                              @{u.handle} · {u.bio}
                            </Text>
                          </View>
                          <Button size="sm" variant="outline" onPress={() => onAccept(u.handle)}>
                            <Text>Add</Text>
                          </Button>
                        </View>
                      </React.Fragment>
                    ))}
                  </CardContent>
                </Card>
                <View className="flex-row items-center gap-1.5 px-1 pt-2">
                  <Sparkles size={14} color={mutedFg} />
                  <Text variant="small" style={{ color: mutedFg }}>
                    Suggestions are people in your group chats who you haven&apos;t added yet.
                  </Text>
                </View>
              </View>
            ) : null}
          </>
        )}
      </ScrollView>
    </View>
  );
}
