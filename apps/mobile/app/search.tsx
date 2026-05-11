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
import { useFocusEffect, useRouter } from 'expo-router';
import {
  ChevronDown,
  ChevronRight,
  ConciergeBell,
  MessageCircle,
  Search,
  Users as UsersIcon,
  X,
} from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Platform, Pressable, View } from 'react-native';
import { FullWindowOverlay } from 'react-native-screens';

import { useQueryClient } from '@tanstack/react-query';

import { ConversationActionMenu } from '@/components/conversation-action-menu';
import { ConversationRow } from '@/components/conversation-row';
import { FriendActionMenu, FriendRowMenuButton } from '@/components/friend-action-menu';
import { FriendRow } from '@/components/friend-row';
import { FriendStatusAction, type FriendStatus } from '@/components/friend-status-action';
import { RelationshipBadge } from '@/components/relationship-badge';
import { MuteSheet } from '@/components/mute-sheet';
import { Toast, toastConfig } from '@/components/toast-config';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List, type ListRef } from '@/components/ui/list';
import { ModalScreenShell } from '@/components/ui/modal-screen-shell';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1FriendsQueryKey,
  getGetV1FriendsRequestsQueryKey,
  useDeleteV1FriendsUserId,
  useGetV1FriendsRequests,
  usePostV1FriendsRequestsIdAccept,
  usePostV1FriendsUserIdBlock,
} from '@/lib/api/hooks/friends/friends';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import { useGetV1Search } from '@/lib/api/hooks/search/search';
import { useFriendActions } from '@/lib/api/use-friend-actions';
import {
  flatten,
  useInfiniteConversations,
  useInfiniteFriends,
  useInfiniteUsers,
} from '@/lib/api/use-infinite';
import { haptics } from '@/lib/haptics';
import { useConversationPinMute } from '@/lib/use-conversation-pin-mute';
import { useLeaveConversation } from '@/lib/use-conversation-leave';
import type {
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpFriendRequestsResponse,
  InternalHandlerHttpFriendshipResponse,
  InternalHandlerHttpPresenceListResponse,
  InternalHandlerHttpSearchConversationRow,
  InternalHandlerHttpSearchMessageRow,
  InternalHandlerHttpSearchResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useEnsureDirectConversation } from '@/lib/api/use-ensure-direct-conversation';
import { conversationDisplay, isCurrentlyMuted } from '@/lib/conversation-display';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

const DEBOUNCE_MS = 200;
const MIN_CHARS = 2;

type SectionId = 'users' | 'conversations' | 'messages';

type Row =
  | {
      kind: 'header';
      key: string;
      title: string;
      count: number;
      section: SectionId;
      collapsed: boolean;
    }
  | { kind: 'user'; key: string; user: InternalHandlerHttpUserResponse }
  | { kind: 'conversation'; key: string; conversation: InternalHandlerHttpSearchConversationRow }
  | { kind: 'message'; key: string; message: InternalHandlerHttpSearchMessageRow }
  // `more` is the absolute remaining count for screen-reader and
  // analytics. The visible label is rendered from total, but a11y
  // wants the delta phrased as "X more results below."
  | { kind: 'show-all'; key: string; section: SectionId; label: string; more: number };

