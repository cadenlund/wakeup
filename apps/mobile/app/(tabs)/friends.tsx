// Phase 4.2 + 4.3 + 4.4 + 4.5 — Friends tab. Three sections rendered
// through a single FlashList:
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
// to render the right badge.
//
// Actions wired in 4.4:
//   - Incoming requests: inline Accept / Decline buttons.
//   - Accepted friends: long-press (or the trailing "…" button)
//     opens a bottom-sheet menu with Unfriend / Block.
// Confirmations live in the menu itself — tapping a destructive
// item commits immediately and toasts the result. A separate
// confirm dialog would just add a second tap and a worse web UX
// (where window.alert is the only out-of-the-box option).
//
// Pull-to-refresh: tugging down on the sections list refetches all
// three relationship queries at once. Search mode doesn't expose a
// pull-to-refresh because the search query already runs live off
// every keystroke (debounced).
import {
  Check,
  ChevronDown,
  ChevronRight,
  MoreHorizontal,
  Search,
  ShieldOff,
  UserMinus,
  Users,
  X,
} from 'lucide-react-native';
import { useRouter } from 'expo-router';
import * as React from 'react';
import { ActivityIndicator, Modal, Pressable, RefreshControl, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { FriendRow } from '@/components/friend-row';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List, type ListRef } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1FriendsQueryKey,
  getGetV1FriendsRequestsQueryKey,
  useDeleteV1FriendsUserId,
  useGetV1Friends,
  useGetV1FriendsRequests,
  usePostV1FriendsRequests,
  usePostV1FriendsRequestsIdAccept,
  usePostV1FriendsRequestsIdDecline,
  usePostV1FriendsUserIdBlock,
} from '@/lib/api/hooks/friends/friends';
import {
  getGetV1PresenceFriendsQueryKey,
  useGetV1PresenceFriends,
} from '@/lib/api/hooks/presence/presence';
import { useGetV1Search } from '@/lib/api/hooks/search/search';
import { useEnsureDirectConversation } from '@/lib/api/use-ensure-direct-conversation';
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
type SectionId = 'incoming' | 'outgoing' | 'friends';

