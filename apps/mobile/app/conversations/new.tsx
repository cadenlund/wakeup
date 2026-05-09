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
import { Check, MessageCircle, Search, X } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Pressable, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Avatar } from '@/components/ui/avatar';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import {
  getGetV1ConversationsQueryKey,
  usePostV1Conversations,
} from '@/lib/api/hooks/conversations/conversations';
import { useGetV1Friends } from '@/lib/api/hooks/friends/friends';
import type {
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpFriendListResponse,
  InternalHandlerHttpFriendshipResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

type Friendship = InternalHandlerHttpFriendshipResponse;

const GROUP_NAME_MAX = 80;

export default function NewConversationScreen() {
  const router = useRouter();
  const qc = useQueryClient();

  const friendsQ = useGetV1Friends({ limit: 100 }, { query: { staleTime: 30_000 } });
  const data = friendsQ.data as InternalHandlerHttpFriendListResponse | undefined;
  // Memoise the array so downstream useMemo deps stay referentially
  // stable across renders that don't actually touch the data.
  const friends = React.useMemo(() => data?.data ?? [], [data]);

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
  const canCreate = !creating && selectedIds.size >= 1 && (!isGroup || groupName.trim().length > 0);

  const toggleSelect = React.useCallback((userId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(userId)) next.delete(userId);
      else next.add(userId);
      return next;
    });
  }, []);

  const onCancel = React.useCallback(() => {
    router.back();
  }, [router]);

  const onCreate = React.useCallback(async () => {
    if (!canCreate) return;
    const memberIds = Array.from(selectedIds);
    setCreating(true);
    try {
      const res = (await create.mutateAsync({
        data: isGroup
          ? { type: 'group', member_ids: memberIds, name: groupName.trim() }
          : { type: 'direct', member_ids: memberIds },
      })) as InternalHandlerHttpConversationResponse | undefined;
      await qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() });
      toast.success(isGroup ? 'Group created' : 'Conversation started');
      // replace, not push: back from the thread goes to the chats
      // list, not back to a stale modal.
      if (res?.id) router.replace(`/conversations/${res.id}`);
      else router.back();
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
    return <FullPaneLoading />;
  }
  if (friends.length === 0) {
    return <NoFriends onClose={onCancel} />;
  }

  return (
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
    <View className="flex-row items-center justify-between border-b border-border bg-card px-4 py-3">
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
            className="flex-row items-center gap-2 rounded-full border border-border bg-muted/40 px-3 py-1.5 active:bg-muted">
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
        {selected ? <Check size={14} color="#fff" /> : null}
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
