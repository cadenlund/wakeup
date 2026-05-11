// Phase 5.7 — manage group members modal.
//
// Sections:
//   - Current members. Each row reuses FriendRow's identity column;
//     trailing renders the same relationship vocabulary the global
//     search + friends tab use (Friend badge, Added badge, etc.) so
//     a member who's a friend gets the 3-dots Unfriend/Block menu and
//     tapping them opens the DM, exactly like the search modal.
//     Members who aren't in the caller's friend graph render a small
//     "Add friend" pill.
//   - Add members. Searches /v1/users (the same paginated, tier-ranked
//     endpoint the friends-tab search uses) and lets the caller
//     multi-select non-members to add to the group. Backend allows
//     non-friends as group members (friend-only DM rule doesn't apply
//     to groups), so this surface lets the user grow the group beyond
//     their friend graph.
//
// Modal route, presented like /search + /conversations/new. Web wraps
// in <ModalScreenShell> for the centered card; native uses the
// expo-router `presentation: 'modal'` half-sheet.
import { useLocalSearchParams, useRouter } from 'expo-router';
import { ChevronLeft, Plus, Search, UserPlus, X } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Platform, Pressable, View } from 'react-native';
import { FullWindowOverlay } from 'react-native-screens';
import { useQueryClient } from '@tanstack/react-query';

import { FriendActionMenu, FriendRowMenuButton } from '@/components/friend-action-menu';
import { FriendRow } from '@/components/friend-row';
import { FriendStatusAction, type FriendStatus } from '@/components/friend-status-action';
import { RelationshipBadge } from '@/components/relationship-badge';
import { Toast, toastConfig } from '@/components/toast-config';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { ModalScreenShell } from '@/components/ui/modal-screen-shell';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1ConversationsIdQueryKey,
  getGetV1ConversationsQueryKey,
  useGetV1ConversationsId,
  usePostV1ConversationsIdMembers,
} from '@/lib/api/hooks/conversations/conversations';
import {
  getGetV1FriendsQueryKey,
  getGetV1FriendsRequestsQueryKey,
  useDeleteV1FriendsUserId,
  useGetV1FriendsRequests,
  usePostV1FriendsUserIdBlock,
} from '@/lib/api/hooks/friends/friends';
import { useEnsureDirectConversation } from '@/lib/api/use-ensure-direct-conversation';
import { useFriendActions } from '@/lib/api/use-friend-actions';
import { flatten, useInfiniteFriends } from '@/lib/api/use-infinite';
import type {
  InternalHandlerHttpConversationMemberRow,
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpFriendRequestsResponse,
  InternalHandlerHttpFriendshipResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

const SEARCH_DEBOUNCE_MS = 200;

type UserRow = InternalHandlerHttpUserResponse;

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}