// Top-N each section truncates to before showing a "Show all" row.
// Backend caps the unified-search response at 10 per section, so
// expanding the section reveals at most 10 — true drill-downs into
// the per-section endpoints (>10 results) are out of scope until
// the user actually has that many friends/groups/messages to find.
const VISIBLE_PER_SECTION = 5;

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

  // Unified search drives only the conversations + messages
  // sections — those endpoints don't have a per-section paginated
  // counterpart, so the unified 10-cap is what we render. People
  // search has its own paginated endpoint (/v1/users), and
  // splitting it out lets the tier sort run across the FULL
  // matching set instead of the unified-search top-10 (which is
  // ranked by trigram score and may not include any friends).
  const searchQ = useGetV1Search(
    { q: debouncedQuery, types: 'conversations,messages' },
    { query: { enabled, staleTime: 30_000 } }
  );
  const data = searchQ.data as InternalHandlerHttpSearchResponse | undefined;

  // The search response only carries a slim {id, name, avatar_url,
  // last_message_at, type} per conversation hit — no members, no
  // muted/pinned. Pull the full row out of the conversations-list
  // cache so the search modal can render the same StackedAvatars +
  // member-count subtitle + presence dots the chats tab does.
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;
  const presenceQ = useGetV1PresenceFriends({ query: { staleTime: 15_000 } });
  const presenceData = presenceQ.data as InternalHandlerHttpPresenceListResponse | undefined;
  const presenceByUser = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const p of presenceData?.data ?? []) {
      if (p.user_id && p.status) m.set(p.user_id, p.status);
    }
    return m;
  }, [presenceData]);

  // Hydrate the conversations infinite-query cache so a search hit
  // for a chat the user just scrolled past in the chats tab can
  // render the same StackedAvatars + member-count subtitle without
  // re-fetching. The chats tab is the ONLY producer of this cache;
  // when it hasn't been visited yet, the map stays empty and each
  // ConversationRow falls back to the slim search-row payload below.
  const fullConversationsQ = useInfiniteConversations({
    query: { staleTime: 30_000 },
  });
  const fullConversationById = React.useMemo(() => {
    const m = new Map<string, InternalHandlerHttpConversationResponse>();
    const { data: convs } = flatten<
      InternalHandlerHttpConversationResponse,
      { data?: InternalHandlerHttpConversationResponse[] }
    >(fullConversationsQ.data?.pages);
    for (const c of convs) {
      if (c.id) m.set(c.id, c);
    }
    return m;
  }, [fullConversationsQ.data]);

  const ensureDM = useEnsureDirectConversation();
  const [openingFor, setOpeningFor] = React.useState<string | null>(null);

  // Friend graph for the current user. Read-only; we don't gate
  // the search on it (results still show for non-friends), but
  // we use it to decide whether each user-section row gets a
  // "tap to message" action, an "Add friend" button, or an
  // "Unsend" button for an outgoing request. The infinite-query
  // cache shares with the friends tab so we don't re-fetch.
  const friendsQ = useInfiniteFriends({ query: { staleTime: 30_000 } });
  const requestsQ = useGetV1FriendsRequests({ query: { staleTime: 30_000 } });
  const requestsData = requestsQ.data as InternalHandlerHttpFriendRequestsResponse | undefined;

  // Map<userId, FriendStatus> built once per render so each row
  // can look up its peer in O(1). Outgoing requests carry the
  // friendship.id so the unsend button can DELETE the right row.
  const friendStatusByUser = React.useMemo(() => {
    const m = new Map<string, FriendStatus>();
    const { data: friends } = flatten<
      InternalHandlerHttpFriendshipResponse,
      { data?: InternalHandlerHttpFriendshipResponse[] }
    >(friendsQ.data?.pages);
    for (const f of friends) {
      if (f.user?.id) m.set(f.user.id, { kind: 'friend' });
    }
    for (const r of requestsData?.outgoing ?? []) {
      if (r.user?.id && r.id) m.set(r.user.id, { kind: 'outgoing', requestId: r.id });
    }
    for (const r of requestsData?.incoming ?? []) {
      if (r.user?.id && r.id) m.set(r.user.id, { kind: 'incoming', requestId: r.id });
    }
    return m;
  }, [friendsQ.data, requestsData]);

  // Send + cancel friend-request actions live in the shared
  // useFriendActions hook so cache invalidation + toast vocab
  // match the friends tab. The toasts are mounted inside this
  // screen via <FullWindowOverlay> below so they render on top
  // of the iOS modal chrome.
  const friendActions = useFriendActions();

  // Friend "more actions" menu — Unfriend / Block. Mirrors the
  // friends-tab implementation so a user can manage relationships
  // straight from a search hit.
  const qc = useQueryClient();
  const unfriend = useDeleteV1FriendsUserId();
  const blockUser = usePostV1FriendsUserIdBlock();
  const acceptRequest = usePostV1FriendsRequestsIdAccept();
  const [friendMenuTarget, setFriendMenuTarget] =
    React.useState<InternalHandlerHttpUserResponse | null>(null);
  const [pendingFriendAction, setPendingFriendAction] = React.useState<Set<string>>(new Set());
  const markFriendPending = React.useCallback((id: string) => {
    setPendingFriendAction((prev) => {
      const next = new Set(prev);
      next.add(id);
      return next;
    });
  }, []);
  const unmarkFriendPending = React.useCallback((id: string) => {
    setPendingFriendAction((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);
  const invalidateRelationships = React.useCallback(async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: getGetV1FriendsQueryKey() }),
      qc.invalidateQueries({ queryKey: getGetV1FriendsRequestsQueryKey() }),
    ]);
  }, [qc]);
  const onUnfriend = React.useCallback(
    async (user: InternalHandlerHttpUserResponse) => {
      const userId = user.id;
      if (!userId) return;
      markFriendPending(userId);
      setFriendMenuTarget(null);
      try {
        await unfriend.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = user.username ? `@${user.username}` : 'this user';
        toast.info('Unfriended', `${handle} is no longer in your friends.`);
      } catch (err) {
        const msg =
          err instanceof APIError && err.message ? err.message : "Couldn't unfriend right now.";
        toast.error(msg);
      } finally {
        unmarkFriendPending(userId);
      }
    },
    [unfriend, invalidateRelationships, markFriendPending, unmarkFriendPending]
  );
  const onBlock = React.useCallback(
    async (user: InternalHandlerHttpUserResponse) => {
      const userId = user.id;
      if (!userId) return;
      markFriendPending(userId);
      setFriendMenuTarget(null);
      try {
        await blockUser.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = user.username ? `@${user.username}` : 'this user';
        toast.info('Blocked', `${handle} can't message or add you.`);
      } catch (err) {
        const msg =
          err instanceof APIError && err.message ? err.message : "Couldn't block right now.";
        toast.error(msg);
      } finally {
        unmarkFriendPending(userId);
      }
    },
    [blockUser, invalidateRelationships, markFriendPending, unmarkFriendPending]
  );

  // Accept/decline a pending incoming request. Wired into the
  // keyboard activation path (Enter on a pending row in the
  // People section fires Accept) and the row-tap on incoming
  // rows that surface the trailing icon pair.
  const onAcceptIncoming = React.useCallback(
    async (requestId: string, handle: string | undefined) => {
      try {
        await acceptRequest.mutateAsync({ id: requestId });
        await invalidateRelationships();
        toast.success("You're now friends", handle ? `Say hi to ${handle}.` : undefined);
      } catch (err) {
        const msg =
          err instanceof APIError && err.message
            ? err.message
            : "Couldn't accept this request right now.";
        toast.error(msg);
      }
    },
    [acceptRequest, invalidateRelationships]
  );

  // Conversation "more actions" — Pin / Mute. Reuses the same
  // optimistic mutation hook the chats tab uses so a long-press +
  // pin from a search hit lands the same row swap on both surfaces.
  const [activeConvAction, setActiveConvAction] = React.useState<{
    id: string;
    title: string;
    isPinned: boolean;
    isMuted: boolean;
    isGroup: boolean;
    screen: 'menu' | 'mute';
  } | null>(null);
  const closeConvMenu = React.useCallback(() => setActiveConvAction(null), []);
  const openConvMute = React.useCallback(
    () => setActiveConvAction((s) => (s ? { ...s, screen: 'mute' } : s)),
    []
  );
  const { togglePin, setMute, unmute } = useConversationPinMute();
  const { leave: leaveConv } = useLeaveConversation();

  const goCancel = React.useCallback(() => {
    if (router.canGoBack()) router.back();
    else router.replace('/');
  }, [router]);

  // Tapping a result should land on the destination as a page in
  // the main nav stack — not push a second pane inside this modal.
  // Dismiss the modal first, then navigate; setTimeout(0) lets the
  // dismiss animation register before the push fires.
  const dismissThenGoToConversation = React.useCallback(
    (conversationId: string) => {
      if (router.canGoBack()) router.back();
      setTimeout(() => router.push(`/conversations/${conversationId}`), 0);
    },
    [router]
  );

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
        dismissThenGoToConversation(conversationId);
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
    [ensureDM, dismissThenGoToConversation, openingFor]
  );

  // Per-section expansion state. Default = collapsed (top
  // VISIBLE_PER_SECTION shown). Tapping a "Show all N" row promotes
  // its section here and the row recomputes to render the rest.
  // Reset on every new query so a section that was expanded for
  // one search doesn't carry over to the next.
  const [expandedSections, setExpandedSections] = React.useState<Set<SectionId>>(new Set());
  // Section collapse — separate from `expandedSections` (which is
  // about the truncate-to-5 cap). Tapping the chevron header
  // hides every row in the section without losing its state, so
  // the user can scan the other sections without scrolling past
  // long results. Reset on every new query alongside the expand
  // state so a fresh search starts everything visible.
  const [collapsedSections, setCollapsedSections] = React.useState<Set<SectionId>>(new Set());
  React.useEffect(() => {
    setExpandedSections(new Set());
    setCollapsedSections(new Set());
  }, [debouncedQuery]);
  // After "Show all N" expands a section, the post-render effect
  // below scrolls that section's header to the top of the viewport
  // so the newly-revealed rows are visible without the user having
  // to hunt for them.
  const justExpandedRef = React.useRef<SectionId | null>(null);
  const expandSection = React.useCallback((section: SectionId) => {
    setExpandedSections((prev) => {
      if (prev.has(section)) return prev;
      const next = new Set(prev);
      next.add(section);
      return next;
    });
    justExpandedRef.current = section;
  }, []);
  const toggleSection = React.useCallback((section: SectionId) => {
    setCollapsedSections((prev) => {
      const next = new Set(prev);
      if (next.has(section)) {
        next.delete(section);
      } else {
        next.add(section);
        // Collapsing also drops the show-all expansion for the
        // section. Two reasons:
        //   1. UX: re-opening the chevron should reset to the
        //      cap-5 + "Show all N" affordance, not silently
        //      restore a 1000-row drill-down state.
        //   2. Correctness: when usersExpanded stays true behind
        //      a collapsed section, the rows under it aren't
        //      rendered but the FlashList is suddenly very short,
        //      so onEndReached keeps firing fetchNextPage on
        //      every render and the drill-down query cascades
        //      every page in the background.
        setExpandedSections((prevExp) => {
          if (!prevExp.has(section)) return prevExp;
          const nextExp = new Set(prevExp);
          nextExp.delete(section);
          return nextExp;
        });
      }
      return next;
    });
  }, []);

  // People search runs against /v1/users from the moment the user
  // types — the friend-tier sort needs the FULL matching set to
  // place friends first (the unified-search top-10 is ranked by
  // trigram score and often returns zero friends). Drilling for
  // more pages just keeps appending rows below; the cap-5 default
  // happens client-side after the sort.
  const usersExpanded = expandedSections.has('users');
  const conversationsTotal = data?.conversations_total ?? data?.conversations?.length ?? 0;
  const messagesTotal = data?.messages_total ?? data?.messages?.length ?? 0;
  const usersDrillQ = useInfiniteUsers(
    { q: debouncedQuery },
    {
      query: { enabled, staleTime: 30_000 },
    }
  );
  const { data: drilledUsers, total: usersTotal } = React.useMemo(
    () =>
      flatten<InternalHandlerHttpUserResponse, { data?: InternalHandlerHttpUserResponse[] }>(
        usersDrillQ.data?.pages
      ),
    [usersDrillQ.data]
  );

  const rows = React.useMemo<Row[]>(
    () =>
      buildRows({
        data,
        usersTotal,
        conversationsTotal,
        messagesTotal,
        drilledUsers,
        usersExpanded,
        expanded: expandedSections,
        collapsed: collapsedSections,
      }),
    [
      data,
      usersTotal,
      conversationsTotal,
      messagesTotal,
      usersExpanded,
      drilledUsers,
      expandedSections,
      collapsedSections,
    ]
  );

  // Indices of tappable rows (skip headers — they're not actions).
  // Keyboard nav cycles only through these, but the visual focus
  // index is into `rows` so the highlighted row matches what the
  // user sees on screen.
  const tappableRowIndices = React.useMemo(() => {
    const out: number[] = [];
    rows.forEach((r, i) => {
      if (isTappableRow(r, friendStatusByUser, me?.id)) out.push(i);
    });
    return out;
  }, [rows, friendStatusByUser, me?.id]);

  const [focusedRowIdx, setFocusedRowIdx] = React.useState<number | null>(null);
  // When results change, keep focus on the same row if it's still
  // tappable; otherwise snap to the first tappable row. The earlier
  // "always reset to first tappable" combined with the
  // scrollToIndex effect below caused a nasty auto-scroll on
  // expand: 1000 stranger users (non-tappable) followed by a group
  // chat (tappable) meant tappableRowIndices[0] was the group, and
  // FlashList scrolled the whole modal down to it.
  React.useEffect(() => {
    setFocusedRowIdx((prev) => {
      if (prev != null && tappableRowIndices.includes(prev)) return prev;
      return tappableRowIndices[0] ?? null;
    });
  }, [tappableRowIndices]);

  // Activate the row at a given index (Enter, or programmatic).
  // Each row kind has a "primary action" — Enter does what the
  // most-prominent visual affordance on that row would do:
  //
  //   header           → toggle the chevron (expand/collapse)
  //   user (friend)    → open DM
  //   user (outgoing)  → unsend the pending request
  //   user (incoming)  → accept the request
  //   user (none)      → send a friend request
  //   conversation     → open the thread
  //   message          → open the parent conversation
  //   show-all         → expand the section
  const activateRow = React.useCallback(
    (rowIdx: number | null) => {
      if (rowIdx == null) return;
      const row = rows[rowIdx];
      if (!row) return;
      if (row.kind === 'header') {
        toggleSection(row.section);
        return;
      }
      if (row.kind === 'user') {
        const u = row.user;
        if (!u.id) return;
        if (me?.id && u.id === me.id) return;
        const status = friendStatusByUser.get(u.id);
        if (status?.kind === 'friend') {
          void onTapUser(u);
        } else if (status?.kind === 'outgoing') {
          friendActions.cancelFriendRequest(status.requestId);
        } else if (status?.kind === 'incoming') {
          void onAcceptIncoming(
            status.requestId,
            u.username ? `@${u.username}` : (u.display_name ?? undefined)
          );
        } else if (u.username) {
          friendActions.sendFriendRequest(u.username);
        }
        return;
      }
      if (row.kind === 'conversation' && row.conversation.id) {
        dismissThenGoToConversation(row.conversation.id);
        return;
      }
      if (row.kind === 'message' && row.message.conversation_id) {
        dismissThenGoToConversation(row.message.conversation_id);
        return;
      }
      if (row.kind === 'show-all') {
        expandSection(row.section);
      }
    },
    [
      rows,
      me?.id,
      friendStatusByUser,
      friendActions,
      onAcceptIncoming,
      onTapUser,
      dismissThenGoToConversation,
      expandSection,
      toggleSection,
    ]
  );

  // Web-only keyboard nav: ↑/↓ cycles tappable rows, Enter activates.
  // Listener is on capture phase so a focused TextInput in the
  // header can't swallow the events. Native gets nothing — touch
  // UX uses tap-to-select directly. The scrollToIndex call lives
  // INSIDE this handler (not in a useEffect on focusedRowIdx) so
  // only user-driven arrow presses cause the viewport to follow —
  // a data-change-driven focus reset never scrolls the list.
  const listRef = React.useRef<ListRef<Row>>(null);
  React.useEffect(() => {
    if (Platform.OS !== 'web' || tappableRowIndices.length === 0) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setFocusedRowIdx((prev) => {
          const next = stepFocus(prev, tappableRowIndices, 1);
          if (next != null) {
            listRef.current?.scrollToIndex({ index: next, viewPosition: 0.5 });
          }
          return next;
        });
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setFocusedRowIdx((prev) => {
          const next = stepFocus(prev, tappableRowIndices, -1);
          if (next != null) {
            listRef.current?.scrollToIndex({ index: next, viewPosition: 0.5 });
          }
          return next;
        });
      } else if (e.key === 'Enter') {
        e.preventDefault();
        activateRow(focusedRowIdx);
      }
    };
    window.addEventListener('keydown', handler, { capture: true });
    return () => window.removeEventListener('keydown', handler, { capture: true });
  }, [tappableRowIndices, focusedRowIdx, activateRow]);

  const mutedFg = useThemeColor('muted-foreground');

  // After "Show all N" lands, scroll the just-expanded section's
  // header to the top of the viewport so the newly-revealed rows
  // are visible without the user having to hunt for them. The
  // ref is cleared unconditionally so a stale value can't fire
  // after a re-render.
  React.useEffect(() => {
    const section = justExpandedRef.current;
    justExpandedRef.current = null;
    if (!section) return;
    const idx = rows.findIndex((r) => r.kind === 'header' && r.section === section);
    if (idx < 0) return;
    listRef.current?.scrollToIndex({ index: idx, animated: true, viewPosition: 0 });
  }, [rows]);

  // FlashList 2.0.2 stickyHeaderIndices paints the sticky overlay
  // on top of the inline header at scrollY=0, doubling the first
  // section header. Gate sticky behind a small scroll so the
  // inline header has left the viewport before the overlay takes
  // over.
  const [stickyEnabled, setStickyEnabled] = React.useState(false);
  const stickyHeaderIndices = React.useMemo(() => {
    if (!stickyEnabled) return undefined;
    return rows.map((r, i) => (r.kind === 'header' ? i : -1)).filter((i) => i >= 0);
  }, [stickyEnabled, rows]);

  return (
    <ModalScreenShell onClose={goCancel} testID="search-modal-shell">
      <View className="flex-1 bg-background">
        <ModalHeader value={rawQuery} onChange={setRawQuery} onCancel={goCancel} />

        {!enabled ? (
          <SearchHint />
        ) : // Block on the friend graph load too — without it, user
        // hits render in stranger order, then jump-reorder once
        // friendsQ lands. The graph is shared with the friends-tab
        // cache (staleTime 30s), so this gate only fires the FIRST
        // time the modal mounts in a fresh session.
        (searchQ.isFetching || usersDrillQ.isFetching || friendsQ.isLoading) &&
          rows.length === 0 ? (
          <SearchLoading />
        ) : (searchQ.isError || usersDrillQ.isError) && rows.length === 0 ? (
          <SearchError
            onRetry={() => {
              void searchQ.refetch();
              void usersDrillQ.refetch();
            }}
          />
        ) : rows.length === 0 ? (
          <SearchNoResults />
        ) : (
          <List
            ref={listRef}
            data={rows}
            keyExtractor={(item) => item.key}
            // FlashList recycles view instances across rows in the
            // same pool. Without an explicit type-tag, a 'header'
            // slot could be reused for a 'user' row on scroll and
            // show stale text. Tagging by `kind` keeps headers,
            // users, conversations, messages, and show-all rows in
            // separate pools so each renders with the right shape.
            getItemType={(item) => item.kind}
            // FlashList v2's default maintainVisibleContentPosition
            // anchors to phantom prior content on first paint and
            // can land the modal scrolled mid-list. Force it off
            // and pin initial render to index 0.
            maintainVisibleContentPosition={{ disabled: true }}
            initialScrollIndex={0}
            // Sticky chevron headers — gated behind a small scroll
            // so the inline header has left the viewport before
            // the overlay takes over (FlashList 2.0.2 duplicate
            // bug). undefined disables sticky entirely.
            stickyHeaderIndices={stickyHeaderIndices}
            scrollEventThrottle={32}
            onScroll={(e) => {
              const y = e.nativeEvent.contentOffset.y;
              const next = y > 50;
              if (next !== stickyEnabled) setStickyEnabled(next);
            }}
            onEndReachedThreshold={0.5}
            onEndReached={() => {
              if (!usersExpanded) return;
              if (!usersDrillQ.hasNextPage || usersDrillQ.isFetchingNextPage) return;
              void usersDrillQ.fetchNextPage();
            }}
            ListFooterComponent={
              usersExpanded && usersDrillQ.isFetchingNextPage ? (
                <View className="items-center py-4">
                  <ActivityIndicator color={mutedFg} />
                </View>
              ) : null
            }
            renderItem={({ item, index }) => (
              <RenderedRow
                row={item}
                isFocused={index === focusedRowIdx}
                onTapUser={onTapUser}
                openingForUserId={openingFor}
                fullConversationById={fullConversationById}
                myUserId={me?.id}
                presenceByUser={presenceByUser}
                friendStatusByUser={friendStatusByUser}
                friendActions={friendActions}
                onOpenConversation={dismissThenGoToConversation}
                onExpandSection={expandSection}
                onToggleSection={toggleSection}
                onOpenFriendMenu={setFriendMenuTarget}
                onOpenConvMenu={(c) =>
                  setActiveConvAction({
                    id: c.id,
                    title: c.title,
                    isPinned: c.isPinned,
                    isMuted: c.isMuted,
                    isGroup: c.isGroup,
                    screen: 'menu',
                  })
                }
                pendingFriendAction={pendingFriendAction}
              />
            )}
          />
        )}
      </View>

      {/* Friend "more actions" sheet — opens for friend rows that
          surface the trailing 3-dots button. Same vocabulary as the
          friends tab: Unfriend / Block. */}
      <FriendActionMenu
        target={friendMenuTarget}
        pendingAction={pendingFriendAction}
        onClose={() => setFriendMenuTarget(null)}
        onUnfriend={onUnfriend}
        onBlock={onBlock}
      />
      {/* Conversation pin / mute menu — same primitives the chats
          tab uses, so a search-modal pin lands the same optimistic
          row swap. */}
      <ConversationActionMenu
        visible={activeConvAction?.screen === 'menu'}
        title={activeConvAction?.title ?? ''}
        isPinned={activeConvAction?.isPinned ?? false}
        isMuted={activeConvAction?.isMuted ?? false}
        isGroup={activeConvAction?.isGroup ?? false}
        onTogglePin={() => {
          if (!activeConvAction) return;
          togglePin(activeConvAction.id, activeConvAction.isPinned);
          closeConvMenu();
        }}
        onMutePress={openConvMute}
        onUnmute={() => {
          if (!activeConvAction) return;
          unmute(activeConvAction.id);
          closeConvMenu();
        }}
        onManageMembers={() => {
          if (!activeConvAction) return;
          const id = activeConvAction.id;
          closeConvMenu();
          // Dismiss the search modal first so the manage-members
          // screen lands on the chats nav stack, not stacked on a
          // closing modal. setTimeout lets the sheet finish its
          // dismiss animation before the push fires.
          if (router.canGoBack()) router.back();
          setTimeout(() => router.push(`/conversations/${id}/members`), 0);
        }}
        onLeave={() => {
          if (!activeConvAction) return;
          const id = activeConvAction.id;
          closeConvMenu();
          void leaveConv(id);
        }}
        onClose={closeConvMenu}
        testID="search-conv-action-menu"
      />
      <MuteSheet
        visible={activeConvAction?.screen === 'mute'}
        isMuted={activeConvAction?.isMuted ?? false}
        onPickUntil={(until) => {
          if (!activeConvAction) return;
          setMute(activeConvAction.id, until);
          closeConvMenu();
        }}
        onUnmute={() => {
          if (!activeConvAction) return;
          unmute(activeConvAction.id);
          closeConvMenu();
        }}
        onClose={closeConvMenu}
        testID="search-mute-sheet"
      />

      {/* iOS native Modal hides the root <ToastRoot> behind the
          modal chrome. <FullWindowOverlay> from react-native-
          screens mounts a UIView at the WINDOW level — it sits
          above any modal — so a Toast rendered inside it pops
          on top of the search drawer. iOS-only because the
          overlay primitive is a no-op on Android, and web
          handles toasts via sonner+portal. */}
      {Platform.OS === 'ios' ? (
        <FullWindowOverlay>
          <Toast config={toastConfig} topOffset={60} />
        </FullWindowOverlay>
      ) : null}
    </ModalScreenShell>
  );
}

