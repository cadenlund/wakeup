// Phase 5.2 — New conversation modal. Multi-select friends; one
// selected = direct DM, two-or-more = group. The group-name input
// shows up only once you cross the threshold so DMs stay
// frictionless ("tap a friend → tap Create" is two taps).
//
// Header right ("Create") gates on:
//   - at least one friend selected
//   - if it's a group, the name is non-empty after trim
//   - no in-flight create
// Tap Create → POST /v1/conversations → invalidate the list →
// router.replace into the resulting conversation thread. We
// `replace` (not push) so the back button on the thread goes to
// the chats list, not back to the half-built modal.
//
// User search beyond accepted friends is out of scope here per the
// 5.2 plan ("multi-select friends + group name"); broader user
// search lives in the global /search modal (5.5).
import { useFocusEffect, useRouter } from 'expo-router';
import { Check, MessageCircle, Search, WifiOff, X } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Platform, Pressable, View } from 'react-native';
import { FullWindowOverlay } from 'react-native-screens';
import { useQueryClient } from '@tanstack/react-query';

import { Toast, toastConfig } from '@/components/toast-config';
import { Avatar } from '@/components/ui/avatar';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { ModalScreenShell } from '@/components/ui/modal-screen-shell';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import {
  getGetV1ConversationsQueryKey,
  usePostV1Conversations,
} from '@/lib/api/hooks/conversations/conversations';
import { flatten, useInfiniteFriends } from '@/lib/api/use-infinite';
import type {
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpFriendshipResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

type Friendship = InternalHandlerHttpFriendshipResponse;

const GROUP_NAME_MAX = 80;
// Backend's CreateConversationRequest schema caps member_ids at 24
// (you + 24 = 25 members per group). Mirror it client-side so we
// don't kick off a doomed POST.
const GROUP_MEMBER_MAX = 24;

export default function NewConversationScreen() {
  const router = useRouter();
  const qc = useQueryClient();

  // Friends list — paginated. Without infinite scroll, users with
  // hundreds of friends would never see most of them in the picker.
  // The list is small enough that we can prefetch all pages once
  // the modal mounts; effect below chains fetchNextPage until done.
  const friendsQ = useInfiniteFriends({ query: { staleTime: 30_000 } });
  React.useEffect(() => {
    if (friendsQ.hasNextPage && !friendsQ.isFetchingNextPage) {
      void friendsQ.fetchNextPage();
    }
  }, [friendsQ, friendsQ.hasNextPage, friendsQ.isFetchingNextPage]);
  // Memoise the flattened array so downstream useMemo deps stay
  // referentially stable across renders that don't touch the data.
  const friends = React.useMemo(
    () => flatten<Friendship, { data?: Friendship[] }>(friendsQ.data?.pages).data,
    [friendsQ.data]
  );

  const [query, setQuery] = React.useState('');
  const [selectedIds, setSelectedIds] = React.useState<Set<string>>(new Set());
  const [groupName, setGroupName] = React.useState('');
  const [creating, setCreating] = React.useState(false);

  const create = usePostV1Conversations();

  const filtered = React.useMemo(() => filterFriends(friends, query), [friends, query]);
  const selectedFriends = React.useMemo(
    () => friends.filter((f) => f.user?.id && selectedIds.has(f.user.id)),
    [friends, selectedIds]
  );
  const isGroup = selectedIds.size >= 2;
  const overMemberCap = selectedIds.size > GROUP_MEMBER_MAX;
  // Group name is optional — backend accepts unnamed groups and the
  // chats list renders them with a "Alice, Bob, Carol and N more"
  // title fallback (conversation-display.ts). Was gated on
  // groupName.trim() being non-empty; that forced users to type
  // something before they could even tap Create.
  const canCreate = !creating && selectedIds.size >= 1 && !overMemberCap;

  const toggleSelect = React.useCallback((userId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(userId)) {
        next.delete(userId);
        return next;
      }
      // At-cap protection: don't add a 25th friend. The user is
      // already deselectable above, so this never strands them.
      if (next.size >= GROUP_MEMBER_MAX) {
        toast.info(`Group max is ${GROUP_MEMBER_MAX} friends`);
        return prev;
      }
      next.add(userId);
      return next;
    });
  }, []);

  const onCancel = React.useCallback(() => {
    // Direct-open via deep link has no back history — router.back()
    // in that case is a no-op on Android and closes the app on iOS.
    // Fall back to the chats tab so the close affordance always
    // lands somewhere coherent.
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/');
    }
  }, [router]);

  const onCreate = React.useCallback(async () => {
    if (!canCreate) return;
    const memberIds = Array.from(selectedIds);
    setCreating(true);
    try {
      // Group name is optional — omit when empty so the backend
      // stores NULL and the chats list falls back to the
      // member-name preview ("Alice, Bob, Carol and N more").
      const trimmedName = groupName.trim();
      const res = (await create.mutateAsync({
        data: isGroup
          ? trimmedName
            ? { type: 'group', member_ids: memberIds, name: trimmedName }
            : { type: 'group', member_ids: memberIds }
          : { type: 'direct', member_ids: memberIds },
      })) as InternalHandlerHttpConversationResponse | undefined;
      await qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() });
      toast.success(isGroup ? 'Group created' : 'Conversation started');
      // replace, not push: back from the thread goes to the chats
      // list, not back to a stale modal.
      if (res?.id) {
        router.replace(`/conversations/${res.id}`);
      } else if (router.canGoBack()) {
        router.back();
      } else {
        router.replace('/');
      }
    } catch (err) {
      const msg =
        err instanceof APIError && err.message
          ? err.message
          : "Couldn't start the conversation — try again in a sec.";
      toast.error(msg);
    } finally {
      setCreating(false);
    }
  }, [canCreate, selectedIds, isGroup, groupName, create, qc, router]);

  // Reset on every focus so re-opening the modal starts clean even
  // if the user dismissed mid-flow last time.
  useFocusEffect(
    React.useCallback(() => {
      return () => {
        setSelectedIds(new Set());
        setGroupName('');
        setQuery('');
      };
    }, [])
  );

  if (friendsQ.isLoading && !friendsQ.data) {
    return (
      <ModalScreenShell onClose={onCancel}>
        <FullPaneLoading />
      </ModalScreenShell>
    );
  }
  // Genuine empty list vs failed cold-load look identical without
  // the isError gate — both just have data === undefined / []. Show
  // an error state with a Retry CTA when the request actually
  // failed; only fall through to "no friends yet" when we have a
  // confirmed empty array.
  if (friendsQ.isError && !friendsQ.data) {
    return (
      <ModalScreenShell onClose={onCancel}>
        <FetchError onRetry={() => friendsQ.refetch()} onClose={onCancel} />
      </ModalScreenShell>
    );
  }
  if (friends.length === 0) {
    return (
      <ModalScreenShell onClose={onCancel}>
        <NoFriends onClose={onCancel} />
      </ModalScreenShell>
    );
  }

  return (
    <ModalScreenShell onClose={onCancel} testID="new-conversation-shell">
      <View className="flex-1 bg-background">
        <ModalHeader
          canCreate={canCreate}
          creating={creating}
          onCancel={onCancel}
          onCreate={onCreate}
          ctaLabel={isGroup ? 'Create' : 'Start'}
        />

        {/* Selected pills strip — only when 1+ selected, gives the
            user a visual confirmation of who's in the new conv. */}
        {selectedFriends.length > 0 ? (
          <SelectedStrip friends={selectedFriends} onRemove={(id) => toggleSelect(id)} />
        ) : null}

        {/* Group name field appears only at 2+ — DMs don't need a
            name, and showing it for a 1-friend selection would be
            cognitive noise. */}
        {isGroup ? <GroupNameField value={groupName} onChange={setGroupName} /> : null}

        {overMemberCap ? (
          <View className="border-b border-border bg-destructive/10 px-4 py-2">
            <Text variant="muted" className="text-center text-xs">
              Groups can have at most {GROUP_MEMBER_MAX} other members.
            </Text>
          </View>
        ) : null}

        <SearchField value={query} onChange={setQuery} />

        <List
          data={filtered}
          keyExtractor={(f, i) => f.user?.id ?? f.id ?? `idx-${i}`}
          renderItem={({ item }) => (
            <FriendCheckRow
              friendship={item}
              selected={!!item.user?.id && selectedIds.has(item.user.id)}
              disabled={creating}
              onToggle={() => item.user?.id && toggleSelect(item.user.id)}
            />
          )}
        />
      </View>
      {/* FullWindowOverlay mounts above the iOS modal at the
          window level so toasts pop ABOVE this screen's chrome.
          iOS-only — the overlay is a no-op on Android, web uses
          sonner via portal. */}
      {Platform.OS === 'ios' ? (
        <FullWindowOverlay>
          <Toast config={toastConfig} topOffset={60} />
        </FullWindowOverlay>
      ) : null}
    </ModalScreenShell>
  );
}

