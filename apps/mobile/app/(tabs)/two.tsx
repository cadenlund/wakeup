// Phase 1.4 preview of the Friends tab (§5.2 / §1). Replaced by the
// real route in Phase 7 when the friends API hooks land. The data
// here is mocked through lib/mock/friends; the surface (favorites
// section, presence dots, status emojis, swipe actions, star
// toggle, invite modal entry) is what the production tab will
// render once the hooks return real rows + mutations.
import { Stack, useRouter } from 'expo-router';
import {
  Ban,
  BellOff,
  Phone,
  Search,
  Star,
  Trash2,
  UserPlus,
} from 'lucide-react-native';
import * as React from 'react';
import { Alert, Pressable, ScrollView, View } from 'react-native';
import ReanimatedSwipeable, {
  type SwipeableMethods,
} from 'react-native-gesture-handler/ReanimatedSwipeable';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import { useFriendsStore, type Friend, type Presence } from '@/lib/mock/friends';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const AVATAR_PALETTE = ['#fb923c', '#a855f7', '#22d3ee', '#f472b6', '#34d399', '#facc15'];
function avatarColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return AVATAR_PALETTE[h % AVATAR_PALETTE.length] as string;
}

const PRESENCE_TO_COLOR: Record<Presence, string> = {
  online: '#22c55e',
  away: '#f59e0b',
  sleeping: '#a855f7',
  dnd: '#ef4444',
  offline: '#94a3b8',
};

const PRESENCE_LABEL: Record<Presence, string> = {
  online: 'Online',
  away: 'Away',
  sleeping: 'Sleeping',
  dnd: 'Do not disturb',
  offline: 'Offline',
};

function PresenceDot({ presence }: { presence: Presence }) {
  return (
    <View
      style={{
        width: 10,
        height: 10,
        borderRadius: 5,
        backgroundColor: PRESENCE_TO_COLOR[presence],
        borderWidth: 2,
        borderColor: '#ffffff',
      }}
    />
  );
}

function Avatar({ name, initials, presence }: { name: string; initials: string; presence: Presence }) {
  return (
    <View style={{ position: 'relative' }}>
      <View
        style={{
          width: 44,
          height: 44,
          borderRadius: 22,
          backgroundColor: avatarColor(name),
          alignItems: 'center',
          justifyContent: 'center',
        }}>
        <Text className="font-semibold text-white">{initials}</Text>
      </View>
      <View style={{ position: 'absolute', right: -2, bottom: -2 }}>
        <PresenceDot presence={presence} />
      </View>
    </View>
  );
}

type SwipeActionProps = {
  bg: string;
  icon: React.ReactNode;
  label: string;
  onPress: () => void;
};

function SwipeAction({ bg, icon, label, onPress }: SwipeActionProps) {
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      style={{
        width: 72,
        height: '100%',
        alignItems: 'center',
        justifyContent: 'center',
        backgroundColor: bg,
        gap: 4,
      }}>
      {icon}
      <Text style={{ color: '#ffffff', fontSize: 11, fontWeight: '600' }}>{label}</Text>
    </Pressable>
  );
}

