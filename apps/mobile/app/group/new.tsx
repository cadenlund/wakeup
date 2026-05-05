// Create-group modal — pick friends, name the group, optionally pick
// an emoji. Mock-only for now: hits useConversationsStore.createGroup
// and navigates into the new thread. Production wiring lands at Phase
// 6 against POST /v1/conversations.
import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import { Check, Search, X } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useConversationsStore } from '@/lib/mock/conversations';
import { useFriendsStore, type Friend, type Presence } from '@/lib/mock/friends';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const PRESENCE_TO_COLOR: Record<Presence, string> = {
  online: '#22c55e',
  away: '#f59e0b',
  sleeping: '#a855f7',
  dnd: '#ef4444',
  offline: '#94a3b8',
};

const AVATAR_PALETTE = ['#fb923c', '#a855f7', '#22d3ee', '#f472b6', '#34d399', '#facc15'];
function avatarColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return AVATAR_PALETTE[h % AVATAR_PALETTE.length] as string;
}

const EMOJI_OPTIONS = ['🎶', '🌙', '🎧', '☕', '🔥', '🧠', '🚀', '✨', '🎮', '📚', '🌅', '💤'];

export default function CreateGroupScreen() {
  const router = useRouter();
  const params = useLocalSearchParams<{ seed?: string }>();
  const friends = useFriendsStore((s) => s.friends);
  const createGroup = useConversationsStore((s) => s.createGroup);

  // If launched from a DM ("Add people"), the seeding friend is
  // pre-selected and the input renders with a chip already in place.
  const [selected, setSelected] = React.useState<Set<string>>(() => {
    return new Set(params.seed ? [params.seed] : []);
  });
  const [name, setName] = React.useState('');
  const [emoji, setEmoji] = React.useState<string | undefined>(undefined);
  const [query, setQuery] = React.useState('');

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const bg = useThemeColor('background');
  const ring = useThemeColor('ring');

  const matches = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return friends;
    return friends.filter((f) => f.name.toLowerCase().includes(q));
  }, [friends, query]);

  const selectedFriends = React.useMemo(
    () => friends.filter((f) => selected.has(f.id)),
    [friends, selected],
  );

  const toggle = React.useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const canCreate = selected.size >= 1;

  const onCreate = React.useCallback(() => {
    if (!canCreate) return;
    const id = createGroup({ name, emoji, members: selectedFriends });
    router.dismiss();
    router.push(`/conversation/${id}`);
  }, [canCreate, createGroup, name, emoji, selectedFriends, router]);

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen
        options={{
          presentation: 'modal',
          headerShown: true,
          title: 'New group',
          headerStyle: { backgroundColor: bg },
          headerShadowVisible: false,
          headerTintColor: fg,
          headerTitleStyle: { color: fg, fontWeight: '600' },
          headerLeft: () => (
            <Pressable
              onPress={() => router.back()}
              hitSlop={10}
              accessibilityLabel="Cancel"
              style={{ marginLeft: 8 }}>
              <X size={22} color={fg} strokeWidth={2.25} />
            </Pressable>
          ),
          headerRight: () => (
            <Pressable
              onPress={onCreate}
              disabled={!canCreate}
              hitSlop={10}
              accessibilityLabel="Create group"
              style={{ marginRight: 14, opacity: canCreate ? 1 : 0.45 }}>
              <Text style={{ color: ring, fontWeight: '700' }}>Create</Text>
            </Pressable>
          ),
        }}
      />

      <ScrollView
        contentContainerClassName="px-4 py-5 gap-5 pb-12"
        keyboardShouldPersistTaps="handled">
        <View className="items-center gap-3">
          <Pressable
            onPress={() => {
              setEmoji(undefined);
            }}
            style={{
              width: 88,
              height: 88,
              borderRadius: 44,
              backgroundColor: emoji ? 'rgba(30, 64, 175, 0.10)' : 'rgba(100, 116, 139, 0.10)',
              alignItems: 'center',
              justifyContent: 'center',
              borderWidth: 1,
              borderColor: 'rgba(0,0,0,0.08)',
            }}>
            {emoji ? (
              <Text style={{ fontSize: 38 }}>{emoji}</Text>
            ) : (
              <Text variant="small" style={{ color: mutedFg, fontWeight: '600' }}>
                Pick
              </Text>
            )}
          </Pressable>
          <View style={{ flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', gap: 8 }}>
            {EMOJI_OPTIONS.map((e) => {
              const active = emoji === e;
              return (
                <Pressable
                  key={e}
                  onPress={() => setEmoji(e)}
                  accessibilityRole="button"
                  accessibilityState={{ selected: active }}
                  style={{
                    width: 38,
                    height: 38,
                    borderRadius: 12,
                    alignItems: 'center',
                    justifyContent: 'center',
                    backgroundColor: active ? ring : 'transparent',
                    borderWidth: 1,
                    borderColor: active ? ring : 'rgba(100, 116, 139, 0.20)',
                  }}>
                  <Text style={{ fontSize: 20 }}>{e}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>

        <View className="gap-1.5">
          <Text variant="small" style={{ color: mutedFg, fontWeight: '600' }}>
            Group name
          </Text>
          <Input
            value={name}
            onChangeText={setName}
            placeholder="Sunday club, Sleep nerds, …"
            autoCapitalize="words"
            maxLength={48}
          />
        </View>

        {selectedFriends.length > 0 ? (
          <View className="gap-2">
            <Text variant="small" style={{ color: mutedFg, fontWeight: '600' }}>
              Members ({selectedFriends.length})
            </Text>
            <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>
              {selectedFriends.map((f) => (
                <Pressable
                  key={f.id}
                  onPress={() => toggle(f.id)}
                  accessibilityRole="button"
                  accessibilityLabel={`Remove ${f.name}`}
                  style={{
                    flexDirection: 'row',
                    alignItems: 'center',
                    gap: 6,
                    paddingLeft: 4,
                    paddingRight: 10,
                    paddingVertical: 4,
                    borderRadius: 999,
                    backgroundColor: 'rgba(30, 64, 175, 0.10)',
                  }}>
                  <View
                    style={{
                      width: 24,
                      height: 24,
                      borderRadius: 12,
                      backgroundColor: avatarColor(f.name),
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}>
                    <Text className="text-[10px] font-semibold text-white">{f.initials}</Text>
                  </View>
                  <Text style={{ color: ring, fontSize: 13, fontWeight: '600' }}>{f.name}</Text>
                  <X size={14} color={ring} />
                </Pressable>
              ))}
            </View>
          </View>
        ) : null}

        <View className="gap-2">
          <Text variant="small" style={{ color: mutedFg, fontWeight: '600' }}>
            Add friends
          </Text>
          <View style={{ position: 'relative' }}>
            <View style={{ position: 'absolute', top: 12, left: 12, zIndex: 1 }}>
              <Search size={18} color={mutedFg} />
            </View>
            <Input
              value={query}
              onChangeText={setQuery}
              placeholder="Search friends"
              autoCapitalize="none"
              className="pl-10"
            />
          </View>

          <View className="gap-0">
            {matches.length === 0 ? (
              <View className="items-center py-8">
                <Text variant="small" style={{ color: mutedFg }}>
                  No friends match &ldquo;{query.trim()}&rdquo;.
                </Text>
              </View>
            ) : (
              matches.map((f, i) => {
                const checked = selected.has(f.id);
                return (
                  <React.Fragment key={f.id}>
                    {i > 0 ? <Separator /> : null}
                    <FriendPickerRow friend={f} checked={checked} onToggle={() => toggle(f.id)} />
                  </React.Fragment>
                );
              })
            )}
          </View>
        </View>
      </ScrollView>

      <View
        style={{
          paddingHorizontal: 16,
          paddingTop: 10,
          paddingBottom: 14,
          borderTopWidth: 1,
          borderTopColor: 'rgba(0,0,0,0.06)',
          backgroundColor: bg,
        }}>
        <Button onPress={onCreate} disabled={!canCreate}>
          <Text>
            {canCreate
              ? `Create group · ${selected.size + 1} ${selected.size + 1 === 1 ? 'member' : 'members'}`
              : 'Pick at least 1 friend'}
          </Text>
        </Button>
      </View>
    </View>
  );
}

function FriendPickerRow({
  friend,
  checked,
  onToggle,
}: {
  friend: Friend;
  checked: boolean;
  onToggle: () => void;
}) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const ring = useThemeColor('ring');
  return (
    <Pressable
      onPress={onToggle}
      accessibilityRole="button"
      accessibilityState={{ checked }}
      className="flex-row items-center gap-3 px-1 py-3 active:bg-muted">
      <View style={{ position: 'relative' }}>
        <View
          style={{
            width: 40,
            height: 40,
            borderRadius: 20,
            backgroundColor: avatarColor(friend.name),
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <Text className="text-sm font-semibold text-white">{friend.initials}</Text>
        </View>
        <View
          style={{
            position: 'absolute',
            right: -2,
            bottom: -2,
            width: 12,
            height: 12,
            borderRadius: 6,
            backgroundColor: PRESENCE_TO_COLOR[friend.presence],
            borderWidth: 2,
            borderColor: '#ffffff',
          }}
        />
      </View>
      <View className="flex-1 gap-0.5">
        <View className="flex-row items-center gap-1.5">
          <Text className="font-medium" style={{ color: fg }}>
            {friend.name}
          </Text>
          {friend.statusEmoji ? <Text>{friend.statusEmoji}</Text> : null}
          {friend.isFavorite ? (
            <Badge variant="secondary">
              <Text>★</Text>
            </Badge>
          ) : null}
        </View>
        {friend.status ? (
          <Text variant="small" style={{ color: mutedFg }} numberOfLines={1}>
            {friend.status}
          </Text>
        ) : null}
      </View>
      <View
        style={{
          width: 24,
          height: 24,
          borderRadius: 12,
          alignItems: 'center',
          justifyContent: 'center',
          backgroundColor: checked ? ring : 'transparent',
          borderWidth: 1.5,
          borderColor: checked ? ring : 'rgba(100, 116, 139, 0.40)',
        }}>
        {checked ? <Check size={14} color="#ffffff" strokeWidth={3} /> : null}
      </View>
    </Pressable>
  );
}