function isTappableRow(
  r: Row,
  friendStatusByUser: Map<string, FriendStatus>,
  myUserId: string | undefined
): boolean {
  // Section headers are keyboard-targetable so ↑/↓ can land on
  // them and Enter toggles the chevron (expand/collapse). Without
  // this, arrowing past a non-tappable People section full of
  // strangers landed on the Chats header below — and Enter did
  // nothing useful there. Now the header itself is the activation
  // surface.
  if (r.kind === 'header') return true;
  if (r.kind === 'user') {
    // Every non-self user row is keyboard-focusable; the
    // activation routes by status (friend → DM, outgoing →
    // unsend, incoming → accept, none → add). Strangers need a
    // username because the Add endpoint targets one — skip rows
    // without it.
    if (!r.user.id) return false;
    if (myUserId && r.user.id === myUserId) return false;
    const status = friendStatusByUser.get(r.user.id);
    if (status?.kind === 'friend') return true;
    if (status?.kind === 'outgoing' || status?.kind === 'incoming') return true;
    return r.user.username != null;
  }
  if (r.kind === 'conversation') return !!r.conversation.id;
  if (r.kind === 'message') return !!r.message.conversation_id;
  if (r.kind === 'show-all') return true;
  return false;
}

function stepFocus(
  current: number | null,
  tappableIndices: number[],
  delta: 1 | -1
): number | null {
  if (tappableIndices.length === 0) return null;
  // Find where `current` sits inside tappableIndices; if not on a
  // tappable row (e.g. a header was focused — shouldn't happen
  // post-effect, but defensive), step from the nearest one.
  const cursor = current == null ? -1 : tappableIndices.indexOf(current);
  if (cursor < 0) return tappableIndices[delta > 0 ? 0 : tappableIndices.length - 1];
  const next = Math.max(0, Math.min(tappableIndices.length - 1, cursor + delta));
  return tappableIndices[next];
}

