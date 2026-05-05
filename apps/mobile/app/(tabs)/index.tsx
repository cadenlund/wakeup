// Phase 1.4 preview of the Conversations tab (§5.1 / §1). Replaced
// by the real route in Phase 6 when the messages API hooks land.
// The data here is mocked but the surface is honest — pinned vs
// recent grouping, group-chat composite avatars, typing indicators,
// per-row mute state, and the active-voice-room banner are all what
// the production tab will render once the WS dispatcher is wired.
import { Stack, useRouter } from 'expo-router';
import {
  BellOff,
  MessageSquarePlus,
  Mic,
  Pin,
  PinOff,
  Search,
  Trash2,
} from 'lucide-react-native';
import * as React from 'react';
import { Alert, Pressable, ScrollView, View } from 'react-native';
import ReanimatedSwipeable, {
  type SwipeableMethods,
} from 'react-native-gesture-handler/ReanimatedSwipeable';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { Text } from '@/components/ui/text';
import {
  useConversationsStore,
  type Conversation,
  type Presence,
} from '@/lib/mock/conversations';
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

function DmAvatar({
  name,
  initials,
  presence,
}: {
  name: string;
  initials: string;
  presence: Presence;
}) {
  return (
    <View style={{ position: 'relative' }}>
      <View
        style={{
          width: 48,
          height: 48,
          borderRadius: 24,
          backgroundColor: avatarColor(name),
          alignItems: 'center',
          justifyContent: 'center',
        }}>
        <Text className="font-semibold text-white">{initials}</Text>
      </View>
      <View
        style={{
          position: 'absolute',
          right: -2,
          bottom: -2,
          width: 12,
          height: 12,
          borderRadius: 6,
          backgroundColor: PRESENCE_TO_COLOR[presence],
          borderWidth: 2,
          borderColor: '#ffffff',
        }}
      />
    </View>
  );
}

function GroupAvatar({ members }: { members: { initials: string; color: string }[] }) {
  // Stack 2 of the up-to-3 member tiles into a 48x48 grid corner.
  const m = members.slice(0, 2);
  return (
    <View style={{ width: 48, height: 48, position: 'relative' }}>
      <View
        style={{
          position: 'absolute',
          top: 0,
          left: 0,
          width: 32,
          height: 32,
          borderRadius: 16,
          backgroundColor: m[0]?.color ?? '#94a3b8',
          alignItems: 'center',
          justifyContent: 'center',
          borderWidth: 2,
          borderColor: '#ffffff',
          zIndex: 2,
        }}>
        <Text className="text-xs font-semibold text-white">{m[0]?.initials ?? '·'}</Text>
      </View>
      <View
        style={{
          position: 'absolute',
          bottom: 0,
          right: 0,
          width: 32,
          height: 32,
          borderRadius: 16,
          backgroundColor: m[1]?.color ?? '#94a3b8',
          alignItems: 'center',
          justifyContent: 'center',
          borderWidth: 2,
          borderColor: '#ffffff',
        }}>
        <Text className="text-xs font-semibold text-white">{m[1]?.initials ?? '·'}</Text>
      </View>
    </View>
  );
}

type RowSwipeActionProps = {
  bg: string;
  icon: React.ReactNode;
  label: string;
  onPress: () => void;
};