export default function ManageMembersScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();
  const conversationId = id ?? '';

  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;

  // Conversation detail drives the current-member list. Falls back to
  // the chats-tab cache via the GetV1ConversationsId hook's automatic
  // QueryClient lookup; otherwise refetches.
  const detailQ = useGetV1ConversationsId(conversationId, {
    query: { enabled: !!conversationId, staleTime: 15_000 },
  });
  const conversation = detailQ.data as InternalHandlerHttpConversationResponse | undefined;
  const members = React.useMemo(() => conversation?.members ?? [], [conversation]);

  // Friend graph for relationship-aware trailing UI. Shared cache with
  // the friends tab so subsequent opens are cheap.
  const friendsQ = useInfiniteFriends({ query: { staleTime: 30_000 } });
  const requestsQ = useGetV1FriendsRequests({ query: { staleTime: 30_000 } });
  const requestsData = requestsQ.data as InternalHandlerHttpFriendRequestsResponse | undefined;
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

  // Add-flow state. The source is the caller's friends list, not the
  // open /v1/users catalog — group invites are scoped to people you
  // actually know per the product rule. Filtering happens client-side
  // (friends lists are bounded by realistic UX, so we can fetch every
  // page once and substring-filter without server round-trips).
  const [showAdd, setShowAdd] = React.useState(false);
  const [query, setQuery] = React.useState('');
  const debouncedQuery = useDebouncedValue(query.trim().toLowerCase(), SEARCH_DEBOUNCE_MS);

  const existingMemberIds = React.useMemo(() => {
    const s = new Set<string>();
    for (const m of members) {
      if (m.user?.id) s.add(m.user.id);
    }
    return s;
  }, [members]);

  // Eagerly chain friend pages so the add list is comprehensive
  // without the user having to scroll. The friends graph is small
  // by design; one or two extra page fetches on first open is cheap.
  React.useEffect(() => {
    if (!showAdd) return;
    if (friendsQ.hasNextPage && !friendsQ.isFetchingNextPage) {
      void friendsQ.fetchNextPage();
    }
  }, [showAdd, friendsQ, friendsQ.data]);

  const addCandidates = React.useMemo<UserRow[]>(() => {
    const { data: friends } = flatten<
      InternalHandlerHttpFriendshipResponse,
      { data?: InternalHandlerHttpFriendshipResponse[] }
    >(friendsQ.data?.pages);
    const out: UserRow[] = [];
    for (const f of friends) {
      const u = f.user;
      if (!u?.id) continue;
      if (existingMemberIds.has(u.id)) continue; // already in
      if (debouncedQuery) {
        const hay = `${u.username ?? ''} ${u.display_name ?? ''}`.toLowerCase();
        if (!hay.includes(debouncedQuery)) continue;
      }
      out.push(u);
    }
    return out;
  }, [friendsQ.data, existingMemberIds, debouncedQuery]);

  const [selectedIds, setSelectedIds] = React.useState<Set<string>>(new Set());
  const toggleSelect = React.useCallback(
    (uid: string) => {
      if (existingMemberIds.has(uid)) return; // already in
      setSelectedIds((prev) => {
        const next = new Set(prev);
        if (next.has(uid)) next.delete(uid);
        else next.add(uid);
        return next;
      });
    },
    [existingMemberIds]
  );

  const addMembers = usePostV1ConversationsIdMembers();
  const onCommitAdd = React.useCallback(async () => {
    if (selectedIds.size === 0) return;
    const ids = Array.from(selectedIds);
    try {
      await addMembers.mutateAsync({ id: conversationId, data: { user_ids: ids } });
      // Invalidate both the per-id detail (drives this screen) and the
      // list (drives the chats tab + search modal preview rows).
      await Promise.all([
        qc.invalidateQueries({ queryKey: getGetV1ConversationsIdQueryKey(conversationId) }),
        qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() }),
      ]);
      setSelectedIds(new Set());
      setShowAdd(false);
      setQuery('');
      toast.success(ids.length === 1 ? 'Added 1 member' : `Added ${ids.length} members`, undefined);
    } catch (err) {
      const msg =
        err instanceof APIError && err.message
          ? err.message
          : "Couldn't add those members right now.";
      toast.error(msg);
    }
  }, [addMembers, conversationId, qc, selectedIds]);

  // Friend "more actions" — same sheet the friends tab + search modal
  // open. Lets the user manage a friendship from within the group's
  // member list (Unfriend / Block).
  const unfriend = useDeleteV1FriendsUserId();
  const blockUser = usePostV1FriendsUserIdBlock();
  const [friendMenuTarget, setFriendMenuTarget] = React.useState<UserRow | null>(null);
  const [pendingFriendAction, setPendingFriendAction] = React.useState<Set<string>>(new Set());
  const markPending = React.useCallback((uid: string) => {
    setPendingFriendAction((p) => new Set(p).add(uid));
  }, []);
  const unmarkPending = React.useCallback((uid: string) => {
    setPendingFriendAction((p) => {
      const next = new Set(p);
      next.delete(uid);
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
    async (u: UserRow) => {
      const userId = u.id;
      if (!userId) return;
      markPending(userId);
      setFriendMenuTarget(null);
      try {
        await unfriend.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = u.username ? `@${u.username}` : 'this user';
        toast.info('Unfriended', `${handle} is no longer in your friends.`);
      } catch (err) {
        toast.error(err instanceof APIError && err.message ? err.message : "Couldn't unfriend.");
      } finally {
        unmarkPending(userId);
      }
    },
    [invalidateRelationships, markPending, unfriend, unmarkPending]
  );
  const onBlock = React.useCallback(
    async (u: UserRow) => {
      const userId = u.id;
      if (!userId) return;
      markPending(userId);
      setFriendMenuTarget(null);
      try {
        await blockUser.mutateAsync({ userId });
        await invalidateRelationships();
        const handle = u.username ? `@${u.username}` : 'this user';
        toast.info('Blocked', `${handle} can't message or add you.`);
      } catch (err) {
        toast.error(err instanceof APIError && err.message ? err.message : "Couldn't block.");
      } finally {
        unmarkPending(userId);
      }
    },
    [blockUser, invalidateRelationships, markPending, unmarkPending]
  );

  // Shared friend-action hook for Add / Unsend pills (mirrors search
  // modal). Send/cancel friend request from a non-friend member row
  // here too.
  const friendActions = useFriendActions();

  // Tap a friend member → open DM (same UX as global search). Strangers
  // and pending rows leave the row tap-disabled.
  const ensureDM = useEnsureDirectConversation();
  const [openingDmFor, setOpeningDmFor] = React.useState<string | null>(null);
  const onTapMember = React.useCallback(
    async (u: UserRow) => {
      if (!u.id || u.id === me?.id) return;
      if (friendStatusByUser.get(u.id)?.kind !== 'friend') return;
      if (openingDmFor) return;
      setOpeningDmFor(u.id);
      try {
        const { conversationId: dmId } = await ensureDM.ensure(u.id);
        if (router.canGoBack()) router.back();
        setTimeout(() => router.push(`/conversations/${dmId}`), 0);
      } catch (err) {
        toast.error(
          err instanceof APIError && err.message ? err.message : "Couldn't open the conversation."
        );
      } finally {
        setOpeningDmFor(null);
      }
    },
    [ensureDM, friendStatusByUser, me?.id, openingDmFor, router]
  );

  const goCancel = React.useCallback(() => {
    if (router.canGoBack()) router.back();
    else router.replace('/');
  }, [router]);

  return (
    <ModalScreenShell onClose={goCancel} testID="manage-members-shell">
      <View className="flex-1 bg-background">
        <Header
          title={showAdd ? 'Add members' : 'Manage members'}
          onBack={showAdd ? () => setShowAdd(false) : undefined}
          onClose={goCancel}
        />

        {showAdd ? (
          <AddPane
            query={query}
            onQueryChange={setQuery}
            results={addCandidates}
            selectedIds={selectedIds}
            onToggleSelect={toggleSelect}
            loading={friendsQ.isLoading && !friendsQ.data}
            onCommit={onCommitAdd}
            committing={addMembers.isPending}
          />
        ) : (
          <MembersPane
            members={members}
            loading={detailQ.isLoading && !detailQ.data}
            myUserId={me?.id}
            friendStatusByUser={friendStatusByUser}
            friendActions={friendActions}
            pendingFriendAction={pendingFriendAction}
            openingDmFor={openingDmFor}
            onTapMember={onTapMember}
            onOpenFriendMenu={setFriendMenuTarget}
            onAdd={() => setShowAdd(true)}
          />
        )}
      </View>

      <FriendActionMenu
        target={friendMenuTarget}
        pendingAction={pendingFriendAction}
        onClose={() => setFriendMenuTarget(null)}
        onUnfriend={onUnfriend}
        onBlock={onBlock}
      />

      {Platform.OS === 'ios' ? (
        <FullWindowOverlay>
          <Toast config={toastConfig} topOffset={12} />
        </FullWindowOverlay>
      ) : null}
    </ModalScreenShell>
  );
}

function Header({
  title,
  onBack,
  onClose,
}: {
  title: string;
  // When provided, renders a left-side chevron that returns to
  // the parent surface (e.g. the add-members pane backs out to
  // the members list without dismissing the whole modal).
  onBack?: () => void;
  // Always present — closes the entire modal. Icon-only so the
  // affordance can't wrap on narrow phones (the earlier "Cancel"
  // text wrapped to two lines on small viewports).
  onClose: () => void;
}) {
  const fg = useThemeColor('foreground');
  return (
    <View className="flex-row items-center border-b border-border bg-card px-3 py-3">
      <View className="w-10 items-start">
        {onBack ? (
          <Pressable
            onPress={onBack}
            accessibilityRole="button"
            accessibilityLabel="Back"
            testID="manage-members-back"
            hitSlop={8}
            className="h-9 w-9 items-center justify-center rounded-md active:bg-muted">
            <ChevronLeft size={22} color={fg} />
          </Pressable>
        ) : null}
      </View>
      <Text variant="h4" numberOfLines={1} className="flex-1 text-center">
        {title}
      </Text>
      <View className="w-10 items-end">
        <Pressable
          onPress={onClose}
          accessibilityRole="button"
          accessibilityLabel="Close"
          testID="manage-members-close"
          hitSlop={8}
          className="h-9 w-9 items-center justify-center rounded-md active:bg-muted">
          <X size={20} color={fg} />
        </Pressable>
      </View>
    </View>
  );
}

function MembersPane({
  members,
  loading,
  myUserId,
  friendStatusByUser,
  friendActions,
  pendingFriendAction,
  openingDmFor,
  onTapMember,
  onOpenFriendMenu,
  onAdd,
}: {
  members: InternalHandlerHttpConversationMemberRow[];
  loading: boolean;
  myUserId: string | undefined;
  friendStatusByUser: Map<string, FriendStatus>;
  friendActions: ReturnType<typeof useFriendActions>;
  pendingFriendAction: Set<string>;
  openingDmFor: string | null;
  onTapMember: (u: UserRow) => void;
  onOpenFriendMenu: (u: UserRow) => void;
  onAdd: () => void;
}) {
  const fg = useThemeColor('foreground');
  if (loading) {
    return (
      <View className="flex-1 items-center justify-center py-12">
        <ActivityIndicator color={fg} />
      </View>
    );
  }
  // The "Add members" affordance lives at the top as a row so the
  // user can scan and decide before scrolling the full list. Native
  // FlashList renders it via ListHeaderComponent so it scrolls with
  // the rows; on a long member list that prevents the button from
  // hogging viewport real estate.
  return (
    <List
      data={members}
      keyExtractor={(m, i) => m.user?.id ?? `idx-${i}`}
      ListHeaderComponent={
        <Pressable
          onPress={onAdd}
          accessibilityRole="button"
          accessibilityLabel="Add members"
          testID="manage-members-add"
          className="flex-row items-center gap-3 border-b border-border bg-card px-4 py-3 active:bg-muted">
          <View className="h-10 w-10 items-center justify-center rounded-full bg-primary/10">
            <UserPlus size={20} color={fg} />
          </View>
          <Text className="text-base font-medium">Add members</Text>
        </Pressable>
      }
      renderItem={({ item }) => (
        <MemberRow
          member={item}
          myUserId={myUserId}
          friendStatusByUser={friendStatusByUser}
          friendActions={friendActions}
          pendingFriendAction={pendingFriendAction}
          openingDmFor={openingDmFor}
          onTap={onTapMember}
          onOpenFriendMenu={onOpenFriendMenu}
        />
      )}
    />
  );
}

function MemberRow({
  member,
  myUserId,
  friendStatusByUser,
  friendActions,
  pendingFriendAction,
  openingDmFor,
  onTap,
  onOpenFriendMenu,
}: {
  member: InternalHandlerHttpConversationMemberRow;
  myUserId: string | undefined;
  friendStatusByUser: Map<string, FriendStatus>;
  friendActions: ReturnType<typeof useFriendActions>;
  pendingFriendAction: Set<string>;
  openingDmFor: string | null;
  onTap: (u: UserRow) => void;
  onOpenFriendMenu: (u: UserRow) => void;
}) {
  const u = member.user;
  if (!u) return null;
  const isSelf = !!myUserId && u.id === myUserId;
  const status = u.id ? friendStatusByUser.get(u.id) : undefined;
  const isFriend = status?.kind === 'friend';
  const inFlight = u.id ? pendingFriendAction.has(u.id) : false;
  const opening = u.id != null && u.id === openingDmFor;

  let trailing: React.ReactNode;
  if (isSelf) {
    trailing = (
      <Text variant="muted" className="text-xs">
        You
      </Text>
    );
  } else if (isFriend) {
    // Same Friend + 3-dots pattern the global search modal uses.
    trailing = (
      <View className="flex-row items-center gap-2">
        <RelationshipBadge label="Friend" />
        <FriendRowMenuButton
          disabled={inFlight}
          onPress={() => onOpenFriendMenu(u)}
          testID={u.id ? `member-${u.id}-menu` : undefined}
        />
      </View>
    );
  } else if (status?.kind === 'outgoing') {
    trailing = (
      <View className="flex-row items-center gap-2">
        <RelationshipBadge label="Added" />
        <FriendStatusAction
          status={status}
          username={u.username}
          onAdd={friendActions.sendFriendRequest}
          onCancel={friendActions.cancelFriendRequest}
          isAdding={friendActions.isAddingFor(u.username)}
          isCanceling={friendActions.isCancelingFor(status.requestId)}
          incomingMode="hint"
          testID={u.id ? `member-${u.id}-status` : undefined}
        />
      </View>
    );
  } else {
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
        testID={u.id ? `member-${u.id}-status` : undefined}
      />
    );
  }

  const onPress = !opening && !isSelf && isFriend ? () => onTap(u) : undefined;
  return (
    <FriendRow
      displayName={u.display_name}
      username={u.username}
      avatarUrl={u.avatar_url}
      hidePresence
      onPress={onPress}
      trailing={trailing}
    />
  );
}

function AddPane({
  query,
  onQueryChange,
  results,
  selectedIds,
  onToggleSelect,
  loading,
  onCommit,
  committing,
}: {
  query: string;
  onQueryChange: (v: string) => void;
  // Pre-filtered list of friend candidates — already excludes
  // existing members + self, and already substring-matches against
  // `query`. The pane just renders.
  results: UserRow[];
  selectedIds: Set<string>;
  onToggleSelect: (uid: string) => void;
  loading: boolean;
  onCommit: () => void;
  committing: boolean;
}) {
  const fg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1">
      <View className="border-b border-border bg-card px-4 pb-3 pt-3">
        <View className="relative">
          <View className="absolute bottom-0 left-3 top-0 z-10 justify-center">
            <Search size={16} color={fg} />
          </View>
          <Input
            value={query}
            onChangeText={onQueryChange}
            placeholder="Filter your friends"
            autoCapitalize="none"
            autoCorrect={false}
            autoComplete="off"
            returnKeyType="search"
            autoFocus
            testID="manage-members-search-input"
            accessibilityLabel="Filter your friends"
            className="pl-9 pr-9"
          />
          {query.length > 0 ? (
            <Pressable
              onPress={() => onQueryChange('')}
              accessibilityRole="button"
              accessibilityLabel="Clear filter"
              testID="manage-members-search-clear"
              hitSlop={8}
              className="absolute bottom-0 right-3 top-0 z-10 justify-center">
              <X size={16} color={fg} />
            </Pressable>
          ) : null}
        </View>
      </View>

      {loading ? (
        <View className="flex-1 items-center justify-center py-12">
          <ActivityIndicator color={fg} />
        </View>
      ) : results.length === 0 ? (
        <View className="flex-1 items-center justify-center px-6">
          <Text variant="muted" className="text-center text-sm">
            {query.length > 0
              ? 'No friends matched.'
              : "You can only add friends to a group. You haven't added anyone yet."}
          </Text>
        </View>
      ) : (
        <List
          data={results}
          keyExtractor={(u, i) => u.id ?? `idx-${i}`}
          renderItem={({ item }) => (
            <AddRow
              user={item}
              isSelected={!!item.id && selectedIds.has(item.id)}
              onToggle={() => item.id && onToggleSelect(item.id)}
            />
          )}
        />
      )}

      <CommitFooter count={selectedIds.size} pending={committing} onCommit={onCommit} />
    </View>
  );
}