// Backend orders /v1/users by (friend → pending → stranger) tier, then
// by (created_at DESC, id DESC) within each tier — see the SQL in
// apps/backend/internal/repository/user/repo.go. The client no longer
// re-sorts; an earlier client-side tier sort couldn't reach friends
// that lived past the first page, and removing it keeps the pagination
// cursor coherent (the cursor encodes tier on the backend side now).

function buildRows({
  data,
  usersTotal,
  conversationsTotal,
  messagesTotal,
  drilledUsers,
  usersExpanded,
  expanded,
  collapsed,
}: {
  data: InternalHandlerHttpSearchResponse | undefined;
  usersTotal: number;
  conversationsTotal: number;
  messagesTotal: number;
  // /v1/users-paginated user matches. Already ordered
  // friends → pending → strangers by the backend's rel_tier
  // ranking; the client renders the slice as-is.
  drilledUsers: InternalHandlerHttpUserResponse[];
  // True once the user taps "Show all N people"; the modal then
  // renders every loaded page and lets the FlashList drive
  // fetchNextPage on scroll.
  usersExpanded: boolean;
  expanded: Set<SectionId>;
  collapsed: Set<SectionId>;
}): Row[] {
  const out: Row[] = [];

  // People section runs against /v1/users from the start so the
  // first page already contains the caller's friends — no client
  // sort needed.
  // drilledUsers already arrives ordered friends → pending → strangers
  // per the backend's rel_tier ranking — no client sort needed.
  const renderedUsers = usersExpanded ? drilledUsers : drilledUsers.slice(0, VISIBLE_PER_SECTION);
  if (usersTotal > 0 || drilledUsers.length > 0) {
    const isCollapsed = collapsed.has('users');
    out.push({
      kind: 'header',
      key: 'h:users',
      title: 'People',
      count: usersTotal,
      section: 'users',
      collapsed: isCollapsed,
    });
    if (!isCollapsed) {
      renderedUsers.forEach((u, i) => {
        out.push({ kind: 'user', key: `user:${u.id ?? `idx-${i}`}`, user: u });
      });
      // Show-all label uses the absolute total — the user wants
      // "Show all 1000 people," not "Show 5 more" when there are
      // 1000 matches behind the unified-search cap.
      if (!usersExpanded && usersTotal > renderedUsers.length) {
        const more = usersTotal - renderedUsers.length;
        out.push({
          kind: 'show-all',
          key: 'show-all:users',
          section: 'users',
          label: `Show all ${usersTotal} ${usersTotal === 1 ? 'person' : 'people'}`,
          more,
        });
      }
    }
  }

  const conversations = data?.conversations ?? [];
  if (conversationsTotal > 0 || conversations.length > 0) {
    const isCollapsed = collapsed.has('conversations');
    const showAll = expanded.has('conversations');
    const visible = showAll ? conversations : conversations.slice(0, VISIBLE_PER_SECTION);
    out.push({
      kind: 'header',
      key: 'h:conversations',
      title: 'Chats',
      count: conversationsTotal,
      section: 'conversations',
      collapsed: isCollapsed,
    });
    if (!isCollapsed) {
      visible.forEach((c, i) => {
        out.push({
          kind: 'conversation',
          key: `conv:${c.id ?? `idx-${i}`}`,
          conversation: c,
        });
      });
      // /v1/conversations doesn't take a `q` filter, so we can't
      // drill past the unified-search 10-cap for chats. The label
      // still reads "Show N more" relative to the visible slice
      // and reveals all 10 when tapped.
      if (!showAll && conversations.length > VISIBLE_PER_SECTION) {
        const more = conversations.length - VISIBLE_PER_SECTION;
        out.push({
          kind: 'show-all',
          key: 'show-all:conversations',
          section: 'conversations',
          label: `Show ${more} more ${more === 1 ? 'chat' : 'chats'}`,
          more,
        });
      }
    }
  }

  const messages = data?.messages ?? [];
  if (messagesTotal > 0 || messages.length > 0) {
    const isCollapsed = collapsed.has('messages');
    const showAll = expanded.has('messages');
    const visible = showAll ? messages : messages.slice(0, VISIBLE_PER_SECTION);
    out.push({
      kind: 'header',
      key: 'h:messages',
      title: 'Messages',
      count: messagesTotal,
      section: 'messages',
      collapsed: isCollapsed,
    });
    if (!isCollapsed) {
      visible.forEach((m, i) => {
        out.push({
          kind: 'message',
          key: `msg:${m.id ?? `idx-${i}`}`,
          message: m,
        });
      });
      if (!showAll && messages.length > VISIBLE_PER_SECTION) {
        const more = messages.length - VISIBLE_PER_SECTION;
        out.push({
          kind: 'show-all',
          key: 'show-all:messages',
          section: 'messages',
          label: `Show ${more} more ${more === 1 ? 'message' : 'messages'}`,
          more,
        });
      }
    }
  }

  return out;
}