function RowSwipeAction({ bg, icon, label, onPress }: RowSwipeActionProps) {
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

function ConversationRow({ convo }: { convo: Conversation }) {
  const router = useRouter();
  const swipeRef = React.useRef<SwipeableMethods>(null);
  const mutedFg = useThemeColor('muted-foreground');
  const isTyping = convo.typing && convo.typing.length > 0;
  const ringColor = useThemeColor('ring');
  // Opaque scheme-aware background so the swipe-revealed action
  // buttons under the row don't bleed through. Was hard-coded
  // '#ffffff' which broke every dark scheme.
  const rowBg = useThemeColor('background');

  const togglePin = useConversationsStore((s) => s.togglePin);
  const toggleMute = useConversationsStore((s) => s.toggleMute);
  const deleteConversation = useConversationsStore((s) => s.deleteConversation);

  const closeSwipe = React.useCallback(() => swipeRef.current?.close(), []);

  const onPin = React.useCallback(() => {
    togglePin(convo.id);
    closeSwipe();
  }, [togglePin, convo.id, closeSwipe]);

  const onMute = React.useCallback(() => {
    toggleMute(convo.id);
    closeSwipe();
  }, [toggleMute, convo.id, closeSwipe]);

  const onDelete = React.useCallback(() => {
    Alert.alert(
      `Delete this ${convo.kind === 'group' ? 'group' : 'conversation'}?`,
      convo.kind === 'group'
        ? `${convo.name} will be removed from your list. The other ${convo.members.length} members keep the chat.`
        : `Your messages with ${convo.name} will be removed from your list. They won't be notified.`,
      [
        { text: 'Cancel', style: 'cancel', onPress: closeSwipe },
        {
          text: 'Delete',
          style: 'destructive',
          onPress: () => deleteConversation(convo.id),
        },
      ],
    );
  }, [convo, deleteConversation, closeSwipe]);

  const renderRightActions = React.useCallback(
    () => (
      <View style={{ flexDirection: 'row' }}>
        <RowSwipeAction
          bg="#facc15"
          icon={
            convo.pinned ? (
              <PinOff size={20} color="#ffffff" />
            ) : (
              <Pin size={20} color="#ffffff" fill="#ffffff" />
            )
          }
          label={convo.pinned ? 'Unpin' : 'Pin'}
          onPress={onPin}
        />
        <RowSwipeAction
          bg="#94a3b8"
          icon={<BellOff size={20} color="#ffffff" />}
          label={convo.muted ? 'Unmute' : 'Mute'}
          onPress={onMute}
        />
        <RowSwipeAction
          bg="#dc2626"
          icon={<Trash2 size={20} color="#ffffff" />}
          label="Delete"
          onPress={onDelete}
        />
      </View>
    ),
    [convo.pinned, convo.muted, onPin, onMute, onDelete],
  );

  return (
    <ReanimatedSwipeable
      ref={swipeRef}
      friction={2}
      rightThreshold={40}
      overshootRight={false}
      renderRightActions={renderRightActions}>
      <Pressable
        onPress={() => router.push(`/conversation/${convo.id}`)}
        className="flex-row items-center gap-3 px-4 py-3 active:bg-muted"
        style={{ backgroundColor: rowBg }}>
        {convo.kind === 'dm' ? (
        <DmAvatar name={convo.name} initials={convo.initials} presence={convo.presence} />
      ) : (
        <GroupAvatar members={convo.members} />
      )}

      <View className="flex-1 gap-0.5">
        <View className="flex-row items-center justify-between">
          <View className="flex-1 flex-row items-center gap-1.5">
            <Text className="font-semibold text-foreground" numberOfLines={1}>
              {convo.name}
            </Text>
            {convo.kind === 'dm' && convo.statusEmoji ? <Text>{convo.statusEmoji}</Text> : null}
            {convo.muted ? <BellOff size={13} color={mutedFg} /> : null}
            {convo.voiceRoom ? (
              <View
                style={{
                  flexDirection: 'row',
                  alignItems: 'center',
                  gap: 3,
                  paddingHorizontal: 6,
                  paddingVertical: 1,
                  borderRadius: 999,
                  backgroundColor: 'rgba(34, 197, 94, 0.14)',
                }}>
                <Mic size={10} color="#15803d" />
                <Text className="text-[10px] font-semibold" style={{ color: '#15803d' }}>
                  {convo.voiceRoom.participants}
                </Text>
              </View>
            ) : null}
          </View>
          <Text variant="small" className="text-muted-foreground">
            {convo.lastTime}
          </Text>
        </View>

        <View className="flex-row items-center justify-between gap-2">
          {isTyping ? (
            <View className="flex-1 flex-row items-center gap-1.5">
              <View className="flex-row gap-0.5">
                <View style={{ width: 4, height: 4, borderRadius: 2, backgroundColor: ringColor }} />
                <View
                  style={{
                    width: 4,
                    height: 4,
                    borderRadius: 2,
                    backgroundColor: ringColor,
                    opacity: 0.7,
                  }}
                />
                <View
                  style={{
                    width: 4,
                    height: 4,
                    borderRadius: 2,
                    backgroundColor: ringColor,
                    opacity: 0.4,
                  }}
                />
              </View>
              <Text
                variant="small"
                className="flex-1 italic"
                style={{ color: ringColor }}
                numberOfLines={1}>
                {convo.typing!.join(', ')} {convo.typing!.length > 1 ? 'are' : 'is'} typing
              </Text>
            </View>
          ) : (
            <Text
              variant="small"
              className="flex-1 text-muted-foreground"
              numberOfLines={1}
              style={convo.unread ? { color: useThemeColor('foreground') } : undefined}>
              {convo.preview}
            </Text>
          )}
          {convo.unread ? (
            <Badge>
              <Text>{convo.unread}</Text>
            </Badge>
          ) : null}
        </View>
      </View>
      </Pressable>
    </ReanimatedSwipeable>
  );
}

function SectionLabel({
  count,
  label,
  Icon,
}: {
  count: number;
  label: string;
  Icon?: React.ComponentType<{ size?: number; color?: string }>;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-row items-center gap-1.5 px-4 pb-2 pt-4">
      {Icon ? <Icon size={12} color={mutedFg} /> : null}
      <Text
        variant="small"
        className="font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      <Text variant="small" className="text-muted-foreground">
        ({count})
      </Text>
    </View>
  );
}

export default function ConversationsScreen() {
  const router = useRouter();
  const conversations = useConversationsStore((s) => s.conversations);
  const [query, setQuery] = React.useState('');
  const mutedFg = useThemeColor('muted-foreground');
  const fg = useThemeColor('foreground');

  const matches = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return conversations;
    return conversations.filter((c) => c.name.toLowerCase().includes(q));
  }, [conversations, query]);
  const pinned = matches.filter((c) => c.pinned);
  const recent = matches.filter((c) => !c.pinned);
  const activeRoom = matches.find((c) => c.voiceRoom);

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="pb-12"
      keyboardShouldPersistTaps="handled">
      <Stack.Screen
        options={{
          title: 'Messages',
          headerRight: () => (
            <Pressable
              onPress={() => router.push('/group/new')}
              accessibilityRole="button"
              accessibilityLabel="New group"
              hitSlop={8}
              style={{ marginRight: 14 }}>
              <MessageSquarePlus size={20} color={fg} strokeWidth={2.25} />
            </Pressable>
          ),
        }}
      />

      {activeRoom ? (
        <View className="px-4 pt-4">
          <Card style={{ borderColor: 'rgba(34, 197, 94, 0.35)' }}>
            <CardContent className="flex-row items-center gap-3 py-3">
              <View
                style={{
                  width: 36,
                  height: 36,
                  borderRadius: 18,
                  backgroundColor: 'rgba(34, 197, 94, 0.14)',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}>
                <Mic size={18} color="#15803d" />
              </View>
              <View className="flex-1 gap-0.5">
                <Text className="font-semibold text-foreground">
                  Voice room in {activeRoom.name}
                </Text>
                <Text variant="small" className="text-muted-foreground">
                  {activeRoom.voiceRoom?.participants} in the room — tap to join
                </Text>
              </View>
              <View
                style={{
                  paddingHorizontal: 12,
                  paddingVertical: 6,
                  borderRadius: 999,
                  backgroundColor: '#16a34a',
                }}>
                <Text className="text-xs font-semibold text-white">Join</Text>
              </View>
            </CardContent>
          </Card>
        </View>
      ) : null}

      <View className="px-4 pt-4">
        <View className="relative">
          <View style={{ position: 'absolute', top: 12, left: 12, zIndex: 1 }}>
            <Search size={18} color={mutedFg} />
          </View>
          <Input
            value={query}
            onChangeText={setQuery}
            placeholder="Search messages"
            autoCapitalize="none"
            className="pl-10"
          />
        </View>
      </View>

      {pinned.length > 0 ? (
        <>
          <SectionLabel count={pinned.length} label="Pinned" Icon={Pin} />
          {pinned.map((c, i) => (
            <React.Fragment key={c.id}>
              {i > 0 ? <Separator /> : null}
              <ConversationRow convo={c} />
            </React.Fragment>
          ))}
        </>
      ) : null}

      {recent.length > 0 ? (
        <>
          <SectionLabel count={recent.length} label="Recent" />
          {recent.map((c, i) => (
            <React.Fragment key={c.id}>
              {i > 0 ? <Separator /> : null}
              <ConversationRow convo={c} />
            </React.Fragment>
          ))}
        </>
      ) : null}
    </ScrollView>
  );
}
