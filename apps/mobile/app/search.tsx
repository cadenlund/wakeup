// Phase 5.5 — global /search modal. Per §5.1:
//
//   `search` | global search modal: users + conversations +
//   messages, debounced 200ms. Triggered by a header search icon
//   on the conversations tab.
//
// Three sections render in a single FlashList via the same
// flat-array-with-discriminator trick the friends tab uses:
//
//   - Users: tap → ensure-or-create a direct conversation, route
//     to the resulting thread (matches the Phase 5.3 friend-tap
//     pattern).
//   - Conversations: tap → /conversations/{id}.
//   - Messages: just count + a snippet for now. Full render with
//     "jump to this message in its thread" lands once Phase 6's
//     real thread surface exists; trying to deep-link into a
//     stub thread would teach users a route that's about to
//     change.
//
// Modal route — back / Cancel pops via canGoBack with a chats-tab
// fallback, mirroring conversations/new's deep-link handling.
import { Stack, useFocusEffect, useRouter } from 'expo-router';
import { ConciergeBell, MessageCircle, Search, Users as UsersIcon, X } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Pressable, View } from 'react-native';

import { ConversationRow } from '@/components/conversation-row';
import { FriendRow } from '@/components/friend-row';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1Search } from '@/lib/api/hooks/search/search';
import type {
  InternalHandlerHttpSearchConversationRow,
  InternalHandlerHttpSearchMessageRow,
  InternalHandlerHttpSearchResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useEnsureDirectConversation } from '@/lib/api/use-ensure-direct-conversation';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

const DEBOUNCE_MS = 200;
const MIN_CHARS = 2;

type Row =
  | { kind: 'header'; key: string; title: string; count: number }
  | { kind: 'user'; key: string; user: InternalHandlerHttpUserResponse }
  | { kind: 'conversation'; key: string; conversation: InternalHandlerHttpSearchConversationRow }
  | { kind: 'message'; key: string; message: InternalHandlerHttpSearchMessageRow };

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}

export default function SearchModalScreen() {
  const router = useRouter();
  const [rawQuery, setRawQuery] = React.useState('');
  const debouncedQuery = useDebouncedValue(rawQuery.trim(), DEBOUNCE_MS);
  const enabled = debouncedQuery.length >= MIN_CHARS;

  const searchQ = useGetV1Search(
    { q: debouncedQuery, types: 'users,conversations,messages' },
    { query: { enabled, staleTime: 30_000 } }
  );
  const data = searchQ.data as InternalHandlerHttpSearchResponse | undefined;

  const ensureDM = useEnsureDirectConversation();
  const [openingFor, setOpeningFor] = React.useState<string | null>(null);

  const goCancel = React.useCallback(() => {
    if (router.canGoBack()) router.back();
    else router.replace('/');
  }, [router]);

  // Reset query on dismiss so re-opening the modal starts clean.
  useFocusEffect(
    React.useCallback(() => {
      return () => setRawQuery('');
    }, [])
  );

  const onTapUser = React.useCallback(
    async (user: InternalHandlerHttpUserResponse) => {
      const userId = user.id;
      if (!userId || openingFor) return;
      setOpeningFor(userId);
      try {
        const { conversationId } = await ensureDM.ensure(userId);
        router.replace(`/conversations/${conversationId}`);
      } catch (err) {
        const msg =
          err instanceof APIError && err.message
            ? err.message
            : "Couldn't open the conversation right now.";
        toast.error(msg);
      } finally {
        setOpeningFor(null);
      }
    },
    [ensureDM, router, openingFor]
  );

  const rows = React.useMemo<Row[]>(() => buildRows(data), [data]);

  return (
    <>
      <Stack.Screen options={{ headerShown: false }} />
      <View className="flex-1 bg-background">
        <ModalHeader value={rawQuery} onChange={setRawQuery} onCancel={goCancel} />

        {!enabled ? (
          <SearchHint />
        ) : searchQ.isFetching && rows.length === 0 ? (
          <SearchLoading />
        ) : searchQ.isError && rows.length === 0 ? (
          <SearchError onRetry={() => searchQ.refetch()} />
        ) : rows.length === 0 ? (
          <SearchNoResults />
        ) : (
          <List
            data={rows}
            keyExtractor={(item) => item.key}
            renderItem={({ item }) => (
              <RenderedRow row={item} onTapUser={onTapUser} openingForUserId={openingFor} />
            )}
          />
        )}
      </View>
    </>
  );
}

function buildRows(data: InternalHandlerHttpSearchResponse | undefined): Row[] {
  if (!data) return [];
  const out: Row[] = [];

  const users = data.users ?? [];
  if (users.length > 0) {
    out.push({ kind: 'header', key: 'h:users', title: 'People', count: users.length });
    users.forEach((u, i) => {
      out.push({ kind: 'user', key: `user:${u.id ?? `idx-${i}`}`, user: u });
    });
  }

  const conversations = data.conversations ?? [];
  if (conversations.length > 0) {
    out.push({
      kind: 'header',
      key: 'h:conversations',
      title: 'Chats',
      count: conversations.length,
    });
    conversations.forEach((c, i) => {
      out.push({
        kind: 'conversation',
        key: `conv:${c.id ?? `idx-${i}`}`,
        conversation: c,
      });
    });
  }

  const messages = data.messages ?? [];
  if (messages.length > 0) {
    out.push({
      kind: 'header',
      key: 'h:messages',
      title: 'Messages',
      count: messages.length,
    });
    messages.forEach((m, i) => {
      out.push({
        kind: 'message',
        key: `msg:${m.id ?? `idx-${i}`}`,
        message: m,
      });
    });
  }

  return out;
}

