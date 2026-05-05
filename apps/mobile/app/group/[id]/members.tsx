// Group · Members — current roster + Add member entry. Mock-only;
// real /v1/conversations/{id}/members hooks land at Phase 6. The
// "Delete group" button at the bottom routes through Alert with a
// member-count warning before calling deleteConversation.
import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import { Crown, Trash2, UserMinus, UserPlus } from 'lucide-react-native';
import * as React from 'react';
import { Alert, Pressable, ScrollView, View } from 'react-native';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import {
  useConversationsStore,
  type GroupConversation,
} from '@/lib/mock/conversations';
import { useFriendsStore } from '@/lib/mock/friends';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const ME = { initials: 'CL', name: 'You', color: '#1e40af' };

export default function GroupMembersScreen() {
  const router = useRouter();
  const { id } = useLocalSearchParams<{ id: string }>();
  const conversation = useConversationsStore((s) =>
    s.conversations.find((c) => c.id === id),
  );
  const removeMember = useConversationsStore((s) => s.removeMember);
  const addMembers = useConversationsStore((s) => s.addMembers);
  const deleteConversation = useConversationsStore((s) => s.deleteConversation);
  const friends = useFriendsStore((s) => s.friends);

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');

  if (!conversation || conversation.kind !== 'group') {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Stack.Screen options={{ title: 'Members' }} />
        <Text variant="h4">Group not found</Text>
      </View>
    );
  }

  const group = conversation as GroupConversation;
  const memberFriendIds = new Set(
    group.members.map((m) => m.id).filter((m): m is string => Boolean(m)),
  );
  const candidates = friends.filter((f) => !memberFriendIds.has(f.id));

  const onRemove = (memberId: string | undefined, name: string) => {
    if (!memberId) return;
    Alert.alert(`Remove ${name}?`, `${name} won't see new messages in ${group.name}.`, [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Remove',
        style: 'destructive',
        onPress: () => removeMember(group.id, memberId),
      },
    ]);
  };

  const onAddSelected = (friendId: string) => {
    const f = friends.find((x) => x.id === friendId);
    if (f) addMembers(group.id, [f]);
  };

  const onDeleteGroup = () => {
    Alert.alert(
      `Delete ${group.name}?`,
      `This can't be undone. All ${group.members.length + 1} members will lose access to the chat.`,
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Delete group',
          style: 'destructive',
          onPress: () => {
            deleteConversation(group.id);
            // Pop twice: members → conversation → list
            router.dismissAll();
          },
        },
      ],
    );
  };

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12">
      <Stack.Screen options={{ title: group.name }} />

      <View className="items-center gap-3">
        <View
          style={{
            width: 80,
            height: 80,
            borderRadius: 40,
            backgroundColor: 'rgba(30, 64, 175, 0.10)',
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          {group.emoji ? (
            <Text style={{ fontSize: 36 }}>{group.emoji}</Text>
          ) : (
            <Text className="text-2xl font-bold" style={{ color: '#1e40af' }}>
              {group.name
                .split(/\s+/)
                .map((w) => w[0])
                .join('')
                .slice(0, 2)
                .toUpperCase()}
            </Text>
          )}
        </View>
        <View className="items-center gap-1">
          <Text variant="h3">{group.name}</Text>
          <Text variant="muted">
            {group.members.length + 1} members
            {group.voiceRoom ? ` · ${group.voiceRoom.participants} in voice` : ''}
          </Text>
        </View>
      </View>

      <View className="gap-2">
        <Text
          variant="small"
          className="font-semibold uppercase tracking-wider text-muted-foreground">
          Members ({group.members.length + 1})
        </Text>
        <Card>
          <CardContent className="p-0">
            {/* Self row — admin, can't remove */}
            <View className="flex-row items-center gap-3 px-4 py-3.5">
              <View
                style={{
                  width: 40,
                  height: 40,
                  borderRadius: 20,
                  backgroundColor: ME.color,
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                <Text className="text-sm font-semibold text-white">{ME.initials}</Text>
              </View>
              <View className="flex-1 gap-0.5">
                <View className="flex-row items-center gap-1.5">
                  <Text className="font-medium" style={{ color: fg }}>
                    {ME.name}
                  </Text>
                  <Crown size={13} color="#ca8a04" fill="#facc15" />
                </View>
                <Text variant="small" style={{ color: mutedFg }}>
                  Admin · created the group
                </Text>
              </View>
            </View>

            {group.members.map((m) => (
              <React.Fragment key={m.id ?? m.name}>
                <Separator />
                <View className="flex-row items-center gap-3 px-4 py-3.5">
                  <View
                    style={{
                      width: 40,
                      height: 40,
                      borderRadius: 20,
                      backgroundColor: m.color,
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}>
                    <Text className="text-sm font-semibold text-white">{m.initials}</Text>
                  </View>
                  <View className="flex-1 gap-0.5">
                    <Text className="font-medium" style={{ color: fg }}>
                      {m.name}
                    </Text>
                    <Text variant="small" style={{ color: mutedFg }}>
                      Member
                    </Text>
                  </View>
                  <Pressable
                    onPress={() => onRemove(m.id, m.name)}
                    hitSlop={6}
                    accessibilityRole="button"
                    accessibilityLabel={`Remove ${m.name}`}>
                    <UserMinus size={18} color={destructive} />
                  </Pressable>
                </View>
              </React.Fragment>
            ))}
          </CardContent>
        </Card>
      </View>

      {candidates.length > 0 ? (
        <View className="gap-2">
          <Text
            variant="small"
            className="font-semibold uppercase tracking-wider text-muted-foreground">
            Add members
          </Text>
          <Card>
            <CardContent className="p-0">
              {candidates.slice(0, 6).map((f, i) => (
                <React.Fragment key={f.id}>
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
                      <Text className="text-sm font-semibold text-white">{f.initials}</Text>
                    </View>
                    <View className="flex-1 gap-0.5">
                      <View className="flex-row items-center gap-1.5">
                        <Text className="font-medium" style={{ color: fg }}>
                          {f.name}
                        </Text>
                        {f.statusEmoji ? <Text>{f.statusEmoji}</Text> : null}
                      </View>
                      <Text variant="small" style={{ color: mutedFg }} numberOfLines={1}>
                        {f.status ?? 'Friend'}
                      </Text>
                    </View>
                    <Button size="sm" variant="outline" onPress={() => onAddSelected(f.id)}>
                      <UserPlus size={14} color={fg} />
                      <Text>Add</Text>
                    </Button>
                  </View>
                </React.Fragment>
              ))}
            </CardContent>
          </Card>
        </View>
      ) : null}

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
                Delete group
              </Text>
              <Text variant="small" style={{ color: mutedFg }}>
                Permanently removes this group and all messages for everyone in it. This can&apos;t
                be undone.
              </Text>
            </View>
            <View className="flex-row justify-end">
              <Button size="sm" variant="destructive" onPress={onDeleteGroup}>
                <Trash2 size={14} color="#ffffff" />
                <Text>Delete group</Text>
              </Button>
            </View>
          </CardContent>
        </Card>
      </View>
    </ScrollView>
  );
}
