// Phase 5.1 — Chats tab. Conversations list, sorted pinned-first
// then by last_message_at DESC. The new-conversation FAB at the
// bottom-right opens a friend-picker sheet; tapping a friend POSTs
// a direct conversation and navigates into the thread (stub at
// /conversations/[id] until 5.2 lands the real thread surface).
//
// Render rules (per the conversation response shape):
//   - direct convos render the OTHER member's display_name +
//     avatar.
//   - group convos render conversation.name + conversation.avatar
//     (with a comma-joined preview of up to three member names as
//     a fallback subtitle when the name is missing).
//   - pinned_at / muted_until on each row are the CALLER's flags
//     — the row mirrors them with a Pin / BellOff icon.
//
// Pull-to-refresh wraps everything per §5.4. Pin/mute long-press
// menus + last-message preview land in 5.6 / 5.5 respectively. The
// new-conversation flow lives at /conversations/new (Phase 5.2).
import { MessageCircle, Plus, Search, X } from 'lucide-react-native';
import * as React from 'react';
import { Platform, Pressable, RefreshControl, View } from 'react-native';
import { useRouter } from 'expo-router';

import { ConversationActionMenu } from '@/components/conversation-action-menu';
import { ConversationRow } from '@/components/conversation-row';
import { MuteSheet } from '@/components/mute-sheet';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import {
  conversationDisplay,
  filterConversations,
  isCurrentlyMuted,
} from '@/lib/conversation-display';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { useGetV1Conversations } from '@/lib/api/hooks/conversations/conversations';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import { haptics } from '@/lib/haptics';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpPresenceListResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useConversationPinMute } from '@/lib/use-conversation-pin-mute';
import { EmptyState } from '@/components/ui/empty-state';

type Conversation = InternalHandlerHttpConversationResponse;

