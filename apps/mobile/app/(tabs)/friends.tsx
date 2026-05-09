// Phase 4.2 + 4.3 — Friends tab. Three sections rendered through a
// single FlashList:
//   1. Accepted Friends — keyset-paginated by `accepted_at DESC`,
//      enriched with presence so each row shows a status dot.
//   2. Incoming Requests — pending friend requests addressed to me.
//   3. Outgoing Requests — pending requests I've sent.
//
// Section dividers are header items in the same flat list rather
// than separate <List>s — keeps recycling working across the whole
// screen and means a single scroll position holds all three lists.
//
// The search bar at the top swaps the sections out for live user
// search results (debounced 250ms; min 2 chars per the backend's
// validator). Each result row carries an inline status — "Friend"
// / "Pending" / "Accept" — derived from the same friends-list and
// requests queries, so we don't make a third per-user lookup just
// to render the right badge. Tap "Add" to fire usePostV1FriendsRequests;
// the row flips to "Sent" optimistically and the outgoing-requests
// query is invalidated so the section list updates next time the
// search is cleared.
//
// Actions on existing requests (accept/decline/unfriend/block)
// land in 4.4; pull-to-refresh in 4.5.
import { Search, Users, X } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Pressable, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { FriendRow } from '@/components/friend-row';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1FriendsRequestsQueryKey,
  useGetV1Friends,
  useGetV1FriendsRequests,
  usePostV1FriendsRequests,
} from '@/lib/api/hooks/friends/friends';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import { useGetV1Search } from '@/lib/api/hooks/search/search';
import type {
  InternalHandlerHttpFriendListResponse,
  InternalHandlerHttpFriendRequestsResponse,
  InternalHandlerHttpFriendshipResponse,
  InternalHandlerHttpPresenceListResponse,
  InternalHandlerHttpSearchResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

type Friendship = InternalHandlerHttpFriendshipResponse;
type UserRow = InternalHandlerHttpUserResponse;

// Discriminated row union — FlashList's renderItem switches on `kind`.
type Row =
  | { kind: 'header'; key: string; title: string; count: number }
  | { kind: 'friend'; key: string; friendship: Friendship; presence?: string }
  | { kind: 'request'; key: string; friendship: Friendship; direction: 'incoming' | 'outgoing' }
  | { kind: 'empty'; key: string; subtitle: string };

// Search-mode row variant — backend returns search hits in a flat
// list, no headers.
type SearchRow = {
  user: UserRow;
  // 'friend' | 'incoming' | 'outgoing' | 'self' | 'sent' (optimistic)
  // | undefined (free to add).
  relation?: 'friend' | 'incoming' | 'outgoing' | 'self' | 'sent';
  pending?: boolean;
};

const SEARCH_DEBOUNCE_MS = 250;
const SEARCH_MIN_CHARS = 2;

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}