function filterFriends(friends: Friendship[], q: string): Friendship[] {
  const term = q.trim().toLowerCase();
  if (!term) return friends;
  return friends.filter((f) => {
    const u = f.user;
    if (!u) return false;
    const dn = (u.display_name ?? '').toLowerCase();
    const un = (u.username ?? '').toLowerCase();
    return dn.includes(term) || un.includes(term);
  });
}

function ModalHeader({
  canCreate,
  creating,
  onCancel,
  onCreate,
  ctaLabel,
}: {
  canCreate: boolean;
  creating: boolean;
  onCancel: () => void;
  onCreate: () => void;
  ctaLabel: string;
}) {
  const primary = useThemeColor('primary');
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-row items-center justify-between border-b border-border bg-card px-5 py-3">
      <Pressable
        onPress={onCancel}
        accessibilityRole="button"
        accessibilityLabel="Cancel"
        testID="conversation-new-cancel"
        hitSlop={8}>
        <Text style={{ color: mutedFg }} className="text-base">
          Cancel
        </Text>
      </Pressable>
      <Text variant="h4">New chat</Text>
      <Pressable
        onPress={onCreate}
        disabled={!canCreate}
        accessibilityRole="button"
        accessibilityLabel={ctaLabel}
        accessibilityState={{ disabled: !canCreate }}
        testID="conversation-new-create"
        hitSlop={8}>
        {creating ? (
          <ActivityIndicator color={primary} />
        ) : (
          <Text
            style={{ color: canCreate ? primary : mutedFg }}
            className="text-base font-semibold">
            {ctaLabel}
          </Text>
        )}
      </Pressable>
    </View>
  );
}

