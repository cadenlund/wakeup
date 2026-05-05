// Phase 1.4 preview of the conversation thread screen (§5.1 / §2).
// Replaced by the real route in Phase 6 when the messages API hooks
// land. The data here is mocked through lib/mock/conversations; the
// surface (bubble alignment, group sender labels, system events,
// reactions, composer, voice-room banner) tracks what the production
// thread will render once useGetMessages + useSendMessage are wired.
import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import {
  ArrowLeft,
  Check,
  CheckCheck,
  MoreVertical,
  Phone,
  Plus,
  Send,
  Smile,
  Video,
} from 'lucide-react-native';
import * as React from 'react';
import {
  ActionSheetIOS,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

import { Text } from '@/components/ui/text';
import {
  useConversationsStore,
  type Conversation,
  type Message,
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

const PRESENCE_LABEL: Record<Presence, string> = {
  online: 'Online',
  away: 'Away',
  sleeping: 'Sleeping',
  dnd: 'Do not disturb',
  offline: 'Offline',
};

const AVATAR_PALETTE = ['#fb923c', '#a855f7', '#22d3ee', '#f472b6', '#34d399', '#facc15'];
function avatarColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return AVATAR_PALETTE[h % AVATAR_PALETTE.length] as string;
}

function HeaderAvatar({ convo }: { convo: Conversation }) {
  if (convo.kind === 'dm') {
    return (
      <View style={{ position: 'relative' }}>
        <View
          style={{
            width: 36,
            height: 36,
            borderRadius: 18,
            backgroundColor: avatarColor(convo.name),
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <Text className="text-sm font-semibold text-white">{convo.initials}</Text>
        </View>
        <View
          style={{
            position: 'absolute',
            right: -1,
            bottom: -1,
            width: 10,
            height: 10,
            borderRadius: 5,
            backgroundColor: PRESENCE_TO_COLOR[convo.presence],
            borderWidth: 2,
            borderColor: '#ffffff',
          }}
        />
      </View>
    );
  }
  // Group: small stacked tile
  const m = convo.members.slice(0, 2);
  return (
    <View style={{ width: 36, height: 36, position: 'relative' }}>
      <View
        style={{
          position: 'absolute',
          top: 0,
          left: 0,
          width: 24,
          height: 24,
          borderRadius: 12,
          backgroundColor: m[0]?.color ?? '#94a3b8',
          alignItems: 'center',
          justifyContent: 'center',
          borderWidth: 1.5,
          borderColor: '#ffffff',
          zIndex: 2,
        }}>
        <Text className="text-[10px] font-semibold text-white">{m[0]?.initials ?? '·'}</Text>
      </View>
      <View
        style={{
          position: 'absolute',
          bottom: 0,
          right: 0,
          width: 24,
          height: 24,
          borderRadius: 12,
          backgroundColor: m[1]?.color ?? '#94a3b8',
          alignItems: 'center',
          justifyContent: 'center',
          borderWidth: 1.5,
          borderColor: '#ffffff',
        }}>
        <Text className="text-[10px] font-semibold text-white">{m[1]?.initials ?? '·'}</Text>
      </View>
    </View>
  );
}

function MessageBubble({
  message,
  showSender,
}: {
  message: Extract<Message, { kind: 'message' }>;
  showSender: boolean;
}) {
  const primary = useThemeColor('primary');
  const primaryFg = useThemeColor('primary-foreground');
  const muted = useThemeColor('muted');
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const ring = useThemeColor('ring');

  return (
    <View
      style={{
        flexDirection: 'row',
        gap: 8,
        marginBottom: 4,
        alignItems: 'flex-end',
        justifyContent: message.mine ? 'flex-end' : 'flex-start',
        paddingHorizontal: 12,
      }}>
      {!message.mine && showSender ? (
        <View
          style={{
            width: 28,
            height: 28,
            borderRadius: 14,
            backgroundColor: message.senderColor,
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <Text className="text-[11px] font-semibold text-white">{message.senderInitials}</Text>
        </View>
      ) : !message.mine ? (
        <View style={{ width: 28 }} />
      ) : null}

      <View style={{ maxWidth: '78%' }}>
        {showSender && !message.mine ? (
          <Text
            variant="small"
            style={{ marginLeft: 12, marginBottom: 2, color: mutedFg, fontWeight: '600' }}>
            {message.senderName}
          </Text>
        ) : null}

        <View
          style={{
            backgroundColor: message.mine ? primary : muted,
            paddingHorizontal: 14,
            paddingVertical: 9,
            borderRadius: 18,
            borderBottomRightRadius: message.mine ? 6 : 18,
            borderBottomLeftRadius: message.mine ? 18 : 6,
          }}>
          <Text style={{ color: message.mine ? primaryFg : fg, lineHeight: 20 }}>
            {message.text}
          </Text>
        </View>

        <View
          style={{
            flexDirection: 'row',
            alignItems: 'center',
            gap: 6,
            marginTop: 4,
            marginHorizontal: 6,
            justifyContent: message.mine ? 'flex-end' : 'flex-start',
          }}>
          {message.reactions?.length
            ? message.reactions.map((r, i) => (
                <View
                  key={i}
                  style={{
                    flexDirection: 'row',
                    alignItems: 'center',
                    gap: 3,
                    paddingHorizontal: 7,
                    paddingVertical: 2,
                    borderRadius: 999,
                    backgroundColor: muted,
                    borderWidth: 1,
                    borderColor: 'rgba(0,0,0,0.06)',
                  }}>
                  <Text className="text-xs">{r.emoji}</Text>
                  <Text className="text-[11px]" style={{ color: mutedFg }}>
                    {r.count}
                  </Text>
                </View>
              ))
            : null}
          <Text variant="small" style={{ color: mutedFg, fontSize: 11 }}>
            {message.timestamp}
          </Text>
          {message.mine && message.status === 'read' ? (
            <CheckCheck size={13} color={ring} />
          ) : message.mine && message.status === 'delivered' ? (
            <CheckCheck size={13} color={mutedFg} />
          ) : message.mine ? (
            <Check size={13} color={mutedFg} />
          ) : null}
        </View>
      </View>
    </View>
  );
}

function SystemBubble({ text, timestamp }: { text: string; timestamp: string }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View style={{ alignItems: 'center', paddingVertical: 8 }}>
      <Text variant="small" style={{ color: mutedFg, fontStyle: 'italic' }}>
        {text} · {timestamp}
      </Text>
    </View>
  );
}

function TypingIndicator({ typing }: { typing: string[] }) {
  const ringColor = useThemeColor('ring');
  return (
    <View
      style={{
        flexDirection: 'row',
        alignItems: 'center',
        gap: 8,
        paddingHorizontal: 16,
        paddingVertical: 6,
      }}>
      <View style={{ flexDirection: 'row', gap: 3 }}>
        <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: ringColor }} />
        <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: ringColor, opacity: 0.7 }} />
        <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: ringColor, opacity: 0.4 }} />
      </View>
      <Text variant="small" style={{ color: ringColor, fontStyle: 'italic' }}>
        {typing.join(', ')} {typing.length > 1 ? 'are' : 'is'} typing
      </Text>
    </View>
  );
}