type Row =
  | {
      kind: 'header';
      key: string;
      sectionId: SectionId;
      title: string;
      count: number;
      collapsed: boolean;
    }
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
  const acceptRequest = usePostV1FriendsRequestsIdAccept();
  const declineRequest = usePostV1FriendsRequestsIdDecline();
  const unfriend = useDeleteV1FriendsUserId();
  const blockUser = usePostV1FriendsUserIdBlock();

  // Pending action set keyed by friendship id (for accept/decline) or
  // user id (for unfriend/block) so the row can show a disabled state
  // while the request is in flight without mounting a per-row spinner.
  const [pendingAction, setPendingAction] = React.useState<Set<string>>(new Set());
  const markPending = React.useCallback((id: string) => {
    setPendingAction((prev) => {
      const next = new Set(prev);
      next.add(id);
      return next;
    });
  }, []);
  const unmarkPending = React.useCallback((id: string) => {
    setPendingAction((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);

  // Bottom-sheet state for the per-friend action menu.
  const [menuTarget, setMenuTarget] = React.useState<UserRow | null>(null);

  const invalidateRelationships = React.useCallback(async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: getGetV1FriendsRequestsQueryKey() }),
      qc.invalidateQueries({ queryKey: getGetV1FriendsQueryKey() }),
      qc.invalidateQueries({ queryKey: getGetV1PresenceFriendsQueryKey() }),
    ]);
  }, [qc]);

  const surfaceError = React.useCallback((err: unknown, fallback: string) => {
    const msg = err instanceof APIError && err.message ? err.message : fallback;
    toast.error(msg);
  }, []);

  // Phase 5.3 — tap a friend row to open (or lazily create) the
  // direct conversation with them. Cache hits are instant, cache
  // misses POST a new conversation. Either way we route to the
  // thread.
  const router = useRouter();
  const ensureDM = useEnsureDirectConversation();
  const [openingDmFor, setOpeningDmFor] = React.useState<string | null>(null);
  const onOpenDMWithFriend = React.useCallback(
    async (friendUserId: string) => {
      if (openingDmFor) return; // ignore double-tap
      setOpeningDmFor(friendUserId);
      try {
        const { conversationId } = await ensureDM.ensure(friendUserId);
        router.push(`/conversations/${conversationId}`);
      } catch (err) {
        surfaceError(err, "Couldn't open the conversation right now.");
      } finally {
        setOpeningDmFor(null);
      }
    },
    [openingDmFor, ensureDM, router, surfaceError]
  );

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
        surfaceError(err, "Couldn't send the request — try again in a sec.");
      } finally {
        setPendingSend((prev) => {
          const next = new Set(prev);
          next.delete(user.id!);
          return next;
        });
      }
    },
    [sendRequest, qc, surfaceError]
  );

  const onAcceptRequest = React.useCallback(
    async (friendship: Friendship) => {
      const id = friendship.id;
      if (!id) return;
      markPending(id);
      try {
        await acceptRequest.mutateAsync({ id });
        await invalidateRelationships();
        const handle = friendship.user?.username ? `@${friendship.user.username}` : 'them';
        toast.success("You're now friends", `Say hi to ${handle}.`);
      } catch (err) {
        surfaceError(err, "Couldn't accept this request right now.");
      } finally {
        unmarkPending(id);
      }
    },
    [acceptRequest, invalidateRelationships, markPending, unmarkPending, surfaceError]
  );

  const onDeclineRequest = React.useCallback(
    async (friendship: Friendship) => {
      const id = friendship.id;
      if (!id) return;
      markPending(id);
      try {
        await declineRequest.mutateAsync({ id });
        await invalidateRelationships();
        toast.info('Request declined');
      } catch (err) {
        surfaceError(err, "Couldn't decline this request right now.");
      } finally {
        unmarkPending(id);
      }
    },
    [declineRequest, invalidateRelationships, markPending, unmarkPending, surfaceError]
  );

  const onUnfriend = React.useCallback(
    async (user: UserRow) => {
      const userId = user.id;
      if (!userId) return;
      markPending(userId);
      setMenuTarget(null);
      try {
        await unfriend.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = user.username ? `@${user.username}` : 'this user';
        toast.info('Unfriended', `${handle} is no longer in your friends.`);
      } catch (err) {
        surfaceError(err, "Couldn't unfriend right now.");
      } finally {
        unmarkPending(userId);
      }
    },
    [unfriend, invalidateRelationships, markPending, unmarkPending, surfaceError]
  );

  const onBlock = React.useCallback(
    async (user: UserRow) => {
      const userId = user.id;
      if (!userId) return;
      markPending(userId);
      setMenuTarget(null);
      try {
        await blockUser.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = user.username ? `@${user.username}` : 'this user';
        toast.info('Blocked', `${handle} can't message or add you.`);
      } catch (err) {
        surfaceError(err, "Couldn't block right now.");
      } finally {
        unmarkPending(userId);
      }
    },
    [blockUser, invalidateRelationships, markPending, unmarkPending, surfaceError]
  );

  // Per-section collapsed state. Tap a header chevron to fold its
  // rows out of view without losing the count. Lives in memory only
  // — fine for now; persistence under STORAGE_KEYS lands when there
  // are more user-prefs to keep with it.
  const [collapsedSections, setCollapsedSections] = React.useState<Set<SectionId>>(new Set());
  // Track the section that was just toggled (in either direction)
  // so the post-render effect can scroll its header to the top.
  // Without this, FlashList preserves the prior scroll offset:
  //   - on expand, the new rows can push the tapped header behind
  //     the screen chrome
  //   - on collapse, removed rows above the offset leave the list
  //     scrolled into empty space below the (now-shorter) content
  // Pinning the tapped header to the top side-steps both.
  const justToggledRef = React.useRef<SectionId | null>(null);
  const toggleSection = React.useCallback((id: SectionId) => {
    setCollapsedSections((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    justToggledRef.current = id;
  }, []);

  const sectionRows = React.useMemo<Row[]>(() => {
    const friends = friendsData?.data ?? [];
    const incoming = requestsData?.incoming ?? [];
    const outgoing = requestsData?.outgoing ?? [];

    const out: Row[] = [];

    if (incoming.length > 0) {
      const collapsed = collapsedSections.has('incoming');
      out.push({
        kind: 'header',
        key: 'h:incoming',
        sectionId: 'incoming',
        title: 'Incoming requests',
        count: incoming.length,
        collapsed,
      });
      if (!collapsed) {
        incoming.forEach((f, i) => {
          out.push({
            kind: 'request',
            key: `req:in:${f.id ?? f.user?.id ?? `idx-${i}`}`,
            friendship: f,
            direction: 'incoming',
          });
        });
      }
    }

    if (outgoing.length > 0) {
      const collapsed = collapsedSections.has('outgoing');
      out.push({
        kind: 'header',
        key: 'h:outgoing',
        sectionId: 'outgoing',
        title: 'Sent requests',
        count: outgoing.length,
        collapsed,
      });
      if (!collapsed) {
        outgoing.forEach((f, i) => {
          out.push({
            kind: 'request',
            key: `req:out:${f.id ?? f.user?.id ?? `idx-${i}`}`,
            friendship: f,
            direction: 'outgoing',
          });
        });
      }
    }

    const friendsCollapsed = collapsedSections.has('friends');
    out.push({
      kind: 'header',
      key: 'h:friends',
      sectionId: 'friends',
      title: 'Friends',
      count: friends.length,
      collapsed: friendsCollapsed,
    });
    if (!friendsCollapsed) {
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
    }

    return out;
  }, [friendsData, requestsData, presenceByUser, collapsedSections]);

  const searchRows = React.useMemo<SearchRow[]>(() => {
    const users = searchData?.users ?? [];
    return users
      .filter((u) => u.id) // backend guarantees this; defensive
      .map<SearchRow>((u) => {
        if (me?.id && u.id === me.id) {
          return { user: u, relation: 'self' };
        }
        // Server-derived relation wins over the optimistic "sent"
        // state — once requests refetches (or a 4.6 WS update flips
        // them to friend / incoming), we stop pinning the row to
        // "Pending". Optimistic state is only a fallback for the
        // window between mutate and refetch settling.
        const rel = u.id ? relationByUserId.get(u.id) : undefined;
        if (rel) {
          return { user: u, relation: rel };
        }
        if (u.id && optimisticSent.has(u.id)) {
          return { user: u, relation: 'sent' };
        }
        return {
          user: u,
          pending: u.id ? pendingSend.has(u.id) : false,
        };
      });
  }, [searchData, me, relationByUserId, optimisticSent, pendingSend]);

  const isInitialLoad =
    !isSearchMode &&
    ((friendsQ.isLoading && !friendsQ.data) || (requestsQ.isLoading && !requestsQ.data));

  // Pull-to-refresh: refetch all three relationship queries in
  // parallel. Local refreshing flag lives independently of the
  // queries' isFetching so a passive background refetch doesn't show
  // the spinner.
  const [refreshing, setRefreshing] = React.useState(false);
  const onRefresh = React.useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([friendsQ.refetch(), requestsQ.refetch(), presenceQ.refetch()]);
    } finally {
      setRefreshing(false);
    }
  }, [friendsQ, requestsQ, presenceQ]);

  return (
    <View className="flex-1 bg-background">
      <SearchBar value={rawQuery} onChange={setRawQuery} />

      {isSearchMode ? (
        <SearchPane
          searchEnabled={searchEnabled}
          isFetching={searchQ.isFetching}
          isError={searchQ.isError}
          rows={searchRows}
          onAdd={onAddFriend}
        />
      ) : isInitialLoad ? (
        <FriendsLoading />
      ) : (
        <SectionsPane
          rows={sectionRows}
          pendingAction={pendingAction}
          onAccept={onAcceptRequest}
          onDecline={onDeclineRequest}
          onOpenMenu={setMenuTarget}
          onOpenDM={onOpenDMWithFriend}
          menuOpen={!!menuTarget}
          onToggleSection={toggleSection}
          refreshing={refreshing}
          onRefresh={onRefresh}
          justToggledRef={justToggledRef}
        />
      )}

      <FriendActionMenu
        target={menuTarget}
        pendingAction={pendingAction}
        onClose={() => setMenuTarget(null)}
        onUnfriend={onUnfriend}
        onBlock={onBlock}
      />
    </View>
  );
}