function RenderedRow({
  row,
  onTapUser,
  openingForUserId,
}: {
  row: Row;
  onTapUser: (u: InternalHandlerHttpUserResponse) => void;
  openingForUserId: string | null;
}) {
  const router = useRouter();
  switch (row.kind) {
    case 'header':
      return <SectionHeader title={row.title} count={row.count} />;
    case 'user': {
      const u = row.user;
      const opening = u.id != null && u.id === openingForUserId;
      return (
        <FriendRow
          displayName={u.display_name}
          username={u.username}
          avatarUrl={u.avatar_url}
          hidePresence
          onPress={!opening && u.id ? () => onTapUser(u) : undefined}
          trailing={
            opening ? (
              <Text variant="muted" className="text-xs">
                Opening…
              </Text>
            ) : undefined
          }
        />
      );
    }
    case 'conversation': {
      const c = row.conversation;
      return (
        <ConversationRow
          title={c.name?.trim() || 'Conversation'}
          avatarUrl={c.avatar_url}
          fallbackInitial={c.name ?? 'C'}
          lastMessageAt={c.last_message_at}
          onPress={() => {
            if (c.id) router.push(`/conversations/${c.id}`);
          }}
          testID={`search-conversation-${c.id}`}
        />
      );
    }
    case 'message': {
      const m = row.message;
      return <MessageHitRow message={m} />;
    }
  }
}

function MessageHitRow({ message }: { message: InternalHandlerHttpSearchMessageRow }) {
  const router = useRouter();
  const mutedFg = useThemeColor('muted-foreground');
  // Lightweight render until Phase 6 ships jump-to-message inside
  // the real thread surface. Tap routes to the conversation; we
  // can't pin the user to the exact message yet because the thread
  // is still a stub.
  return (
    <Pressable
      onPress={() => {
        if (message.conversation_id) {
          router.push(`/conversations/${message.conversation_id}`);
        }
      }}
      accessibilityRole="button"
      accessibilityLabel="Open conversation"
      testID={`search-message-${message.id}`}
      className="flex-row items-start gap-3 px-4 py-3 active:bg-muted">
      <View className="mt-1">
        <MessageCircle size={20} color={mutedFg} />
      </View>
      <View className="min-w-0 flex-1">
        <Text numberOfLines={2} className="text-sm">
          {message.body?.trim() || '(empty message)'}
        </Text>
        <Text variant="muted" className="text-xs">
          Tap to open the conversation
        </Text>
      </View>
    </Pressable>
  );
}

function SectionHeader({ title, count }: { title: string; count: number }) {
  return (
    <View className="flex-row items-baseline justify-between border-b border-border bg-muted/30 px-4 py-2">
      <Text variant="muted" className="text-xs font-semibold uppercase tracking-wider">
        {title}
      </Text>
      <Text variant="muted" className="text-xs">
        {count}
      </Text>
    </View>
  );
}

function ModalHeader({
  value,
  onChange,
  onCancel,
}: {
  value: string;
  onChange: (v: string) => void;
  onCancel: () => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-row items-center gap-3 border-b border-border bg-card px-3 py-3">
      <View className="relative flex-1">
        <View className="absolute bottom-0 left-3 top-0 z-10 justify-center">
          <Search size={16} color={mutedFg} />
        </View>
        <Input
          value={value}
          onChangeText={onChange}
          placeholder="Search people, chats, messages"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="off"
          autoFocus
          returnKeyType="search"
          testID="search-input"
          accessibilityLabel="Search"
          className="pl-9 pr-9"
        />
        {value.length > 0 ? (
          <Pressable
            onPress={() => onChange('')}
            accessibilityRole="button"
            accessibilityLabel="Clear search"
            testID="search-clear"
            hitSlop={8}
            className="absolute bottom-0 right-3 top-0 z-10 justify-center">
            <X size={16} color={mutedFg} />
          </Pressable>
        ) : null}
      </View>
      <Pressable
        onPress={onCancel}
        accessibilityRole="button"
        accessibilityLabel="Cancel"
        testID="search-cancel"
        hitSlop={8}>
        <Text style={{ color: mutedFg }} className="text-base">
          Cancel
        </Text>
      </Pressable>
    </View>
  );
}

function SearchHint() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <EmptyState
        icon={<Search size={40} color={mutedFg} />}
        title="Search"
        subtitle="Type at least 2 characters to find people, chats, and messages."
      />
    </View>
  );
}

function SearchLoading() {
  const fg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center py-12">
      <ActivityIndicator color={fg} />
    </View>
  );
}

function SearchNoResults() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <EmptyState
        icon={<UsersIcon size={40} color={mutedFg} />}
        title="No matches"
        subtitle="Try a different name, username, or message."
      />
    </View>
  );
}

function SearchError({ onRetry }: { onRetry: () => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <EmptyState
        icon={<ConciergeBell size={40} color={mutedFg} />}
        title="Search couldn't reach the server"
        subtitle="Check your connection and try again."
        cta={{ label: 'Retry', onPress: onRetry }}
      />
    </View>
  );
}
