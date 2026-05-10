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

import { ConversationRow } from '@/components/conversation-row';
import { FriendRow } from '@/components/friend-row';
import { FriendStatusAction, type FriendStatus } from '@/components/friend-status-action';
import { Toast, toastConfig } from '@/components/toast-config';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List, type ListRef } from '@/components/ui/list';
import { ModalScreenShell } from '@/components/ui/modal-screen-shell';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { useGetV1FriendsRequests } from '@/lib/api/hooks/friends/friends';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import { useGetV1Search } from '@/lib/api/hooks/search/search';
import { useFriendActions } from '@/lib/api/use-friend-actions';
import { flatten, useInfiniteConversations, useInfiniteFriends } from '@/lib/api/use-infinite';
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
import { conversationDisplay } from '@/lib/conversation-display';
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
  | { kind: 'show-all'; key: string; section: SectionId; label: string };

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

  const searchQ = useGetV1Search(
    { q: debouncedQuery, types: 'users,conversations,messages' },
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
  const expandSection = React.useCallback((section: SectionId) => {
    setExpandedSections((prev) => {
      if (prev.has(section)) return prev;
      const next = new Set(prev);
      next.add(section);
      return next;
    });
  }, []);
  const toggleSection = React.useCallback((section: SectionId) => {
    setCollapsedSections((prev) => {
      const next = new Set(prev);
      if (next.has(section)) next.delete(section);
      else next.add(section);
      return next;
    });
  }, []);

  const rows = React.useMemo<Row[]>(
    () => buildRows(data, expandedSections, collapsedSections),
    [data, expandedSections, collapsedSections]
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
  // Whenever results change, snap focus to the first tappable row
  // so Enter immediately activates the most relevant hit.
  React.useEffect(() => {
    setFocusedRowIdx(tappableRowIndices[0] ?? null);
  }, [tappableRowIndices]);

  // Activate the row at a given index (Enter, or programmatic).
  // Different row kinds have different tap callbacks; this routes
  // each kind to its own handler so we don't duplicate the
  // "ensure DM / open thread" logic that already lives there.
  const activateRow = React.useCallback(
    (rowIdx: number | null) => {
      if (rowIdx == null) return;
      const row = rows[rowIdx];
      if (!row) return;
      if (row.kind === 'user' && row.user.id) {
        void onTapUser(row.user);
      } else if (row.kind === 'conversation' && row.conversation.id) {
        dismissThenGoToConversation(row.conversation.id);
      } else if (row.kind === 'message' && row.message.conversation_id) {
        dismissThenGoToConversation(row.message.conversation_id);
      } else if (row.kind === 'show-all') {
        expandSection(row.section);
      }
    },
    [rows, onTapUser, dismissThenGoToConversation, expandSection]
  );

  // Web-only keyboard nav: ↑/↓ cycles tappable rows, Enter activates.
  // Listener is on capture phase so a focused TextInput in the
  // header can't swallow the events. Native gets nothing — touch
  // UX uses tap-to-select directly.
  const listRef = React.useRef<ListRef<Row>>(null);
  React.useEffect(() => {
    if (Platform.OS !== 'web' || tappableRowIndices.length === 0) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setFocusedRowIdx((prev) => stepFocus(prev, tappableRowIndices, 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setFocusedRowIdx((prev) => stepFocus(prev, tappableRowIndices, -1));
      } else if (e.key === 'Enter') {
        e.preventDefault();
        activateRow(focusedRowIdx);
      }
    };
    window.addEventListener('keydown', handler, { capture: true });
    return () => window.removeEventListener('keydown', handler, { capture: true });
  }, [tappableRowIndices, focusedRowIdx, activateRow]);

  // Keep the focused row in view as the user arrows around — without
  // this, ArrowDown past the last visible row leaves the highlight
  // off-screen.
  React.useEffect(() => {
    if (focusedRowIdx == null) return;
    listRef.current?.scrollToIndex({ index: focusedRowIdx, viewPosition: 0.5 });
  }, [focusedRowIdx]);

  return (
    <ModalScreenShell onClose={goCancel} testID="search-modal-shell">
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
            ref={listRef}
            data={rows}
            keyExtractor={(item) => item.key}
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
              />
            )}
          />
        )}
      </View>
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
  if (r.kind === 'user') {
    // The rendered user row only allows the row tap when the
    // peer is an accepted friend AND not self. Mirror that here
    // so ↓ + Enter on a non-friend hit doesn't drive ensureDM
    // into a 403 / self-DM branch the rendered Pressable would
    // have refused.
    if (!r.user.id) return false;
    if (myUserId && r.user.id === myUserId) return false;
    return friendStatusByUser.get(r.user.id)?.kind === 'friend';
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

function buildRows(
  data: InternalHandlerHttpSearchResponse | undefined,
  expanded: Set<SectionId>,
  collapsed: Set<SectionId>
): Row[] {
  if (!data) return [];
  const out: Row[] = [];

  // Section header counts use `*_total` from the response — that's
  // the absolute population across every page, even though the
  // unified-search response itself is hard-capped at 10 per section.
  // Falling back to the slice length keeps the count honest if a
  // legacy backend response omits the total.
  const users = data.users ?? [];
  const usersTotal = data.users_total ?? users.length;
  if (users.length > 0) {
    const isCollapsed = collapsed.has('users');
    const showAll = expanded.has('users');
    const visible = showAll ? users : users.slice(0, VISIBLE_PER_SECTION);
    out.push({
      kind: 'header',
      key: 'h:users',
      title: 'People',
      count: usersTotal,
      section: 'users',
      collapsed: isCollapsed,
    });
    if (!isCollapsed) {
      visible.forEach((u, i) => {
        out.push({ kind: 'user', key: `user:${u.id ?? `idx-${i}`}`, user: u });
      });
      if (!showAll && users.length > VISIBLE_PER_SECTION) {
        const more = users.length - VISIBLE_PER_SECTION;
        out.push({
          kind: 'show-all',
          key: 'show-all:users',
          section: 'users',
          label: `Show ${more} more ${more === 1 ? 'user' : 'users'}`,
        });
      }
    }
  }

  const conversations = data.conversations ?? [];
  const conversationsTotal = data.conversations_total ?? conversations.length;
  if (conversations.length > 0) {
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
      if (!showAll && conversations.length > VISIBLE_PER_SECTION) {
        const more = conversations.length - VISIBLE_PER_SECTION;
        out.push({
          kind: 'show-all',
          key: 'show-all:conversations',
          section: 'conversations',
          label: `Show ${more} more ${more === 1 ? 'chat' : 'chats'}`,
        });
      }
    }
  }

  const messages = data.messages ?? [];
  const messagesTotal = data.messages_total ?? messages.length;
  if (messages.length > 0) {
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
        // Friends can be tapped to open a DM. Non-friends get the row
        // tap disabled — the affordance lives in the trailing button
        // (Add friend / Unsend / accept-via-friends-tab) so a stray
        // row tap doesn't 403 against the friends-only DM rule.
        // Self gets no tap or button — the backend rejects self-DMs
        // and self-friend-requests; we shouldn't surface either.
        const onTap = !opening && !isSelf && isFriend && u.id ? () => onTapUser(u) : undefined;
        return (
          <FriendRow
            displayName={u.display_name}
            username={u.username}
            avatarUrl={u.avatar_url}
            hidePresence
            onPress={onTap}
            trailing={
              isSelf ? (
                <Text variant="muted" className="text-xs">
                  You
                </Text>
              ) : (
                <FriendStatusAction
                  status={status}
                  username={u.username}
                  busyLabel={opening ? 'Opening…' : undefined}
                  onAdd={friendActions.sendFriendRequest}
                  onCancel={friendActions.cancelFriendRequest}
                  isAdding={friendActions.isAddingFor(u.username)}
                  isCanceling={friendActions.isCancelingFor(
                    status?.kind === 'outgoing' ? status.requestId : undefined
                  )}
                  incomingMode="hint"
                  testID={u.id ? `search-${u.id}` : undefined}
                />
              )
            }
          />
        );
      }
      case 'conversation': {
        const c = row.conversation;
        const full = c.id ? fullConversationById.get(c.id) : undefined;
        if (full) {
          const display = conversationDisplay(full, myUserId, presenceByUser);
          return (
            <ConversationRow
              title={display.title}
              subtitle={display.subtitle}
              avatarUrl={display.avatarUrl}
              fallbackInitial={display.fallbackInitial}
              stackedMembers={display.stackedMembers}
              presence={display.presence}
              lastMessageAt={full.last_message_at}
              onPress={() => {
                if (full.id) onOpenConversation(full.id);
              }}
              testID={`search-conversation-${full.id}`}
            />
          );
        }
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
  onToggle,
}: {
  title: string;
  count: number;
  collapsed: boolean;
  onToggle: () => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
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