function FriendRow({ friend }: { friend: Friend }) {
  const swipeRef = React.useRef<SwipeableMethods>(null);
  const mutedFg = useThemeColor('muted-foreground');
  const ring = useThemeColor('ring');
  // Opaque scheme-aware background so the swipe-revealed action
  // buttons under the row don't bleed through. Was hard-coded
  // '#ffffff' which broke every dark scheme.
  const rowBg = useThemeColor('background');
  const dimmed = friend.presence === 'offline';

  const toggleFavorite = useFriendsStore((s) => s.toggleFavorite);
  const toggleMute = useFriendsStore((s) => s.toggleMute);
  const removeFriend = useFriendsStore((s) => s.removeFriend);
  const blockFriend = useFriendsStore((s) => s.blockFriend);

  const closeSwipe = React.useCallback(() => swipeRef.current?.close(), []);

  const onFavorite = React.useCallback(() => {
    toggleFavorite(friend.id);
    closeSwipe();
  }, [friend.id, toggleFavorite, closeSwipe]);

  const onMute = React.useCallback(() => {
    toggleMute(friend.id);
    closeSwipe();
  }, [friend.id, toggleMute, closeSwipe]);

  const onDelete = React.useCallback(() => {
    Alert.alert(
      `Remove ${friend.name}?`,
      "You'll be unfriended on both sides. They won't be notified.",
      [
        { text: 'Cancel', style: 'cancel', onPress: closeSwipe },
        {
          text: 'Remove',
          style: 'destructive',
          onPress: () => {
            removeFriend(friend.id);
            // Row unmounts on remove, no need to close the swipe.
          },
        },
      ],
    );
  }, [friend.id, friend.name, removeFriend, closeSwipe]);

  const onBlock = React.useCallback(() => {
    Alert.alert(
      `Block ${friend.name}?`,
      "They won't be able to message or call you, and they won't see your status. You can unblock them later from Settings · Privacy.",
      [
        { text: 'Cancel', style: 'cancel', onPress: closeSwipe },
        {
          text: 'Block',
          style: 'destructive',
          onPress: () => {
            blockFriend(friend.id);
          },
        },
      ],
    );
  }, [friend.id, friend.name, blockFriend, closeSwipe]);

  const renderRightActions = React.useCallback(
    () => (
      <View style={{ flexDirection: 'row' }}>
        <SwipeAction
          bg="#facc15"
          icon={<Star size={20} color="#ffffff" fill="#ffffff" />}
          label={friend.isFavorite ? 'Unstar' : 'Star'}
          onPress={onFavorite}
        />
        <SwipeAction
          bg="#94a3b8"
          icon={<BellOff size={20} color="#ffffff" />}
          label={friend.isMuted ? 'Unmute' : 'Mute'}
          onPress={onMute}
        />
        <SwipeAction
          bg="#f97316"
          icon={<Trash2 size={20} color="#ffffff" />}
          label="Remove"
          onPress={onDelete}
        />
        <SwipeAction
          bg="#dc2626"
          icon={<Ban size={20} color="#ffffff" />}
          label="Block"
          onPress={onBlock}
        />
      </View>
    ),
    [friend.isFavorite, friend.isMuted, onFavorite, onMute, onDelete, onBlock],
  );

  return (
    <ReanimatedSwipeable
      ref={swipeRef}
      friction={2}
      rightThreshold={40}
      overshootRight={false}
      renderRightActions={renderRightActions}>
      <Pressable
        className="flex-row items-center gap-3 px-4 py-3 active:bg-muted"
        style={{ opacity: dimmed ? 0.65 : 1, backgroundColor: rowBg }}>
        <Avatar name={friend.name} initials={friend.initials} presence={friend.presence} />

        <View className="flex-1 gap-0.5">
          <View className="flex-row items-center gap-1.5">
            <Text className="font-semibold text-foreground" numberOfLines={1}>
              {friend.name}
            </Text>
            {friend.statusEmoji ? <Text>{friend.statusEmoji}</Text> : null}
            {friend.isMuted ? <BellOff size={13} color={mutedFg} /> : null}
          </View>
          <Text variant="small" className="text-muted-foreground" numberOfLines={1}>
            {friend.status ?? PRESENCE_LABEL[friend.presence]}
          </Text>
        </View>

        {friend.unread ? (
          <Badge>
            <Text>{friend.unread}</Text>
          </Badge>
        ) : null}

        <Pressable
          onPress={() => toggleFavorite(friend.id)}
          hitSlop={6}
          accessibilityRole="button"
          accessibilityLabel={friend.isFavorite ? 'Remove from favorites' : 'Add to favorites'}>
          <Star
            size={20}
            color={friend.isFavorite ? '#facc15' : mutedFg}
            fill={friend.isFavorite ? '#facc15' : 'transparent'}
            strokeWidth={friend.isFavorite ? 1.5 : 2}
          />
        </Pressable>

        <Button variant="ghost" size="icon" className="h-9 w-9">
          <Phone size={18} color={ring} />
        </Button>
      </Pressable>
    </ReanimatedSwipeable>
  );
}

function SectionLabel({
  count,
  label,
  Icon,
  iconColor,
}: {
  count: number;
  label: string;
  Icon?: React.ComponentType<{ size?: number; color?: string; fill?: string }>;
  iconColor?: string;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-row items-center gap-1.5 px-4 pb-2 pt-4">
      {Icon ? <Icon size={12} color={iconColor ?? mutedFg} fill={iconColor} /> : null}
      <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      <Text variant="small" className="text-muted-foreground">
        ({count})
      </Text>
    </View>
  );
}

