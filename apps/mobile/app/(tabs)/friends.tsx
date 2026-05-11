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
import { ChevronDown, ChevronRight, Search, Users, X } from 'lucide-react-native';
import { useRouter } from 'expo-router';
import * as React from 'react';
import { ActivityIndicator, Pressable, RefreshControl, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { FriendActionMenu, FriendRowMenuButton } from '@/components/friend-action-menu';
import { FriendRow } from '@/components/friend-row';
import { FriendStatusAction, type FriendStatus } from '@/components/friend-status-action';
import { RelationshipBadge } from '@/components/relationship-badge';
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
  useGetV1FriendsRequests,
  usePostV1FriendsRequestsIdAccept,
  usePostV1FriendsRequestsIdDecline,
  usePostV1FriendsUserIdBlock,
} from '@/lib/api/hooks/friends/friends';
import { flatten, useInfiniteFriends, useInfiniteUsers } from '@/lib/api/use-infinite';
import {
  getGetV1PresenceFriendsQueryKey,
  useGetV1PresenceFriends,
} from '@/lib/api/hooks/presence/presence';
import { useEnsureDirectConversation } from '@/lib/api/use-ensure-direct-conversation';
import { useFriendActions } from '@/lib/api/use-friend-actions';
import type {
  InternalHandlerHttpFriendRequestsResponse,
  InternalHandlerHttpFriendshipResponse,
  InternalHandlerHttpPresenceListResponse,
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
  | { kind: 'show-all'; key: string; section: 'incoming' | 'outgoing'; label: string }
  | { kind: 'empty'; key: string; subtitle: string };

// Top-N each request section truncates to before showing a
// "Show N more requests" row. Friends section is uncapped — at
// realistic scale users scroll their friends list, but they
// rarely have so many pending requests that scanning the list
// is a problem. Mirrors the global-search modal pattern.
const VISIBLE_REQUESTS = 5;

// Search-mode row variant — backend returns search hits in a flat
// list, no headers. `status` matches the shared FriendStatusAction
// map; `self` is the only state outside that vocabulary (you
// shouldn't befriend yourself), so it gets its own flag.
type SearchRow = {
  user: UserRow;
  status?: FriendStatus;
  isSelf?: boolean;
  // Presence is friends-only (§7.2), so only friend rows carry a
  // value here. Non-friend search hits leave it undefined and the
  // FriendRow hides the presence dot.
  presence?: string;
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
  // Infinite-scroll friends list (§6.4). Earlier this asked for
  // limit:100 which capped users with more friends than that and
  // ignored next_cursor entirely.
  const friendsQ = useInfiniteFriends({ query: { staleTime: 30_000 } });
  const requestsQ = useGetV1FriendsRequests({ query: { staleTime: 30_000 } });
  const presenceQ = useGetV1PresenceFriends({ query: { staleTime: 15_000 } });
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });

  const { data: friendsList, total: friendsTotal } = React.useMemo(
    () => flatten<Friendship, { data?: Friendship[] }>(friendsQ.data?.pages),
    [friendsQ.data]
  );

  // apiFetch returns the unwrapped JSON body; orval types the response
  // as the {data, status, headers} envelope. Cast to the runtime shape
  // (same pattern as auth-gate.tsx).
  const requestsData = requestsQ.data as InternalHandlerHttpFriendRequestsResponse | undefined;
  const presenceData = presenceQ.data as InternalHandlerHttpPresenceListResponse | undefined;
  const me = meQ.data as { id?: string } | undefined;

  // Search state.
  const [rawQuery, setRawQuery] = React.useState('');
  const debouncedQuery = useDebouncedValue(rawQuery.trim(), SEARCH_DEBOUNCE_MS);
  const isSearchMode = rawQuery.trim().length >= SEARCH_MIN_CHARS;
  const searchEnabled = debouncedQuery.length >= SEARCH_MIN_CHARS;

  // Per-section paginated user search — friends tab needs the FULL
  // result set (so users with hundreds of matching contacts can still
  // scroll past the first 10), not the global-search 10-cap. The
  // /v1/users endpoint runs the same trigram + substring match the
  // global modal does and supports the keyset cursor we drive
  // infinite-scroll off of.
  const searchQ = useInfiniteUsers(
    { q: debouncedQuery },
    { query: { enabled: searchEnabled, staleTime: 30_000 } }
  );
  const { data: searchUsers, total: searchTotal } = React.useMemo(
    () => flatten<UserRow, { data?: UserRow[] }>(searchQ.data?.pages),
    [searchQ.data]
  );

  const presenceByUser = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const p of presenceData?.data ?? []) {
      if (p.user_id && p.status) m.set(p.user_id, p.status);
    }
    return m;
  }, [presenceData]);

  // Build a lookup of existing relationships so search-mode rows
  // can render the right affordance without an extra per-user
  // request. Outgoing / incoming entries carry the friendship.id
  // so the Unsend / Accept / Decline buttons can target the right
  // row — same map shape FriendStatusAction consumes elsewhere.
  const friendStatusByUserId = React.useMemo(() => {
    const m = new Map<string, FriendStatus>();
    for (const f of friendsList) {
      if (f.user?.id) m.set(f.user.id, { kind: 'friend' });
    }
    for (const f of requestsData?.incoming ?? []) {
      if (f.user?.id && f.id) m.set(f.user.id, { kind: 'incoming', requestId: f.id });
    }
    for (const f of requestsData?.outgoing ?? []) {
      if (f.user?.id && f.id) m.set(f.user.id, { kind: 'outgoing', requestId: f.id });
    }
    return m;
  }, [friendsList, requestsData]);

  // friendActions handles in-flight pending + invalidation, so the
  // row's status flips from `none` → `outgoing` automatically once
  // requestsData refetches. No local optimisticSent state needed.

  const qc = useQueryClient();
  const acceptRequest = usePostV1FriendsRequestsIdAccept();
  const declineRequest = usePostV1FriendsRequestsIdDecline();
  const unfriend = useDeleteV1FriendsUserId();
  const blockUser = usePostV1FriendsUserIdBlock();
  // Shared friend-action hook — drives the trailing affordance on
  // outgoing-request rows (Unsend) and supplies the per-row pending
  // flag so the right pill spins. Same source of truth as the
  // global search modal so a user who unsends from one surface
  // sees the row clear from the other after invalidation lands.
  const friendActions = useFriendActions();

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
  // "Show all" expansion for the request sections — independent of
  // the chevron-collapse state above. Default is truncated to top
  // VISIBLE_REQUESTS; tapping the show-all row promotes the
  // section here and the rest of the rows render. Friends section
  // is uncapped so it doesn't appear here.
  const [expandedRequestSections, setExpandedRequestSections] = React.useState<
    Set<'incoming' | 'outgoing'>
  >(new Set());
  const expandRequestSection = React.useCallback((section: 'incoming' | 'outgoing') => {
    setExpandedRequestSections((prev) => {
      if (prev.has(section)) return prev;
      const next = new Set(prev);
      next.add(section);
      return next;
    });
  }, []);
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
    const friends = friendsList;
    const incoming = requestsData?.incoming ?? [];
    const outgoing = requestsData?.outgoing ?? [];

    const out: Row[] = [];

    if (incoming.length > 0) {
      const collapsed = collapsedSections.has('incoming');
      const showAll = expandedRequestSections.has('incoming');
      const visible = showAll ? incoming : incoming.slice(0, VISIBLE_REQUESTS);
      out.push({
        kind: 'header',
        key: 'h:incoming',
        sectionId: 'incoming',
        title: 'Incoming requests',
        count: incoming.length,
        collapsed,
      });
      if (!collapsed) {
        visible.forEach((f, i) => {
          out.push({
            kind: 'request',
            key: `req:in:${f.id ?? f.user?.id ?? `idx-${i}`}`,
            friendship: f,
            direction: 'incoming',
          });
        });
        if (!showAll && incoming.length > VISIBLE_REQUESTS) {
          const more = incoming.length - VISIBLE_REQUESTS;
          out.push({
            kind: 'show-all',
            key: 'show-all:incoming',
            section: 'incoming',
            label: `Show ${more} more ${more === 1 ? 'request' : 'requests'}`,
          });
        }
      }
    }

    if (outgoing.length > 0) {
      const collapsed = collapsedSections.has('outgoing');
      const showAll = expandedRequestSections.has('outgoing');
      const visible = showAll ? outgoing : outgoing.slice(0, VISIBLE_REQUESTS);
      out.push({
        kind: 'header',
        key: 'h:outgoing',
        sectionId: 'outgoing',
        title: 'Sent requests',
        count: outgoing.length,
        collapsed,
      });
      if (!collapsed) {
        visible.forEach((f, i) => {
          out.push({
            kind: 'request',
            key: `req:out:${f.id ?? f.user?.id ?? `idx-${i}`}`,
            friendship: f,
            direction: 'outgoing',
          });
        });
        if (!showAll && outgoing.length > VISIBLE_REQUESTS) {
          const more = outgoing.length - VISIBLE_REQUESTS;
          out.push({
            kind: 'show-all',
            key: 'show-all:outgoing',
            section: 'outgoing',
            label: `Show ${more} more ${more === 1 ? 'request' : 'requests'}`,
          });
        }
      }
    }

    const friendsCollapsed = collapsedSections.has('friends');
    out.push({
      kind: 'header',
      key: 'h:friends',
      sectionId: 'friends',
      title: 'Friends',
      // Header count is the absolute friends total from the
      // backend, not friends.length — with infinite scroll we
      // haven't necessarily loaded everyone, but the user wants
      // "you have N friends," not "I've shown you M of N."
      count: friendsTotal || friends.length,
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
  }, [
    friendsList,
    friendsTotal,
    requestsData,
    presenceByUser,
    collapsedSections,
    expandedRequestSections,
  ]);

  const searchRows = React.useMemo<SearchRow[]>(() => {
    const users = searchUsers;
    const mapped = users
      .filter((u) => u.id) // backend guarantees this; defensive
      .map<SearchRow>((u) => {
        if (me?.id && u.id === me.id) {
          return { user: u, isSelf: true };
        }
        const status = u.id ? friendStatusByUserId.get(u.id) : undefined;
        if (status?.kind === 'friend') {
          // Friends carry presence so the search-result row reads
          // with the same online/offline glance the section list
          // gives. presenceByUser is keyed by user_id.
          return { user: u, status, presence: u.id ? presenceByUser.get(u.id) : undefined };
        }
        if (status) return { user: u, status };
        return { user: u };
      });
    // Prioritise friends > pending requests > strangers in the
    // search results — typing a name is almost always "find my
    // friend X" and seeing strangers above the friend match is
    // the wrong answer. Backend search is sorted by created_at
    // DESC; we re-sort here without changing the order within
    // each tier (stable on backend's response order).
    const tier = (r: SearchRow) => {
      if (r.isSelf) return 4; // self at the bottom — opens nothing
      if (r.status?.kind === 'friend') return 0;
      if (r.status?.kind === 'incoming' || r.status?.kind === 'outgoing') return 1;
      return 2; // strangers
    };
    return mapped
      .map((r, idx) => ({ r, idx }))
      .sort((a, b) => {
        const t = tier(a.r) - tier(b.r);
        if (t !== 0) return t;
        return a.idx - b.idx;
      })
      .map(({ r }) => r);
  }, [searchUsers, me, friendStatusByUserId, presenceByUser]);

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
          total={searchTotal}
          friendActions={friendActions}
          onAcceptRequest={onAcceptRequest}
          onDeclineRequest={onDeclineRequest}
          onOpenMenu={setMenuTarget}
          onOpenDM={onOpenDMWithFriend}
          pendingAction={pendingAction}
          onEndReached={() => {
            if (searchQ.hasNextPage && !searchQ.isFetchingNextPage) {
              void searchQ.fetchNextPage();
            }
          }}
          isFetchingNextPage={searchQ.isFetchingNextPage}
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
          friendActions={friendActions}
          menuOpen={!!menuTarget}
          onToggleSection={toggleSection}
          onExpandRequestSection={expandRequestSection}
          refreshing={refreshing}
          onRefresh={onRefresh}
          justToggledRef={justToggledRef}
          onEndReached={() => {
            if (friendsQ.hasNextPage && !friendsQ.isFetchingNextPage) {
              void friendsQ.fetchNextPage();
            }
          }}
          isFetchingNextPage={friendsQ.isFetchingNextPage}
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
  friendActions,
  menuOpen,
  onToggleSection,
  onExpandRequestSection,
  refreshing,
  onRefresh,
  justToggledRef,
  onEndReached,
  isFetchingNextPage,
}: {
  rows: Row[];
  pendingAction: Set<string>;
  onAccept: (f: Friendship) => void;
  onDecline: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  friendActions: ReturnType<typeof useFriendActions>;
  // True while the bottom-sheet action menu is open. Threaded
  // through to RenderedRow so a Pressable's onPress can't race the
  // long-press → menu transition (CR #134).
  menuOpen: boolean;
  onToggleSection: (id: SectionId) => void;
  onExpandRequestSection: (section: 'incoming' | 'outgoing') => void;
  refreshing: boolean;
  onRefresh: () => void;
  justToggledRef: React.MutableRefObject<SectionId | null>;
  onEndReached: () => void;
  isFetchingNextPage: boolean;
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

  // Section-header rows stick to the top of the viewport while
  // their section is in view, so scrolling deep into Friends still
  // leaves the chevron tappable for collapse without scrolling
  // back up.
  const stickyHeaderIndices = React.useMemo(
    () => rows.map((r, i) => (r.kind === 'header' ? i : -1)).filter((i) => i >= 0),
    [rows]
  );

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
      stickyHeaderIndices={stickyHeaderIndices}
      onEndReachedThreshold={0.5}
      onEndReached={onEndReached}
      ListFooterComponent={isFetchingNextPage ? <SectionsListLoader /> : null}
      renderItem={({ item }) => (
        <RenderedRow
          row={item}
          pendingAction={pendingAction}
          onAccept={onAccept}
          onDecline={onDecline}
          onOpenMenu={onOpenMenu}
          onOpenDM={onOpenDM}
          friendActions={friendActions}
          menuOpen={menuOpen}
          onToggleSection={onToggleSection}
          onExpandRequestSection={onExpandRequestSection}
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
  total,
  friendActions,
  onAcceptRequest,
  onDeclineRequest,
  onOpenMenu,
  onOpenDM,
  pendingAction,
  onEndReached,
  isFetchingNextPage,
}: {
  searchEnabled: boolean;
  isFetching: boolean;
  isError: boolean;
  rows: SearchRow[];
  total: number;
  friendActions: ReturnType<typeof useFriendActions>;
  onAcceptRequest: (f: Friendship) => void;
  onDeclineRequest: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  pendingAction: Set<string>;
  onEndReached: () => void;
  isFetchingNextPage: boolean;
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
    <SearchPaneList
      rows={rows}
      total={total}
      friendActions={friendActions}
      onAcceptRequest={onAcceptRequest}
      onDeclineRequest={onDeclineRequest}
      onOpenMenu={onOpenMenu}
      onOpenDM={onOpenDM}
      pendingAction={pendingAction}
      onEndReached={onEndReached}
      isFetchingNextPage={isFetchingNextPage}
    />
  );
}

function SearchPaneList({
  rows,
  total,
  friendActions,
  onAcceptRequest,
  onDeclineRequest,
  onOpenMenu,
  onOpenDM,
  pendingAction,
  onEndReached,
  isFetchingNextPage,
}: {
  rows: SearchRow[];
  total: number;
  friendActions: ReturnType<typeof useFriendActions>;
  onAcceptRequest: (f: Friendship) => void;
  onDeclineRequest: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  pendingAction: Set<string>;
  onEndReached: () => void;
  isFetchingNextPage: boolean;
}) {
  return (
    <List
      data={rows}
      keyExtractor={(r, i) => r.user.id ?? r.user.username ?? `idx-${i}`}
      onEndReachedThreshold={0.5}
      onEndReached={onEndReached}
      ListFooterComponent={
        <SearchListFooter loading={isFetchingNextPage} loaded={rows.length} total={total} />
      }
      renderItem={({ item }) => (
        <SearchResultRow
          row={item}
          friendActions={friendActions}
          onAcceptRequest={onAcceptRequest}
          onDeclineRequest={onDeclineRequest}
          onOpenMenu={onOpenMenu}
          onOpenDM={onOpenDM}
          pendingAction={pendingAction}
        />
      )}
    />
  );
}

function SearchListFooter({
  loading,
  loaded,
  total,
}: {
  loading: boolean;
  loaded: number;
  total: number;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  if (loading) {
    return (
      <View className="items-center py-4">
        <ActivityIndicator color={mutedFg} />
      </View>
    );
  }
  // Keep the "Showing N of M" footer rendered even when N === M so
  // the user sees the final count (CodeRabbit on PR #138).
  if (total <= 0) return null;
  return (
    <View className="items-center py-3">
      <Text variant="muted" className="text-xs">
        Showing {loaded} of {total}
      </Text>
    </View>
  );
}

function SectionsListLoader() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="items-center py-4">
      <ActivityIndicator color={mutedFg} />
    </View>
  );
}