export default function FriendsScreen() {
  const friendsQ = useGetV1Friends({ limit: 100 }, { query: { staleTime: 30_000 } });
  const requestsQ = useGetV1FriendsRequests({ query: { staleTime: 30_000 } });
  const presenceQ = useGetV1PresenceFriends({ query: { staleTime: 15_000 } });
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });

  // apiFetch returns the unwrapped JSON body; orval types the response
  // as the {data, status, headers} envelope. Cast to the runtime shape
  // (same pattern as auth-gate.tsx).
  const friendsData = friendsQ.data as InternalHandlerHttpFriendListResponse | undefined;
  const requestsData = requestsQ.data as InternalHandlerHttpFriendRequestsResponse | undefined;
  const presenceData = presenceQ.data as InternalHandlerHttpPresenceListResponse | undefined;
  const me = meQ.data as { id?: string } | undefined;

  // Search state.
  const [rawQuery, setRawQuery] = React.useState('');
  const debouncedQuery = useDebouncedValue(rawQuery.trim(), SEARCH_DEBOUNCE_MS);
  const isSearchMode = rawQuery.trim().length >= SEARCH_MIN_CHARS;
  const searchEnabled = debouncedQuery.length >= SEARCH_MIN_CHARS;

  const searchQ = useGetV1Search(
    { q: debouncedQuery, types: 'users' },
    { query: { enabled: searchEnabled, staleTime: 30_000 } }
  );
  const searchData = searchQ.data as InternalHandlerHttpSearchResponse | undefined;

  const presenceByUser = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const p of presenceData?.data ?? []) {
      if (p.user_id && p.status) m.set(p.user_id, p.status);
    }
    return m;
  }, [presenceData]);

  // Build a lookup of existing relationships so search-mode rows can
  // render the right badge without an extra per-user request.
  const relationByUserId = React.useMemo(() => {
    const m = new Map<string, 'friend' | 'incoming' | 'outgoing'>();
    for (const f of friendsData?.data ?? []) {
      if (f.user?.id) m.set(f.user.id, 'friend');
    }
    for (const f of requestsData?.incoming ?? []) {
      if (f.user?.id) m.set(f.user.id, 'incoming');
    }
    for (const f of requestsData?.outgoing ?? []) {
      if (f.user?.id) m.set(f.user.id, 'outgoing');
    }
    return m;
  }, [friendsData, requestsData]);

  // Optimistic local state for "just-sent" requests so the row flips
  // immediately on Add tap. Cleared when the user clears the query.
  const [optimisticSent, setOptimisticSent] = React.useState<Set<string>>(new Set());
  const [pendingSend, setPendingSend] = React.useState<Set<string>>(new Set());

  // Keep optimistic state pinned to the current search session — once
  // they leave search mode, drop it so the next session starts clean.
  React.useEffect(() => {
    if (!isSearchMode) {
      setOptimisticSent(new Set());
    }
  }, [isSearchMode]);

  const qc = useQueryClient();
  const sendRequest = usePostV1FriendsRequests();

  const onAddFriend = React.useCallback(
    async (user: UserRow) => {
      if (!user.id || !user.username) return;
      setPendingSend((prev) => {
        const next = new Set(prev);
        next.add(user.id!);
        return next;
      });
      try {
        await sendRequest.mutateAsync({ data: { username: user.username } });
        setOptimisticSent((prev) => {
          const next = new Set(prev);
          next.add(user.id!);
          return next;
        });
        await qc.invalidateQueries({ queryKey: getGetV1FriendsRequestsQueryKey() });
        toast.success('Request sent', `Waiting on @${user.username}`);
      } catch (err) {
        const msg =
          err instanceof APIError && err.message
            ? err.message
            : "Couldn't send the request — try again in a sec.";
        toast.error(msg);
      } finally {
        setPendingSend((prev) => {
          const next = new Set(prev);
          next.delete(user.id!);
          return next;
        });
      }
    },
    [sendRequest, qc]
  );

  const sectionRows = React.useMemo<Row[]>(() => {
    const friends = friendsData?.data ?? [];
    const incoming = requestsData?.incoming ?? [];
    const outgoing = requestsData?.outgoing ?? [];

    const out: Row[] = [];

    if (incoming.length > 0) {
      out.push({
        kind: 'header',
        key: 'h:incoming',
        title: 'Incoming requests',
        count: incoming.length,
      });
      incoming.forEach((f, i) => {
        out.push({
          kind: 'request',
          key: `req:in:${f.id ?? f.user?.id ?? `idx-${i}`}`,
          friendship: f,
          direction: 'incoming',
        });
      });
    }

    if (outgoing.length > 0) {
      out.push({
        kind: 'header',
        key: 'h:outgoing',
        title: 'Sent requests',
        count: outgoing.length,
      });
      outgoing.forEach((f, i) => {
        out.push({
          kind: 'request',
          key: `req:out:${f.id ?? f.user?.id ?? `idx-${i}`}`,
          friendship: f,
          direction: 'outgoing',
        });
      });
    }

    out.push({
      kind: 'header',
      key: 'h:friends',
      title: 'Friends',
      count: friends.length,
    });
    if (friends.length === 0) {
      out.push({
        kind: 'empty',
        key: 'empty:friends',
        subtitle: 'Search above to find someone to add.',
      });
    } else {
      friends.forEach((f, i) => {
        out.push({
          kind: 'friend',
          key: `friend:${f.id ?? f.user?.id ?? `idx-${i}`}`,
          friendship: f,
          presence: f.user?.id ? presenceByUser.get(f.user.id) : undefined,
        });
      });
    }

    return out;
  }, [friendsData, requestsData, presenceByUser]);

  const searchRows = React.useMemo<SearchRow[]>(() => {
    const users = searchData?.users ?? [];
    return users
      .filter((u) => u.id) // backend guarantees this; defensive
      .map<SearchRow>((u) => {
        if (me?.id && u.id === me.id) {
          return { user: u, relation: 'self' };
        }
        if (u.id && optimisticSent.has(u.id)) {
          return { user: u, relation: 'sent' };
        }
        const rel = u.id ? relationByUserId.get(u.id) : undefined;
        return {
          user: u,
          relation: rel,
          pending: u.id ? pendingSend.has(u.id) : false,
        };
      });
  }, [searchData, me, relationByUserId, optimisticSent, pendingSend]);

  const isInitialLoad =
    !isSearchMode &&
    ((friendsQ.isLoading && !friendsQ.data) || (requestsQ.isLoading && !requestsQ.data));

  return (
    <View className="flex-1 bg-background">
      <SearchBar value={rawQuery} onChange={setRawQuery} />

      {isSearchMode ? (
        <SearchPane
          searchEnabled={searchEnabled}
          isFetching={searchQ.isFetching}
          rows={searchRows}
          onAdd={onAddFriend}
        />
      ) : isInitialLoad ? (
        <FriendsLoading />
      ) : (
        <SectionsPane rows={sectionRows} />
      )}
    </View>
  );
}