export default function FriendsScreen() {
  const router = useRouter();
  const friends = useFriendsStore((s) => s.friends);
  const [query, setQuery] = React.useState('');
  const mutedFg = useThemeColor('muted-foreground');
  const fg = useThemeColor('foreground');

  const matches = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return friends;
    return friends.filter((f) => f.name.toLowerCase().includes(q));
  }, [friends, query]);

  // Favorites surface as their own section above online/offline. To
  // avoid duplicate rows, the online/offline buckets exclude any
  // friend already in favorites.
  const favorites = matches.filter((f) => f.isFavorite);
  const online = matches.filter((f) => !f.isFavorite && f.presence !== 'offline');
  const offline = matches.filter((f) => !f.isFavorite && f.presence === 'offline');
  const noMatches = query.length > 0 && matches.length === 0;

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="pb-12"
      keyboardShouldPersistTaps="handled">
      <Stack.Screen
        options={{
          title: 'Friends',
          headerRight: () => (
            <Pressable
              onPress={() => router.push('/invite-friend')}
              hitSlop={8}
              accessibilityLabel="Invite a friend"
              style={{ marginRight: 14, flexDirection: 'row', alignItems: 'center', gap: 4 }}>
              <UserPlus size={18} color={fg} strokeWidth={2.25} />
              <Text style={{ color: fg, fontWeight: '600' }}>Invite</Text>
            </Pressable>
          ),
        }}
      />

      <View className="px-4 pt-4">
        <Card>
          <CardHeader>
            <View className="flex-row items-center justify-between">
              <View className="flex-row items-center gap-3">
                <View
                  style={{
                    width: 36,
                    height: 36,
                    borderRadius: 18,
                    backgroundColor: 'rgba(34, 197, 94, 0.12)',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}>
                  <UserPlus size={18} color="#16a34a" />
                </View>
                <View>
                  <CardTitle className="text-base">2 friend requests</CardTitle>
                  <CardDescription>From Lily and Owen</CardDescription>
                </View>
              </View>
              <Button size="sm">
                <Text>Review</Text>
              </Button>
            </View>
          </CardHeader>
        </Card>
      </View>

      <View className="gap-2 px-4 pt-4">
        <View className="relative">
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
      </View>

      {noMatches ? (
        <EmptyState
          icon={<Search size={32} color={mutedFg} />}
          title="No matches"
          subtitle={`Nothing for "${query.trim()}". Check the spelling, or send them an invite.`}
          cta={{
            label: 'Invite by email',
            onPress: () => {
              setQuery('');
              router.push('/invite-friend');
            },
          }}
        />
      ) : (
        <>
          {favorites.length > 0 ? (
            <>
              <SectionLabel
                count={favorites.length}
                label="Favorites"
                Icon={Star}
                iconColor="#facc15"
              />
              {favorites.map((f, i) => (
                <React.Fragment key={f.id}>
                  {i > 0 ? <Separator /> : null}
                  <FriendRow friend={f} />
                </React.Fragment>
              ))}
            </>
          ) : null}

          {online.length > 0 ? (
            <>
              <SectionLabel count={online.length} label="Online now" />
              {online.map((f, i) => (
                <React.Fragment key={f.id}>
                  {i > 0 ? <Separator /> : null}
                  <FriendRow friend={f} />
                </React.Fragment>
              ))}
            </>
          ) : null}

          {offline.length > 0 ? (
            <>
              <SectionLabel count={offline.length} label="Offline" />
              {offline.map((f, i) => (
                <React.Fragment key={f.id}>
                  {i > 0 ? <Separator /> : null}
                  <FriendRow friend={f} />
                </React.Fragment>
              ))}
            </>
          ) : null}

          <View className="mt-8 px-4">
            <Card>
              <CardContent className="items-center gap-3 pt-6">
                <View
                  style={{
                    width: 48,
                    height: 48,
                    borderRadius: 24,
                    backgroundColor: 'rgba(30, 64, 175, 0.10)',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}>
                  <UserPlus size={22} color={fg} />
                </View>
                <View className="items-center gap-1">
                  <Text className="font-semibold text-foreground">Find more friends</Text>
                  <Text variant="small" className="text-center text-muted-foreground">
                    Sync your contacts to see who&apos;s already on Wakeup, or invite someone by
                    email.
                  </Text>
                </View>
                <View className="flex-row gap-2 pt-2">
                  <Button size="sm" variant="outline">
                    <Text>Sync contacts</Text>
                  </Button>
                  <Button size="sm" onPress={() => router.push('/invite-friend')}>
                    <Text>Invite</Text>
                  </Button>
                </View>
              </CardContent>
            </Card>
          </View>
        </>
      )}
    </ScrollView>
  );
}