function AddRow({
  user,
  isSelected,
  onToggle,
}: {
  user: UserRow;
  isSelected: boolean;
  onToggle: () => void;
}) {
  // Every row in this list is an addable friend by construction
  // (the parent filtered self + existing members out), so the
  // trailing UI is just "Selected" vs "Tap to add."
  const trailing = isSelected ? (
    <RelationshipBadge label="Selected" />
  ) : (
    <Text variant="muted" className="text-xs">
      Tap to add
    </Text>
  );
  return (
    <FriendRow
      displayName={user.display_name}
      username={user.username}
      avatarUrl={user.avatar_url}
      hidePresence
      onPress={onToggle}
      trailing={trailing}
    />
  );
}

function CommitFooter({
  count,
  pending,
  onCommit,
}: {
  count: number;
  pending: boolean;
  onCommit: () => void;
}) {
  if (count === 0 && !pending) return null;
  return (
    <View className="border-t border-border bg-card px-4 py-3">
      <Button onPress={onCommit} disabled={pending || count === 0} testID="manage-members-commit">
        <View className="flex-row items-center gap-2">
          <Plus size={16} color="#fff" />
          <Text style={{ color: '#fff' }} className="text-base font-semibold">
            {pending ? 'Adding…' : count === 1 ? 'Add 1 member' : `Add ${count} members`}
          </Text>
        </View>
      </Button>
    </View>
  );
}