function SearchField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="border-b border-border bg-background px-4 py-3">
      <View className="relative">
        <View className="absolute bottom-0 left-3 top-0 z-10 justify-center">
          <Search size={16} color={mutedFg} />
        </View>
        <Input
          value={value}
          onChangeText={onChange}
          placeholder="Search friends"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="off"
          returnKeyType="search"
          testID="conversation-new-search"
          accessibilityLabel="Filter your friends"
          className="pl-9 pr-9"
        />
        {value.length > 0 ? (
          <Pressable
            onPress={() => onChange('')}
            accessibilityRole="button"
            accessibilityLabel="Clear search"
            testID="conversation-new-search-clear"
            hitSlop={8}
            className="absolute bottom-0 right-3 top-0 z-10 justify-center">
            <X size={16} color={mutedFg} />
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

function GroupNameField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <View className="border-b border-border bg-background px-4 pb-3 pt-3">
      <Text variant="muted" className="mb-1 text-xs font-semibold uppercase tracking-wider">
        Group name
      </Text>
      <Input
        value={value}
        onChangeText={onChange}
        placeholder="e.g. Roommates, Trip squad"
        maxLength={GROUP_NAME_MAX}
        autoCapitalize="sentences"
        testID="conversation-new-group-name"
        accessibilityLabel="Group name"
      />
    </View>
  );
}

