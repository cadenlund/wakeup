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
import { Pressable, RefreshControl, View } from 'react-native';
import { useRouter } from 'expo-router';

import { ConversationRow } from '@/components/conversation-row';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { useGetV1Conversations } from '@/lib/api/hooks/conversations/conversations';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpPresenceListResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
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
  const visible = React.useMemo(
    () => filterConversations(sorted, me?.id, query),
    [sorted, me, query]
  );
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

  const isInitialLoad = conversationsQ.isLoading && !conversationsQ.data;

  return (
    <View className="flex-1 bg-background">
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
            />
          )}
        />
      )}

      <ComposeFab onPress={goCompose} />
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
  return (
    <View className="px-6 py-12">
      <Text variant="muted" className="text-center text-sm">
        No conversations match that filter.
      </Text>
    </View>
  );
}

function filterConversations(
  rows: Conversation[],
  myUserId: string | undefined,
  rawQuery: string
): Conversation[] {
  const term = rawQuery.trim().toLowerCase();
  if (!term) return rows;
  return rows.filter((c) => {
    if (c.type === 'direct') {
      const others = (c.members ?? []).filter((m) => m.user?.id && m.user.id !== myUserId);
      const other = others[0]?.user;
      const dn = other?.display_name?.toLowerCase() ?? '';
      const un = other?.username?.toLowerCase() ?? '';
      return dn.includes(term) || un.includes(term);
    }
    // group: match on group name OR any member's display/username
    const named = c.name?.toLowerCase() ?? '';
    if (named.includes(term)) return true;
    return (c.members ?? []).some((m) => {
      const dn = m.user?.display_name?.toLowerCase() ?? '';
      const un = m.user?.username?.toLowerCase() ?? '';
      return dn.includes(term) || un.includes(term);
    });
  });
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
}: {
  conversation: Conversation;
  myUserId: string | undefined;
  presenceByUser: Map<string, string>;
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
      testID={`conversation-${conversation.id}`}
      onPress={() => {
        if (conversation.id) router.push(`/conversations/${conversation.id}`);
      }}
    />
  );
}

type ConversationDisplay = {
  title: string;
  subtitle?: string;
  avatarUrl?: string | null;
  fallbackInitial?: string;
  // Two member avatars to render in a stacked cluster when the
  // group has no avatar_url. Each carries its own presence so the
  // cluster can show two dots. Empty / undefined for direct convos.
  stackedMembers?: {
    avatarUrl?: string | null;
    fallbackName?: string | null;
    presence?: string | null;
  }[];
  // Presence to overlay on the (single) avatar. Set for direct DMs
  // where there's a clear "the other person"; unset for groups
  // where per-member dots ride on stackedMembers instead.
  presence?: string | null;
};

function conversationDisplay(
  c: Conversation,
  myUserId: string | undefined,
  presenceByUser: Map<string, string>
): ConversationDisplay {
  if (c.type === 'direct') {
    // For a 1:1 conversation, we want the *other* member. Server may
    // include the caller as a member; filter them out so a self-DM
    // (rare; admin tooling) at least falls back to the same row.
    const others = (c.members ?? []).filter((m) => m.user?.id && m.user.id !== myUserId);
    const other = others[0]?.user ?? c.members?.[0]?.user;
    const title = other?.display_name?.trim() || other?.username?.trim() || 'Direct message';
    return {
      title,
      avatarUrl: other?.avatar_url,
      fallbackInitial: title,
      presence: other?.id ? (presenceByUser.get(other.id) ?? null) : null,
    };
  }
  // group
  const others = (c.members ?? []).filter((m) => m.user?.id && m.user.id !== myUserId);
  const memberCount = (c.members ?? []).length;
  const stackedMembers = others.slice(0, 2).map((m) => ({
    avatarUrl: m.user?.avatar_url,
    fallbackName: m.user?.display_name ?? m.user?.username ?? null,
    presence: m.user?.id ? (presenceByUser.get(m.user.id) ?? null) : null,
  }));

  const named = c.name?.trim();
  if (named) {
    // Named group → subtitle is "N members" so the avatar / name +
    // count read as a complete identity even before any messages.
    const subtitle = memberCount > 0 ? membersLabel(memberCount) : undefined;
    return {
      title: named,
      subtitle,
      avatarUrl: c.avatar_url,
      fallbackInitial: named,
      stackedMembers,
    };
  }
  // Unnamed group — fall back to a comma-joined preview of up to
  // three member names so the row isn't empty.
  const previewNames = others
    .map((m) => m.user?.display_name?.trim() || m.user?.username?.trim())
    .filter((s): s is string => !!s);
  const previewShown = previewNames.slice(0, 3).join(', ');
  const remaining = previewNames.length - 3;
  // "Caden, Test, Alice +2" when there's overflow; bare list when
  // it all fits.
  const subtitle = previewShown
    ? remaining > 0
      ? `${previewShown} +${remaining}`
      : previewShown
    : undefined;
  return {
    title: previewShown || 'Group',
    subtitle,
    avatarUrl: c.avatar_url,
    fallbackInitial: previewShown || 'G',
    stackedMembers,
  };
}

function membersLabel(n: number): string {
  return n === 1 ? '1 member' : `${n} members`;
}

function isCurrentlyMuted(mutedUntil: string | null | undefined): boolean {
  if (!mutedUntil) return false;
  const t = Date.parse(mutedUntil);
  if (Number.isNaN(t)) return false;
  return t > Date.now();
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