function SearchBar({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="border-b border-border px-4 pb-3 pt-3">
      <View className="relative">
        <View className="absolute bottom-0 left-3 top-0 z-10 justify-center">
          <Search size={16} color={mutedFg} />
        </View>
        <Input
          value={value}
          onChangeText={onChange}
          placeholder="Search by username or display name"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="off"
          returnKeyType="search"
          testID="friend-search-input"
          accessibilityLabel="Search for users to add as a friend"
          className="pl-9 pr-9"
        />
        {value.length > 0 ? (
          <Pressable
            onPress={() => onChange('')}
            accessibilityRole="button"
            accessibilityLabel="Clear search"
            testID="friend-search-clear"
            hitSlop={8}
            className="absolute bottom-0 right-3 top-0 z-10 justify-center">
            <X size={16} color={mutedFg} />
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

function SectionsPane({ rows }: { rows: Row[] }) {
  if (rows.length === 0) return <FriendsAllEmpty />;
  // No friends, no requests — show the welcoming empty state instead
  // of a lone "Friends (0)" header.
  const onlyEmptyHeader =
    rows.length === 2 &&
    rows[0].kind === 'header' &&
    rows[0].key === 'h:friends' &&
    rows[1].kind === 'empty';
  if (onlyEmptyHeader) return <FriendsAllEmpty />;

  return (
    <List
      data={rows}
      keyExtractor={(item) => item.key}
      renderItem={({ item }) => <RenderedRow row={item} />}
    />
  );
}

function SearchPane({
  searchEnabled,
  isFetching,
  rows,
  onAdd,
}: {
  searchEnabled: boolean;
  isFetching: boolean;
  rows: SearchRow[];
  onAdd: (user: UserRow) => void;
}) {
  const fg = useThemeColor('muted-foreground');
  if (!searchEnabled) {
    // We're between the user starting to type and the debounce
    // catching up — render nothing so the screen doesn't flash an
    // empty state for ~250ms.
    return <View className="flex-1" />;
  }
  if (isFetching && rows.length === 0) {
    return (
      <View className="flex-1 items-center justify-center py-12">
        <ActivityIndicator color={fg} />
      </View>
    );
  }
  if (rows.length === 0) {
    return (
      <View className="px-6 py-12">
        <Text variant="muted" className="text-center">
          No users matched.
        </Text>
      </View>
    );
  }
  return (
    <List
      data={rows}
      keyExtractor={(r, i) => r.user.id ?? r.user.username ?? `idx-${i}`}
      renderItem={({ item }) => <SearchResultRow row={item} onAdd={onAdd} />}
    />
  );
}

function SearchResultRow({ row, onAdd }: { row: SearchRow; onAdd: (user: UserRow) => void }) {
  const u = row.user;
  let trailing: React.ReactNode;
  switch (row.relation) {
    case 'self':
      trailing = (
        <Text variant="muted" className="text-xs">
          You
        </Text>
      );
      break;
    case 'friend':
      trailing = (
        <Text variant="muted" className="text-xs">
          Friend
        </Text>
      );
      break;
    case 'incoming':
      trailing = (
        <Text variant="muted" className="text-xs">
          Wants to add you
        </Text>
      );
      break;
    case 'outgoing':
    case 'sent':
      trailing = (
        <Text variant="muted" className="text-xs">
          Pending
        </Text>
      );
      break;
    default:
      trailing = (
        <Button
          size="sm"
          variant="default"
          disabled={row.pending}
          onPress={() => onAdd(u)}
          accessibilityLabel={`Send friend request to ${u.username ?? 'user'}`}
          testID={`friend-add-${u.id ?? u.username}`}>
          <Text>{row.pending ? 'Adding…' : 'Add'}</Text>
        </Button>
      );
  }
  return (
    <FriendRow
      displayName={u.display_name}
      username={u.username}
      avatarUrl={u.avatar_url}
      hidePresence
      trailing={trailing}
    />
  );
}

function RenderedRow({ row }: { row: Row }) {
  switch (row.kind) {
    case 'header':
      return <SectionHeader title={row.title} count={row.count} />;
    case 'empty':
      return <SectionEmpty subtitle={row.subtitle} />;
    case 'friend': {
      const u = row.friendship.user;
      return (
        <FriendRow
          displayName={u?.display_name}
          username={u?.username}
          avatarUrl={u?.avatar_url}
          statusEmoji={u?.status_emoji}
          presence={row.presence}
        />
      );
    }
    case 'request': {
      const u = row.friendship.user;
      // Phase 4.4 swaps the trailing slot for accept/decline buttons
      // (incoming) and a "Pending" indicator (outgoing). For 4.2 we
      // surface intent through a muted text marker.
      const marker =
        row.direction === 'incoming' ? (
          <Text variant="muted" className="text-xs">
            wants to be friends
          </Text>
        ) : (
          <Text variant="muted" className="text-xs">
            Pending
          </Text>
        );
      return (
        <FriendRow
          displayName={u?.display_name}
          username={u?.username}
          avatarUrl={u?.avatar_url}
          hidePresence
          trailing={marker}
        />
      );
    }
  }
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

function SectionEmpty({ subtitle }: { subtitle: string }) {
  return (
    <View className="px-4 py-6">
      <Text variant="muted" className="text-center text-sm">
        {subtitle}
      </Text>
    </View>
  );
}

function FriendsLoading() {
  const fg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center bg-background">
      <ActivityIndicator color={fg} />
    </View>
  );
}

function FriendsAllEmpty() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <EmptyState
        icon={<Users size={40} color={mutedFg} />}
        title="No friends yet"
        subtitle="Search above by username or display name to send your first friend request."
      />
    </View>
  );
}