function RenderedRow({
  row,
  isFocused,
  onTapUser,
  openingForUserId,
  fullConversationById,
  myUserId,
  presenceByUser,
  friendStatusByUser,
  friendActions,
  onOpenConversation,
  onExpandSection,
  onToggleSection,
  onOpenFriendMenu,
  onOpenConvMenu,
  pendingFriendAction,
}: {
  row: Row;
  isFocused: boolean;
  onTapUser: (u: InternalHandlerHttpUserResponse) => void;
  openingForUserId: string | null;
  fullConversationById: Map<string, InternalHandlerHttpConversationResponse>;
  myUserId: string | undefined;
  presenceByUser: Map<string, string>;
  friendStatusByUser: Map<string, FriendStatus>;
  friendActions: ReturnType<typeof useFriendActions>;
  onOpenConversation: (conversationId: string) => void;
  onExpandSection: (section: SectionId) => void;
  onToggleSection: (section: SectionId) => void;
  onOpenFriendMenu: (user: InternalHandlerHttpUserResponse) => void;
  onOpenConvMenu: (c: {
    id: string;
    title: string;
    isPinned: boolean;
    isMuted: boolean;
    isGroup: boolean;
  }) => void;
  pendingFriendAction: Set<string>;
}) {
  // Headers don't get the keyboard-focus highlight — only tappable
  // rows do, otherwise arrowing past a section title would land
  // the focus ring on a non-actionable strip.
  if (row.kind === 'header') {
    return (
      <SectionHeader
        title={row.title}
        count={row.count}
        collapsed={row.collapsed}
        isFocused={isFocused}
        onToggle={() => onToggleSection(row.section)}
      />
    );
  }
  if (row.kind === 'show-all') {
    return (
      <ShowAllRow
        label={row.label}
        isFocused={isFocused}
        onPress={() => onExpandSection(row.section)}
      />
    );
  }
  // Wrap each tappable row in a focusable shell that paints a
  // primary tint behind it when the keyboard cursor is here.
  // ConversationRow already handles its own pinned-tint background
  // — overlaying primary/10 on top reads fine in both states.
  const inner = (() => {
    switch (row.kind) {
      case 'user': {
        const u = row.user;
        const opening = u.id != null && u.id === openingForUserId;
        const isSelf = !!myUserId && u.id === myUserId;
        const status = u.id ? friendStatusByUser.get(u.id) : undefined;
        const isFriend = status?.kind === 'friend';
        const inFlight = u.id ? pendingFriendAction.has(u.id) : false;
        // Friends can be tapped to open a DM. Non-friends get the row
        // tap disabled — the affordance lives in the trailing button
        // (Add friend / Unsend / accept-via-friends-tab) so a stray
        // row tap doesn't 403 against the friends-only DM rule.
        // Self gets no tap or button — the backend rejects self-DMs
        // and self-friend-requests; we shouldn't surface either.
        const onTap = !opening && !isSelf && isFriend && u.id ? () => onTapUser(u) : undefined;
        let trailing: React.ReactNode;
        if (isSelf) {
          trailing = (
            <Text variant="muted" className="text-xs">
              You
            </Text>
          );
        } else if (isFriend) {
          // "Friend" badge + 3-dots Unfriend/Block sheet — same
          // primitives the friends tab uses, with an explicit
          // relationship label so the row reads as "this person is
          // already in your graph" at a glance.
          trailing = (
            <View className="flex-row items-center gap-2">
              <RelationshipBadge label="Friend" />
              <FriendRowMenuButton
                disabled={inFlight}
                onPress={() => onOpenFriendMenu(u)}
                testID={u.id ? `search-${u.id}-menu` : undefined}
              />
            </View>
          );
        } else if (status?.kind === 'outgoing') {
          // Pending outgoing — "Added" badge alongside the existing
          // Unsend pill (FriendStatusAction renders that for the
          // outgoing kind). Mirrors the friends-tab search vocab.
          trailing = (
            <View className="flex-row items-center gap-2">
              <RelationshipBadge label="Added" />
              <FriendStatusAction
                status={status}
                username={u.username}
                busyLabel={opening ? 'Opening…' : undefined}
                onAdd={friendActions.sendFriendRequest}
                onCancel={friendActions.cancelFriendRequest}
                isAdding={friendActions.isAddingFor(u.username)}
                isCanceling={friendActions.isCancelingFor(status.requestId)}
                incomingMode="hint"
                testID={u.id ? `search-${u.id}` : undefined}
              />
            </View>
          );
        } else {
          // Status is now 'incoming' or undefined (friend and
          // outgoing handled above). FriendStatusAction renders
          // the "Sent you a request" hint for incoming and the
          // "Add friend" pill when there's no relationship.
          trailing = (
            <FriendStatusAction
              status={status}
              username={u.username}
              busyLabel={opening ? 'Opening…' : undefined}
              onAdd={friendActions.sendFriendRequest}
              onCancel={friendActions.cancelFriendRequest}
              isAdding={friendActions.isAddingFor(u.username)}
              isCanceling={false}
              incomingMode="hint"
              testID={u.id ? `search-${u.id}` : undefined}
            />
          );
        }
        // Presence is friends-only (§7.2). Show the dot for friend
        // rows so the search hit reads with the same online/offline
        // glance the friends-list section gives; strangers /
        // pending rows still hide it since their presence isn't
        // subscribed. Status emoji intentionally omitted — search
        // results stay clean visually (the user only wanted the
        // presence dot).
        const presence = isFriend && u.id ? presenceByUser.get(u.id) : undefined;
        return (
          <FriendRow
            displayName={u.display_name}
            username={u.username}
            avatarUrl={u.avatar_url}
            presence={presence}
            hidePresence={!isFriend}
            onPress={onTap}
            trailing={trailing}
          />
        );
      }
      case 'conversation': {
        const c = row.conversation;
        const full = c.id ? fullConversationById.get(c.id) : undefined;
        if (full && full.id) {
          const display = conversationDisplay(full, myUserId, presenceByUser);
          const isMuted = isCurrentlyMuted(full.muted_until);
          const isPinned = !!full.pinned_at;
          const fullId = full.id;
          return (
            <ConversationRow
              title={display.title}
              subtitle={display.subtitle}
              avatarUrl={display.avatarUrl}
              fallbackInitial={display.fallbackInitial}
              stackedMembers={display.stackedMembers}
              presence={display.presence}
              lastMessageAt={full.last_message_at}
              isMuted={isMuted}
              isPinned={isPinned}
              mutedUntil={full.muted_until}
              testID={`search-conversation-${fullId}`}
              onPress={() => onOpenConversation(fullId)}
              onMorePress={() => {
                haptics.tap();
                onOpenConvMenu({
                  id: fullId,
                  title: display.title,
                  isPinned,
                  isMuted,
                  isGroup: full.type === 'group',
                });
              }}
            />
          );
        }
        // Slim path: search hit for a conversation that isn't in
        // the chats-tab cache yet (user hasn't opened the tab this
        // session). No member roster → can't render the pin/mute
        // sheet meaningfully (it'd flash with stale `false`s);
        // skip the 3-dots in that case so we don't lie.
        return (
          <ConversationRow
            title={c.name?.trim() || 'Conversation'}
            avatarUrl={c.avatar_url}
            fallbackInitial={c.name ?? 'C'}
            lastMessageAt={c.last_message_at}
            onPress={() => {
              if (c.id) onOpenConversation(c.id);
            }}
            testID={`search-conversation-${c.id}`}
          />
        );
      }
      case 'message': {
        const m = row.message;
        return <MessageHitRow message={m} onOpenConversation={onOpenConversation} />;
      }
    }
  })();
  return <View className={isFocused ? 'bg-primary/10' : ''}>{inner}</View>;
}