function SearchResultRow({
  row,
  friendActions,
  onAcceptRequest,
  onDeclineRequest,
  onOpenMenu,
  onOpenDM,
  pendingAction,
}: {
  row: SearchRow;
  friendActions: ReturnType<typeof useFriendActions>;
  onAcceptRequest: (f: Friendship) => void;
  onDeclineRequest: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  pendingAction: Set<string>;
}) {
  const u = row.user;
  // Only "you" stays a custom hint — every other relation maps
  // onto the shared FriendStatusAction so search results feel
  // identical to the section list and the global search modal.
  let trailing: React.ReactNode;
  if (row.isSelf) {
    trailing = (
      <Text variant="muted" className="text-xs">
        You
      </Text>
    );
  } else if (row.status?.kind === 'friend') {
    // Already a friend — show a "Friend" badge alongside the
    // 3-dots menu so the row reads as "this person is already in
    // your graph." Without the badge the action affordance alone
    // feels ambiguous (Instagram-style "Following" badge in their
    // search list).
    const inFlight = u.id ? pendingAction.has(u.id) : false;
    trailing = (
      <View className="flex-row items-center gap-2">
        <RelationshipBadge label="Friend" />
        <FriendRowMenuButton
          disabled={inFlight}
          onPress={() => onOpenMenu(u)}
          testID={u.id ? `friend-search-${u.id}-menu` : undefined}
        />
      </View>
    );
  } else if (row.status?.kind === 'outgoing') {
    // Pending outgoing request — "Added" badge mirrors what the
    // user said ("when u search that says friend or added"), with
    // the existing Unsend pill kept as the actionable affordance.
    const fid = row.status.requestId;
    trailing = (
      <View className="flex-row items-center gap-2">
        <RelationshipBadge label="Added" />
        <FriendStatusAction
          status={row.status}
          username={u.username}
          onAdd={friendActions.sendFriendRequest}
          onCancel={friendActions.cancelFriendRequest}
          isAdding={friendActions.isAddingFor(u.username)}
          isCanceling={friendActions.isCancelingFor(fid)}
          incomingMode="actions"
          testID={u.id ? `friend-search-${u.id}` : undefined}
        />
      </View>
    );
  } else {
    const fid = row.status?.requestId;
    const acceptDisabled = fid ? pendingAction.has(fid) : false;
    trailing = (
      <FriendStatusAction
        status={row.status}
        username={u.username}
        onAdd={friendActions.sendFriendRequest}
        onCancel={friendActions.cancelFriendRequest}
        isAdding={friendActions.isAddingFor(u.username)}
        isCanceling={friendActions.isCancelingFor(fid)}
        onAccept={() => {
          if (row.status?.kind === 'incoming' && row.status.requestId) {
            onAcceptRequest({ id: row.status.requestId, user: u });
          }
        }}
        onDecline={() => {
          if (row.status?.kind === 'incoming' && row.status.requestId) {
            onDeclineRequest({ id: row.status.requestId, user: u });
          }
        }}
        acceptDisabled={acceptDisabled}
        incomingMode="actions"
        testID={u.id ? `friend-search-${u.id}` : undefined}
      />
    );
  }
  // Presence is friends-only — show the dot for friend rows so a
  // search hit reads like the section list. Strangers / pending /
  // self leave it hidden because their presence isn't subscribed.
  const isFriend = row.status?.kind === 'friend';
  // Tapping a friend hit opens the DM, mirroring the section
  // list's row-tap behaviour. Non-friends, pending requests, and
  // self leave the row tap-disabled — actions live in the trailing
  // affordance for those cases.
  const userId = u.id;
  const onPress = isFriend && userId ? () => onOpenDM(userId) : undefined;
  return (
    <FriendRow
      displayName={u.display_name}
      username={u.username}
      avatarUrl={u.avatar_url}
      presence={isFriend ? row.presence : undefined}
      hidePresence={!isFriend}
      onPress={onPress}
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
  friendActions,
  menuOpen,
  onToggleSection,
  onExpandRequestSection,
}: {
  row: Row;
  pendingAction: Set<string>;
  onAccept: (f: Friendship) => void;
  onDecline: (f: Friendship) => void;
  onOpenMenu: (u: UserRow) => void;
  onOpenDM: (friendUserId: string) => void;
  friendActions: ReturnType<typeof useFriendActions>;
  menuOpen: boolean;
  onToggleSection: (id: SectionId) => void;
  onExpandRequestSection: (section: 'incoming' | 'outgoing') => void;
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
    case 'show-all':
      return <ShowAllRow label={row.label} onPress={() => onExpandRequestSection(row.section)} />;
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
            u ? (
              <View className="flex-row items-center gap-2">
                <RelationshipBadge label="Friend" />
                <FriendRowMenuButton disabled={inFlight} onPress={() => onOpenMenu(u)} />
              </View>
            ) : undefined
          }
        />
      );
    }
    case 'request': {
      const u = row.friendship.user;
      const fid = row.friendship.id;
      const inFlight = fid ? pendingAction.has(fid) : false;
      // Same trailing affordance as the global search modal —
      // FriendStatusAction renders the right pill per status.
      // Friends-tab incoming requests use the icon-button pair
      // (accept/decline both inline) since this is THE surface
      // for resolving requests; the search modal uses a hint.
      const status: FriendStatus | undefined = fid
        ? row.direction === 'incoming'
          ? { kind: 'incoming', requestId: fid }
          : { kind: 'outgoing', requestId: fid }
        : undefined;
      // "Added" badge for outgoing pending rows so the section
      // list reads with the same vocabulary as the search hits.
      // Incoming rows already make their state obvious through
      // the inline accept/decline icon pair, so they don't get a
      // badge.
      const showAddedBadge = row.direction === 'outgoing';
      return (
        <FriendRow
          displayName={u?.display_name}
          username={u?.username}
          avatarUrl={u?.avatar_url}
          hidePresence
          trailing={
            <View className="flex-row items-center gap-2">
              {showAddedBadge ? <RelationshipBadge label="Added" /> : null}
              <FriendStatusAction
                status={status}
                username={u?.username}
                // Outgoing rows are the only state where Add fires;
                // these rows are pending requests so the username
                // path is dead. Wire to the shared hook for safety.
                onAdd={friendActions.sendFriendRequest}
                onCancel={friendActions.cancelFriendRequest}
                isAdding={friendActions.isAddingFor(u?.username)}
                isCanceling={friendActions.isCancelingFor(fid)}
                onAccept={() => fid && onAccept(row.friendship)}
                onDecline={() => fid && onDecline(row.friendship)}
                acceptDisabled={inFlight}
                incomingMode="actions"
                testID={fid ? `friend-request-${fid}` : undefined}
              />
            </View>
          }
        />
      );
    }
  }
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
      // Opaque background — sticky-header bleed-through let the
      // user see avatar rows underneath the chevron strip while
      // scrolling. `bg-card` matches the chrome around the modal
      // and keeps the slight elevation read.
      className="flex-row items-center gap-2 border-b border-border bg-card px-4 py-2 active:bg-muted">
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

// Tap target appended to a request section that's been truncated
// to VISIBLE_REQUESTS rows. Click promotes the section into the
// expanded set and the rest of the rows render in place.
// Mirrors the global-search modal's <ShowAllRow> visual.
function ShowAllRow({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <View className="px-4 py-2">
      <Button size="sm" variant="ghost" onPress={onPress} accessibilityLabel={label}>
        <Text className="text-primary">{label}</Text>
      </Button>
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