export default function ConversationScreen() {
  const router = useRouter();
  const { id } = useLocalSearchParams<{ id: string }>();
  const convo = useConversationsStore((s) => s.conversations.find((c) => c.id === id));
  const messages = useConversationsStore((s) => s.messagesByConversation[id] ?? []);
  const deleteConversation = useConversationsStore((s) => s.deleteConversation);
  const [draft, setDraft] = React.useState('');

  const openHeaderMenu = React.useCallback(() => {
    if (!convo) return;
    const isGroup = convo.kind === 'group';
    const options = isGroup
      ? ['Group info', 'Add people', 'Delete chat', 'Cancel']
      : ['Add people to start a group', 'Delete chat', 'Cancel'];
    const cancelButtonIndex = options.length - 1;
    const destructiveButtonIndex = options.length - 2;

    const handle = (i: number) => {
      if (i === cancelButtonIndex) return;
      if (isGroup) {
        if (i === 0) router.push(`/group/${convo.id}/members`);
        else if (i === 1) router.push(`/group/${convo.id}/members`);
        else if (i === 2) {
          Alert.alert(`Delete ${convo.name}?`, "This can't be undone.", [
            { text: 'Cancel', style: 'cancel' },
            {
              text: 'Delete',
              style: 'destructive',
              onPress: () => {
                deleteConversation(convo.id);
                router.back();
              },
            },
          ]);
        }
      } else {
        if (i === 0) router.push(`/group/new?seed=${convo.id}`);
        else if (i === 1) {
          Alert.alert(
            `Delete chat with ${convo.name}?`,
            "Your messages will be removed from your list. They won't be notified.",
            [
              { text: 'Cancel', style: 'cancel' },
              {
                text: 'Delete',
                style: 'destructive',
                onPress: () => {
                  deleteConversation(convo.id);
                  router.back();
                },
              },
            ],
          );
        }
      }
    };

    if (Platform.OS === 'ios') {
      ActionSheetIOS.showActionSheetWithOptions(
        {
          options,
          cancelButtonIndex,
          destructiveButtonIndex,
          title: convo.name,
        },
        handle,
      );
    } else {
      // Android falls back to Alert with the same option set.
      Alert.alert(
        convo.name,
        undefined,
        options.slice(0, -1).map((label, i) => ({
          text: label,
          style: i === destructiveButtonIndex ? 'destructive' : 'default',
          onPress: () => handle(i),
        })),
      );
    }
  }, [convo, router, deleteConversation]);

  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const ring = useThemeColor('ring');
  const muted = useThemeColor('muted');
  const border = useThemeColor('border');
  const bg = useThemeColor('background');

  if (!convo) {
    return (
      <SafeAreaView className="flex-1 items-center justify-center bg-background">
        <Stack.Screen options={{ title: 'Conversation' }} />
        <Text variant="h4">Conversation not found</Text>
        <Pressable onPress={() => router.back()} className="mt-3">
          <Text variant="small" style={{ color: ring }}>Back to messages</Text>
        </Pressable>
      </SafeAreaView>
    );
  }

  const subtitle =
    convo.kind === 'dm'
      ? convo.statusEmoji
        ? `${convo.statusEmoji} ${convo.bio ?? PRESENCE_LABEL[convo.presence].toLowerCase()}`
        : PRESENCE_LABEL[convo.presence]
      : `${convo.members.length + 1} members${convo.voiceRoom ? ` · ${convo.voiceRoom.participants} in voice` : ''}`;

  return (
    <SafeAreaView edges={['bottom']} className="flex-1 bg-background">
      <Stack.Screen
        options={{
          headerShown: true,
          // Opaque background — iOS's default translucent header
          // washed everything out against a bright bg.
          headerStyle: { backgroundColor: bg },
          headerTransparent: false,
          headerShadowVisible: true,
          headerTintColor: fg,
          headerTitle: () => (
            <View style={{ flexDirection: 'row', alignItems: 'center', gap: 10 }}>
              <HeaderAvatar convo={convo} />
              <View>
                <Text style={{ color: fg, fontWeight: '600', fontSize: 16 }}>{convo.name}</Text>
                <Text style={{ color: fg, fontSize: 12, opacity: 0.65 }} numberOfLines={1}>
                  {subtitle}
                </Text>
              </View>
            </View>
          ),
          headerLeft: () => (
            <Pressable
              onPress={() => router.back()}
              hitSlop={10}
              accessibilityLabel="Back"
              style={{ marginLeft: 8 }}>
              <ArrowLeft size={24} color={fg} strokeWidth={2.25} />
            </Pressable>
          ),
          headerRight: () => (
            <View style={{ flexDirection: 'row', alignItems: 'center', gap: 16, marginRight: 12 }}>
              <Pressable hitSlop={6} accessibilityLabel="Voice call">
                <Phone size={22} color={fg} strokeWidth={2.25} />
              </Pressable>
              <Pressable hitSlop={6} accessibilityLabel="Video call">
                <Video size={22} color={fg} strokeWidth={2.25} />
              </Pressable>
              <Pressable hitSlop={6} accessibilityLabel="More" onPress={openHeaderMenu}>
                <MoreVertical size={22} color={fg} strokeWidth={2.25} />
              </Pressable>
            </View>
          ),
        }}
      />

      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={Platform.OS === 'ios' ? 88 : 0}
        style={{ flex: 1 }}>
        {convo.voiceRoom ? (
          <View
            style={{
              marginHorizontal: 12,
              marginTop: 10,
              flexDirection: 'row',
              alignItems: 'center',
              gap: 10,
              paddingHorizontal: 12,
              paddingVertical: 10,
              borderRadius: 12,
              backgroundColor: 'rgba(34, 197, 94, 0.10)',
              borderWidth: 1,
              borderColor: 'rgba(34, 197, 94, 0.35)',
            }}>
            <View
              style={{
                width: 6,
                height: 6,
                borderRadius: 3,
                backgroundColor: '#16a34a',
              }}
            />
            <Text className="flex-1 text-sm font-medium" style={{ color: '#15803d' }}>
              Voice room is live · {convo.voiceRoom.participants} in the room
            </Text>
            <View
              style={{
                paddingHorizontal: 12,
                paddingVertical: 6,
                borderRadius: 999,
                backgroundColor: '#16a34a',
              }}>
              <Text className="text-xs font-semibold text-white">Join</Text>
            </View>
          </View>
        ) : null}

        <ScrollView
          contentContainerStyle={{ paddingTop: 14, paddingBottom: 8 }}
          keyboardDismissMode="interactive"
          keyboardShouldPersistTaps="handled">
          {messages.map((m, idx) => {
            if (m.kind === 'system') {
              return <SystemBubble key={m.id} text={m.text} timestamp={m.timestamp} />;
            }
            // Show sender label/avatar for groups, but only at the
            // start of a streak (different sender or first message).
            const prev = messages[idx - 1];
            const sameSender =
              prev && prev.kind === 'message' && prev.senderName === m.senderName && prev.mine === m.mine;
            const showSender = convo.kind === 'group' && !sameSender;
            return <MessageBubble key={m.id} message={m} showSender={showSender} />;
          })}
          {convo.typing && convo.typing.length > 0 ? (
            <TypingIndicator typing={convo.typing} />
          ) : null}
        </ScrollView>

        <View
          style={{
            flexDirection: 'row',
            alignItems: 'flex-end',
            gap: 8,
            paddingHorizontal: 12,
            paddingTop: 8,
            paddingBottom: 8,
            borderTopWidth: 1,
            borderTopColor: border,
          }}>
          <Pressable
            hitSlop={6}
            accessibilityLabel="Attach"
            style={{
              width: 36,
              height: 36,
              borderRadius: 18,
              backgroundColor: muted,
              alignItems: 'center',
              justifyContent: 'center',
            }}>
            <Plus size={18} color={fg} />
          </Pressable>
          <View
            style={{
              flex: 1,
              flexDirection: 'row',
              alignItems: 'center',
              gap: 6,
              paddingHorizontal: 12,
              paddingVertical: 6,
              borderRadius: 22,
              backgroundColor: muted,
              minHeight: 40,
            }}>
            <TextInput
              value={draft}
              onChangeText={setDraft}
              placeholder="Message"
              placeholderTextColor={mutedFg}
              multiline
              style={{
                flex: 1,
                color: fg,
                fontSize: 15,
                paddingTop: Platform.OS === 'ios' ? 4 : 0,
                maxHeight: 120,
              }}
            />
            <Pressable hitSlop={6} accessibilityLabel="Emoji">
              <Smile size={20} color={mutedFg} />
            </Pressable>
          </View>
          <Pressable
            disabled={draft.trim().length === 0}
            onPress={() => setDraft('')}
            accessibilityLabel="Send message"
            style={{
              width: 40,
              height: 40,
              borderRadius: 20,
              backgroundColor: draft.trim().length === 0 ? muted : ring,
              alignItems: 'center',
              justifyContent: 'center',
            }}>
            <Send size={18} color={draft.trim().length === 0 ? mutedFg : '#ffffff'} />
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