function SearchBar({ value, onChange }: { value: string; onChange: (v: string) => void }) {
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

function useThemedRefreshControl(
  refreshing: boolean,
  onRefresh: () => void
): React.ReactElement<React.ComponentProps<typeof RefreshControl>> {
  // Plain RN default tint. The custom-colour route through
  // RefreshControl's tintColor / colors props turned out to be
  // flaky across UIRefreshControl / SwipeRefreshLayout — the
  // default native spinner reads fine in both modes once nothing
  // bleeds through the search row above.
  return <RefreshControl refreshing={refreshing} onRefresh={onRefresh} />;
}

function SectionsPane({
  rows,
  pendingAction,
  onAccept,
  onDecline,
  onOpenMenu,
  onOpenDM,
  menuOpen,
  onToggleSection,
  refreshing,
  onRefresh,
  justToggledRef,
}: {
  rows: Row[];
  pendingAction: Set<string>;
  onAccept: (f: Friendship) => void;
  onDecline: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  // True while the bottom-sheet action menu is open. Threaded
  // through to RenderedRow so a Pressable's onPress can't race the
  // long-press → menu transition (CR #134).
  menuOpen: boolean;
  onToggleSection: (id: SectionId) => void;
  refreshing: boolean;
  onRefresh: () => void;
  justToggledRef: React.MutableRefObject<SectionId | null>;
}) {
  const listRef = React.useRef<ListRef<Row>>(null);

  // After any section toggle (expand OR collapse), scroll its
  // header to the top of the viewport. FlashList preserves the
  // prior scroll offset across data updates, so without this:
  //   - expand → inserted rows push the header behind the screen
  //     chrome
  //   - collapse → removed rows above the offset leave the list
  //     scrolled into empty space below the now-shorter content
  // Pinning the tapped header to the top is the predictable shape
  // either way.
  React.useEffect(() => {
    const id = justToggledRef.current;
    if (!id) return;
    const idx = rows.findIndex((r) => r.kind === 'header' && r.sectionId === id);
    if (idx >= 0) {
      listRef.current?.scrollToIndex({ index: idx, animated: true });
    }
    justToggledRef.current = null;
  }, [rows, justToggledRef]);

  const refreshControl = useThemedRefreshControl(refreshing, onRefresh);

  // Empty / single-empty-header collapse to the welcoming empty
  // state; we still wrap that in a ScrollView so pull-to-refresh
  // works even when there's no data yet.
  if (rows.length === 0) {
    return <PullableEmpty refreshControl={refreshControl} />;
  }
  // Friends section is the only one that's always rendered. If it's
  // the lone header AND empty AND not collapsed, fall through to the
  // welcoming empty state. (When collapsed-by-tap the user has
  // explicitly hidden it, so still show the toggleable header.)
  const onlyEmptyHeader =
    rows.length === 2 &&
    rows[0].kind === 'header' &&
    rows[0].key === 'h:friends' &&
    rows[0].collapsed === false &&
    rows[1].kind === 'empty';
  if (onlyEmptyHeader) {
    return <PullableEmpty refreshControl={refreshControl} />;
  }

  return (
    <List
      ref={listRef}
      data={rows}
      keyExtractor={(item) => item.key}
      renderItem={({ item }) => (
        <RenderedRow
          row={item}
          pendingAction={pendingAction}
          onAccept={onAccept}
          onDecline={onDecline}
          onOpenMenu={onOpenMenu}
          onOpenDM={onOpenDM}
          menuOpen={menuOpen}
          onToggleSection={onToggleSection}
        />
      )}
      refreshControl={refreshControl}
    />
  );
}

function PullableEmpty({
  refreshControl,
}: {
  refreshControl: React.ReactElement<React.ComponentProps<typeof RefreshControl>>;
}) {
  // FlashList doesn't render its scroll surface when data is empty,
  // so a refresh-control wouldn't be reachable on the empty state.
  // Wrap a single-item list (the empty placeholder) in <List> so the
  // pull-to-refresh affordance is still available.
  type EmptyItem = { kind: 'empty-screen' };
  const data: EmptyItem[] = [{ kind: 'empty-screen' }];
  return (
    <List
      data={data}
      keyExtractor={(_, i) => `empty-${i}`}
      renderItem={() => <FriendsAllEmpty />}
      refreshControl={refreshControl}
    />
  );
}

function SearchPane({
  searchEnabled,
  isFetching,
  isError,
  rows,
  onAdd,
}: {
  searchEnabled: boolean;
  isFetching: boolean;
  isError: boolean;
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
  // Distinguish "server failed" from "no matches" — the former needs
  // to read as recoverable, not as an empty success.
  if (isError && rows.length === 0) {
    return (
      <View className="px-6 py-12">
        <Text variant="muted" className="text-center">
          Search couldn&apos;t reach the server. Check your connection and try again.
        </Text>
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

function RenderedRow({
  row,
  pendingAction,
  onAccept,
  onDecline,
  onOpenMenu,
  onOpenDM,
  menuOpen,
  onToggleSection,
}: {
  row: Row;
  pendingAction: Set<string>;
  onAccept: (f: Friendship) => void;
  onDecline: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  menuOpen: boolean;
  onToggleSection: (id: SectionId) => void;
}) {
  switch (row.kind) {
    case 'header':
      return (
        <SectionHeader
          title={row.title}
          count={row.count}
          collapsed={row.collapsed}
          onToggle={() => onToggleSection(row.sectionId)}
        />
      );
    case 'empty':
      return <SectionEmpty subtitle={row.subtitle} />;
    case 'friend': {
      const u = row.friendship.user;
      const userId = u?.id;
      const inFlight = userId ? pendingAction.has(userId) : false;
      return (
        <FriendRow
          displayName={u?.display_name}
          username={u?.username}
          avatarUrl={u?.avatar_url}
          statusEmoji={u?.status_emoji}
          presence={row.presence}
          onPress={userId && !inFlight && !menuOpen ? () => onOpenDM(userId) : undefined}
          onLongPress={u ? () => onOpenMenu(u) : undefined}
          trailing={
            u ? <RowMenuButton disabled={inFlight} onPress={() => onOpenMenu(u)} /> : undefined
          }
        />
      );
    }
    case 'request': {
      const u = row.friendship.user;
      const fid = row.friendship.id;
      const inFlight = fid ? pendingAction.has(fid) : false;
      const trailing =
        row.direction === 'incoming' ? (
          <RequestActions
            disabled={inFlight}
            onAccept={() => onAccept(row.friendship)}
            onDecline={() => onDecline(row.friendship)}
          />
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
          trailing={trailing}
        />
      );
    }
  }
}

function RequestActions({
  disabled,
  onAccept,
  onDecline,
}: {
  disabled: boolean;
  onAccept: () => void;
  onDecline: () => void;
}) {
  const fg = useThemeColor('foreground');
  return (
    <>
      <Button
        size="icon"
        variant="outline"
        disabled={disabled}
        onPress={onDecline}
        accessibilityLabel="Decline friend request"
        testID="friend-request-decline">
        <X size={16} color={fg} />
      </Button>
      <Button
        size="icon"
        variant="default"
        disabled={disabled}
        onPress={onAccept}
        accessibilityLabel="Accept friend request"
        testID="friend-request-accept">
        <Check size={16} color="#fff" />
      </Button>
    </>
  );
}

function RowMenuButton({ disabled, onPress }: { disabled: boolean; onPress: () => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      accessibilityRole="button"
      accessibilityLabel="More actions"
      testID="friend-row-menu"
      hitSlop={6}
      className="h-8 w-8 items-center justify-center rounded-md active:bg-muted">
      <MoreHorizontal size={18} color={mutedFg} />
    </Pressable>
  );
}

function FriendActionMenu({
  target,
  pendingAction,
  onClose,
  onUnfriend,
  onBlock,
}: {
  target: UserRow | null;
  pendingAction: Set<string>;
  onClose: () => void;
  onUnfriend: (u: UserRow) => void;
  onBlock: (u: UserRow) => void;
}) {
  const fg = useThemeColor('foreground');
  const destructive = useThemeColor('destructive');
  const mutedFg = useThemeColor('muted-foreground');
  const handle = target?.username ? `@${target.username}` : (target?.display_name ?? '');
  const inFlight = target?.id ? pendingAction.has(target.id) : false;
  return (
    <Modal
      visible={!!target}
      transparent
      animationType="fade"
      onRequestClose={onClose}
      // Stop scrolling underneath while the sheet is up.
      statusBarTranslucent>
      <Pressable
        accessibilityLabel="Dismiss"
        onPress={onClose}
        className="flex-1 justify-end bg-black/40">
        {/* Inner Pressable absorbs touches on the sheet itself so taps
            inside don't bubble to the dimmer and dismiss it. */}
        <Pressable onPress={() => {}} className="rounded-t-3xl bg-card">
          <View className="items-center pt-3">
            <View className="h-1 w-12 rounded-full bg-muted-foreground/30" />
          </View>
          <View className="px-4 pb-2 pt-3">
            <Text variant="muted" className="text-center text-sm">
              {handle}
            </Text>
          </View>
          <View className="px-2 pb-6">
            <Pressable
              onPress={() => target && onUnfriend(target)}
              disabled={inFlight}
              accessibilityRole="button"
              accessibilityLabel="Unfriend"
              testID="friend-menu-unfriend"
              className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
              <UserMinus size={18} color={fg} />
              <Text className="text-base">Unfriend</Text>
            </Pressable>
            <Pressable
              onPress={() => target && onBlock(target)}
              disabled={inFlight}
              accessibilityRole="button"
              accessibilityLabel="Block"
              testID="friend-menu-block"
              className="flex-row items-center gap-3 rounded-lg px-3 py-3 active:bg-muted">
              <ShieldOff size={18} color={destructive} />
              <Text style={{ color: destructive }} className="text-base font-medium">
                Block
              </Text>
            </Pressable>
            <Pressable
              onPress={onClose}
              accessibilityRole="button"
              accessibilityLabel="Cancel"
              testID="friend-menu-cancel"
              className="mt-2 items-center rounded-lg px-3 py-3 active:bg-muted">
              <Text style={{ color: mutedFg }} className="text-sm">
                Cancel
              </Text>
            </Pressable>
          </View>
        </Pressable>
      </Pressable>
    </Modal>
  );
}

function SectionHeader({
  title,
  count,
  collapsed,
  onToggle,
}: {
  title: string;
  count: number;
  collapsed: boolean;
  onToggle: () => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  // Caret reads "current state": ChevronRight when closed (rotated
  // 90°), ChevronDown when open. Same convention as macOS Finder
  // disclosure triangles.
  const Caret = collapsed ? ChevronRight : ChevronDown;
  return (
    <Pressable
      onPress={onToggle}
      accessibilityRole="button"
      // Stable label that announces the section title + count.
      // expanded/collapsed state is conveyed via accessibilityState
      // so VoiceOver and TalkBack can phrase it idiomatically
      // ("Friends, 3 items, expanded").
      accessibilityLabel={`${title}, ${count} ${count === 1 ? 'item' : 'items'}`}
      accessibilityState={{ expanded: !collapsed }}
      testID={`friend-section-${title.toLowerCase().replace(/\s+/g, '-')}`}
      className="flex-row items-center gap-2 border-b border-border bg-muted/30 px-4 py-2 active:bg-muted">
      <Caret size={14} color={mutedFg} />
      <Text variant="muted" className="flex-1 text-xs font-semibold uppercase tracking-wider">
        {title}
      </Text>
      <Text variant="muted" className="text-xs">
        {count}
      </Text>
    </Pressable>
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