export default function ChatsScreen() {
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;

  const conversationsQ = useGetV1Conversations({ limit: 100 }, { query: { staleTime: 30_000 } });
  const data = conversationsQ.data as InternalHandlerHttpConversationListResponse | undefined;

  // Presence is keyed by user_id. Cache it once per render so each
  // row's display can look up O(1) without re-deriving the map per
  // row. Stale 15s tracks the friends-tab choice.
  const presenceQ = useGetV1PresenceFriends({ query: { staleTime: 15_000 } });
  const presenceData = presenceQ.data as InternalHandlerHttpPresenceListResponse | undefined;
  const presenceByUser = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const p of presenceData?.data ?? []) {
      if (p.user_id && p.status) m.set(p.user_id, p.status);
    }
    return m;
  }, [presenceData]);

  const sorted = React.useMemo(() => sortConversations(data?.data ?? []), [data]);

  // Inline filter input. Matches the friends-tab shape so the two
  // tabs read the same. Filter narrows the existing list by name —
  // global search (across users/chats/messages) lives behind the
  // header icon, this is a local filter only.
  const [query, setQuery] = React.useState('');
  const visible = React.useMemo(() => filterConversations(sorted, query), [sorted, query]);
  const filterActive = query.trim().length > 0;

  // Pull-to-refresh: refetch the list. Local refreshing flag is
  // independent of conversationsQ.isFetching so passive background
  // refetches (focus, mount) don't surface the spinner.
  const [refreshing, setRefreshing] = React.useState(false);
  const onRefresh = React.useCallback(async () => {
    setRefreshing(true);
    try {
      await conversationsQ.refetch();
    } finally {
      setRefreshing(false);
    }
  }, [conversationsQ]);

  const router = useRouter();
  const goCompose = React.useCallback(() => router.push('/conversations/new'), [router]);

  // Long-press menu state machine: 'menu' shows pin + mute
  // entry; 'mute' is the duration sheet. The active conversation
  // ID stays in state across the transition so the mute sheet
  // resolves against the same row even after the menu closes.
  // null = nothing open.
  const [activeAction, setActiveAction] = React.useState<{
    id: string;
    title: string;
    isPinned: boolean;
    isMuted: boolean;
    screen: 'menu' | 'mute';
  } | null>(null);
  const closeMenu = React.useCallback(() => setActiveAction(null), []);
  const openMuteSheet = React.useCallback(
    () => setActiveAction((s) => (s ? { ...s, screen: 'mute' } : s)),
    []
  );

  const { togglePin, setMute, unmute } = useConversationPinMute();

  const isInitialLoad = conversationsQ.isLoading && !conversationsQ.data;

  return (
    <View className="flex-1 bg-background">
      {Platform.OS === 'web' ? <ChatsWebHeader onCompose={goCompose} /> : null}
      <ChatsSearchBar value={query} onChange={setQuery} />
      {isInitialLoad ? (
        <ChatsLoading />
      ) : sorted.length === 0 ? (
        <PullableEmpty refreshing={refreshing} onRefresh={onRefresh} />
      ) : visible.length === 0 ? (
        <NoFilterMatches />
      ) : (
        <List
          data={visible}
          keyExtractor={(item, i) => item.id ?? `idx-${i}`}
          refreshControl={
            // Skip pull-to-refresh while a filter is active — refetching
            // would replace the visible subset with a fresh full list and
            // re-filter, which feels right but the pull gesture conflicts
            // with scrolling a short filtered result set.
            filterActive ? undefined : (
              <RefreshControl refreshing={refreshing} onRefresh={onRefresh} />
            )
          }
          renderItem={({ item }) => (
            <RenderedConversationRow
              conversation={item}
              myUserId={me?.id}
              presenceByUser={presenceByUser}
              onMorePress={(row) => {
                haptics.tap();
                setActiveAction({
                  id: row.id,
                  title: row.title,
                  isPinned: row.isPinned,
                  isMuted: row.isMuted,
                  screen: 'menu',
                });
              }}
            />
          )}
        />
      )}

      {/* The floating compose FAB is the right thumb-zone affordance
          on touch but on a desktop browser it's a stranded floating
          button. Web gets the top-bar "New chat" button instead
          (rendered above) — this stays native-only. */}
      {Platform.OS !== 'web' ? <ComposeFab onPress={goCompose} /> : null}

      <ConversationActionMenu
        visible={activeAction?.screen === 'menu'}
        title={activeAction?.title ?? ''}
        isPinned={activeAction?.isPinned ?? false}
        isMuted={activeAction?.isMuted ?? false}
        onTogglePin={() => {
          if (!activeAction) return;
          togglePin(activeAction.id, activeAction.isPinned);
          closeMenu();
        }}
        onMutePress={openMuteSheet}
        onUnmute={() => {
          if (!activeAction) return;
          unmute(activeAction.id);
          closeMenu();
        }}
        onClose={closeMenu}
        testID="conversation-action-menu"
      />
      <MuteSheet
        visible={activeAction?.screen === 'mute'}
        isMuted={activeAction?.isMuted ?? false}
        onPickUntil={(until) => {
          if (!activeAction) return;
          setMute(activeAction.id, until);
          closeMenu();
        }}
        onUnmute={() => {
          if (!activeAction) return;
          unmute(activeAction.id);
          closeMenu();
        }}
        onClose={closeMenu}
        testID="mute-sheet"
      />
    </View>
  );
}

// Web-only header bar above the filter input. Holds the page
// title on the left and a primary "New chat" button on the right
// — replaces the floating compose FAB which feels marooned on a
// desktop browser. The button text is "New chat" rather than
// just "+" so the affordance reads at a glance on a wide screen.
function ChatsWebHeader({ onCompose }: { onCompose: () => void }) {
  const fg = useThemeColor('primary-foreground');
  return (
    <View className="flex-row items-center justify-between gap-3 border-b border-border px-4 py-3">
      <Text className="text-lg font-semibold">Chats</Text>
      <Pressable
        onPress={onCompose}
        accessibilityRole="button"
        accessibilityLabel="New conversation"
        testID="conversation-compose-web"
        className="flex-row items-center gap-2 rounded-full bg-primary px-4 py-2 active:opacity-80">
        <Plus size={16} color={fg} />
        <Text style={{ color: fg }} className="text-sm font-semibold">
          New chat
        </Text>
      </Pressable>
    </View>
  );
}