function MessageHitRow({
  message,
  onOpenConversation,
}: {
  message: InternalHandlerHttpSearchMessageRow;
  onOpenConversation: (conversationId: string) => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  // Lightweight render until Phase 6 ships jump-to-message inside
  // the real thread surface. Tap routes to the conversation; we
  // can't pin the user to the exact message yet because the thread
  // is still a stub.
  return (
    <Pressable
      onPress={() => {
        if (message.conversation_id) {
          onOpenConversation(message.conversation_id);
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

// Tap target appended to a section that has more results than
// VISIBLE_PER_SECTION shows. Click promotes the section into the
// expanded set and the rest of the hits render in place. Renders
// as a centered ghost-button strip so the affordance reads as a
// secondary action (matches the friends-tab section affordances)
// instead of a list row.
function ShowAllRow({
  label,
  isFocused,
  onPress,
}: {
  label: string;
  isFocused: boolean;
  onPress: () => void;
}) {
  return (
    <View className={`px-4 py-2 ${isFocused ? 'bg-primary/10' : ''}`}>
      <Button size="sm" variant="ghost" onPress={onPress} accessibilityLabel={label}>
        <Text className="text-primary">{label}</Text>
      </Button>
    </View>
  );
}

function SectionHeader({
  title,
  count,
  collapsed,
  isFocused,
  onToggle,
}: {
  title: string;
  count: number;
  collapsed: boolean;
  // Web keyboard focus — Enter on a focused header toggles the
  // section. The visual tint mirrors the other rows' focus ring.
  isFocused?: boolean;
  onToggle: () => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  // Resolve the card colour to a literal hex/rgb so the sticky
  // overlay paints with a fully opaque fill — Tailwind's bg-card
  // class wasn't reliably opaque through FlashList's sticky
  // wrapper, which left avatar rows visible underneath the
  // chevron. Focus state still gets a primary tint via the
  // class, layered on top of the opaque card fill.
  const cardBg = useThemeColor('card');
  // Same chevron convention the friends tab uses for its
  // disclosure headers — ChevronRight when closed, ChevronDown
  // when open. Keeps the two collapse surfaces visually identical.
  const Caret = collapsed ? ChevronRight : ChevronDown;
  return (
    <Pressable
      onPress={onToggle}
      accessibilityRole="button"
      accessibilityLabel={`${title}, ${count} ${count === 1 ? 'item' : 'items'}`}
      accessibilityState={{ expanded: !collapsed }}
      testID={`search-section-${title.toLowerCase().replace(/\s+/g, '-')}`}
      style={{ backgroundColor: cardBg }}
      className={`flex-row items-center gap-2 border-b border-border px-4 py-2 active:bg-muted ${isFocused ? 'bg-primary/10' : ''}`}>
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
  // iOS pageSheet modals already sit below the status bar — no
  // safe-area inset needed. A bit more horizontal padding lets the
  // search input breathe inside the rounded modal corners.
  return (
    <View className="flex-row items-center gap-3 border-b border-border bg-card px-5 py-3">
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