function SelectedStrip({
  friends,
  onRemove,
}: {
  friends: Friendship[];
  onRemove: (userId: string) => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-row flex-wrap gap-2 border-b border-border bg-background px-4 py-3">
      {friends.map((f) => {
        const u = f.user;
        if (!u?.id) return null;
        const handle = u.display_name?.trim() || u.username?.trim() || 'Friend';
        return (
          <Pressable
            key={u.id}
            onPress={() => onRemove(u.id!)}
            accessibilityRole="button"
            accessibilityLabel={`Remove ${handle}`}
            testID={`conversation-new-pill-${u.id}`}
            className="flex-row items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-1.5 active:bg-muted">
            <Avatar source={u.avatar_url} fallbackName={handle} size={20} />
            <Text className="text-sm">{handle}</Text>
            <X size={12} color={mutedFg} />
          </Pressable>
        );
      })}
    </View>
  );
}

function FriendCheckRow({
  friendship,
  selected,
  disabled,
  onToggle,
}: {
  friendship: Friendship;
  selected: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  const u = friendship.user;
  const handle = u?.display_name?.trim() || u?.username?.trim() || 'Friend';
  // Theme-aware tick colour — primary-foreground is paired with the
  // primary background by the palette, so the contrast stays right
  // across every scheme/mode.
  const checkColor = useThemeColor('primary-foreground');
  return (
    <Pressable
      onPress={onToggle}
      disabled={disabled}
      accessibilityRole="checkbox"
      accessibilityLabel={handle}
      accessibilityState={{ checked: selected, disabled }}
      testID={`conversation-new-friend-${u?.id ?? handle}`}
      className="flex-row items-center gap-3 px-4 py-3 active:bg-muted">
      <Avatar source={u?.avatar_url} fallbackName={handle} size={40} />
      <View className="min-w-0 flex-1">
        <Text numberOfLines={1} className="text-base font-medium">
          {handle}
        </Text>
        {u?.username ? (
          <Text numberOfLines={1} variant="muted" className="text-sm">
            @{u.username}
          </Text>
        ) : null}
      </View>
      <View
        className={`h-6 w-6 items-center justify-center rounded-full border ${
          selected ? 'border-primary bg-primary' : 'border-border'
        }`}>
        {selected ? <Check size={14} color={checkColor} /> : null}
      </View>
    </Pressable>
  );
}

function FullPaneLoading() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center bg-background">
      <ActivityIndicator color={mutedFg} />
    </View>
  );
}

function FetchError({ onRetry, onClose }: { onRetry: () => void; onClose: () => void }) {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <ModalHeader
        canCreate={false}
        creating={false}
        onCancel={onClose}
        onCreate={() => {}}
        ctaLabel="Start"
      />
      <EmptyState
        icon={<WifiOff size={40} color={mutedFg} />}
        title="Couldn't load your friends"
        subtitle="Check your connection and try again."
        cta={{
          label: 'Retry',
          onPress: onRetry,
        }}
      />
    </View>
  );
}

function NoFriends({ onClose }: { onClose: () => void }) {
  const router = useRouter();
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 bg-background">
      <ModalHeader
        canCreate={false}
        creating={false}
        onCancel={onClose}
        onCreate={() => {}}
        ctaLabel="Start"
      />
      <EmptyState
        icon={<MessageCircle size={40} color={mutedFg} />}
        title="No friends to message yet"
        subtitle="Add a friend first, then come back here to start a conversation."
        cta={{
          label: 'Go to Friends',
          onPress: () => {
            onClose();
            router.push('/friends');
          },
        }}
      />
    </View>
  );
}