function ChatsSearchBar({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="border-b border-border bg-background px-4 pb-3 pt-3">
      <View className="relative">
        <View className="absolute bottom-0 left-3 top-0 z-10 justify-center">
          <Search size={16} color={mutedFg} />
        </View>
        <Input
          value={value}
          onChangeText={onChange}
          placeholder="Filter your chats"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="off"
          returnKeyType="search"
          testID="chats-filter-input"
          accessibilityLabel="Filter your chats"
          className="pl-9 pr-9"
        />
        {value.length > 0 ? (
          <Pressable
            onPress={() => onChange('')}
            accessibilityRole="button"
            accessibilityLabel="Clear filter"
            testID="chats-filter-clear"
            hitSlop={8}
            className="absolute bottom-0 right-3 top-0 z-10 justify-center">
            <X size={16} color={mutedFg} />
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

function NoFilterMatches() {
  // Use the shared EmptyState primitive so this state reads with
  // the same shape every other "no results" surface in the app
  // (friends-tab no-matches, search modal no-matches, etc.). §4.9
  // forbids ad-hoc Text+View pairs for empty states.
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <EmptyState
      icon={<Search size={40} color={mutedFg} />}
      title="No matches"
      subtitle="Try a different name or member."
    />
  );
}

// Sort: pinned first (most recent pin first), then by last_message_at
// DESC, then created_at DESC as a tiebreaker so freshly-created
// conversations with no messages still land at the top.
function sortConversations(rows: Conversation[]): Conversation[] {
  return rows.slice().sort((a, b) => {
    const aPin = a.pinned_at ? Date.parse(a.pinned_at) : 0;
    const bPin = b.pinned_at ? Date.parse(b.pinned_at) : 0;
    if (aPin !== bPin) return bPin - aPin;
    const aMsg = a.last_message_at ? Date.parse(a.last_message_at) : 0;
    const bMsg = b.last_message_at ? Date.parse(b.last_message_at) : 0;
    if (aMsg !== bMsg) return bMsg - aMsg;
    const aCreated = a.created_at ? Date.parse(a.created_at) : 0;
    const bCreated = b.created_at ? Date.parse(b.created_at) : 0;
    return bCreated - aCreated;
  });
}

function RenderedConversationRow({
  conversation,
  myUserId,
  presenceByUser,
  onMorePress,
}: {
  conversation: Conversation;
  myUserId: string | undefined;
  presenceByUser: Map<string, string>;
  onMorePress?: (row: { id: string; title: string; isPinned: boolean; isMuted: boolean }) => void;
}) {
  const router = useRouter();
  const display = conversationDisplay(conversation, myUserId, presenceByUser);
  const isMuted = isCurrentlyMuted(conversation.muted_until);
  const isPinned = !!conversation.pinned_at;
  return (
    <ConversationRow
      title={display.title}
      subtitle={display.subtitle}
      avatarUrl={display.avatarUrl}
      fallbackInitial={display.fallbackInitial}
      stackedMembers={display.stackedMembers}
      presence={display.presence}
      lastMessageAt={conversation.last_message_at}
      isMuted={isMuted}
      isPinned={isPinned}
      mutedUntil={conversation.muted_until}
      testID={`conversation-${conversation.id}`}
      onPress={() => {
        if (conversation.id) router.push(`/conversations/${conversation.id}`);
      }}
      onMorePress={
        conversation.id && onMorePress
          ? () =>
              onMorePress({
                id: conversation.id!,
                title: display.title,
                isPinned,
                isMuted,
              })
          : undefined
      }
    />
  );
}

function ComposeFab({ onPress }: { onPress: () => void }) {
  const fg = useThemeColor('primary-foreground');
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel="New conversation"
      testID="conversation-compose"
      className="absolute bottom-6 right-6 h-14 w-14 items-center justify-center rounded-full bg-primary shadow-lg shadow-black/30 active:opacity-80">
      <Plus size={26} color={fg} />
    </Pressable>
  );
}

function ChatsLoading() {
  return <View className="flex-1 bg-background" />;
}

function PullableEmpty({ refreshing, onRefresh }: { refreshing: boolean; onRefresh: () => void }) {
  type EmptyItem = { kind: 'empty-screen' };
  const data: EmptyItem[] = [{ kind: 'empty-screen' }];
  return (
    <List
      data={data}
      keyExtractor={(_, i) => `empty-${i}`}
      renderItem={() => <ChatsAllEmpty />}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} />}
    />
  );
}

function ChatsAllEmpty() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <EmptyState
        icon={<MessageCircle size={40} color={mutedFg} />}
        title="No conversations yet"
        subtitle="Tap the + button to start one with a friend."
      />
    </View>
  );
}
