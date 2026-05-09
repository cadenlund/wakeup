// Phase 4.2 — Friends tab. Three sections rendered through a single
// FlashList:
//   1. Accepted Friends — keyset-paginated by `accepted_at DESC`,
//      enriched with presence so each row shows a status dot.
//   2. Incoming Requests — pending friend requests addressed to me.
//   3. Outgoing Requests — pending requests I've sent.
//
// Section dividers are header items in the same flat list rather
// than separate <List>s — keeps recycling working across the whole
// screen and means a single scroll position holds all three lists.
//
// Actions (accept/decline/unfriend/block) ship in 4.4; add-friend
// search ships in 4.3; pull-to-refresh wraps everything in 4.5. The
// row is already a Pressable so those phases hang behaviour off it
// without restructuring.
import { Users } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, View } from 'react-native';

import { FriendRow } from '@/components/friend-row';
import { EmptyState } from '@/components/ui/empty-state';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { useGetV1Friends, useGetV1FriendsRequests } from '@/lib/api/hooks/friends/friends';
import { useGetV1PresenceFriends } from '@/lib/api/hooks/presence/presence';
import type {
  InternalHandlerHttpFriendListResponse,
  InternalHandlerHttpFriendRequestsResponse,
  InternalHandlerHttpFriendshipResponse,
  InternalHandlerHttpPresenceListResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type Friendship = InternalHandlerHttpFriendshipResponse;

// Discriminated row union — FlashList's renderItem switches on `kind`.
type Row =
  | { kind: 'header'; key: string; title: string; count: number }
  | { kind: 'friend'; key: string; friendship: Friendship; presence?: string }
  | { kind: 'request'; key: string; friendship: Friendship; direction: 'incoming' | 'outgoing' }
  | { kind: 'empty'; key: string; subtitle: string };

export default function FriendsScreen() {
  const friendsQ = useGetV1Friends({ limit: 100 }, { query: { staleTime: 30_000 } });
  const requestsQ = useGetV1FriendsRequests({ query: { staleTime: 30_000 } });
  const presenceQ = useGetV1PresenceFriends({ query: { staleTime: 15_000 } });

  // apiFetch returns the unwrapped JSON body; orval types the response
  // as the {data, status, headers} envelope. Cast to the runtime shape
  // (same pattern as auth-gate.tsx).
  const friendsData = friendsQ.data as InternalHandlerHttpFriendListResponse | undefined;
  const requestsData = requestsQ.data as InternalHandlerHttpFriendRequestsResponse | undefined;
  const presenceData = presenceQ.data as InternalHandlerHttpPresenceListResponse | undefined;

  const presenceByUser = React.useMemo(() => {
    const m = new Map<string, string>();
    for (const p of presenceData?.data ?? []) {
      if (p.user_id && p.status) m.set(p.user_id, p.status);
    }
    return m;
  }, [presenceData]);

  const rows = React.useMemo<Row[]>(() => {
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
      for (const f of incoming) {
        out.push({
          kind: 'request',
          key: `req:in:${f.id ?? f.user?.id ?? Math.random()}`,
          friendship: f,
          direction: 'incoming',
        });
      }
    }

    if (outgoing.length > 0) {
      out.push({
        kind: 'header',
        key: 'h:outgoing',
        title: 'Sent requests',
        count: outgoing.length,
      });
      for (const f of outgoing) {
        out.push({
          kind: 'request',
          key: `req:out:${f.id ?? f.user?.id ?? Math.random()}`,
          friendship: f,
          direction: 'outgoing',
        });
      }
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
        subtitle:
          'Find someone in 4.3 — for now, accepted friends will land here once a request is approved.',
      });
    } else {
      for (const f of friends) {
        out.push({
          kind: 'friend',
          key: `friend:${f.id ?? f.user?.id ?? Math.random()}`,
          friendship: f,
          presence: f.user?.id ? presenceByUser.get(f.user.id) : undefined,
        });
      }
    }

    return out;
  }, [friendsData, requestsData, presenceByUser]);

  const isInitialLoad =
    (friendsQ.isLoading && !friendsQ.data) || (requestsQ.isLoading && !requestsQ.data);

  if (isInitialLoad) {
    return <FriendsLoading />;
  }

  // No data at all — neither friends nor pending requests. Show the
  // catch-all empty state instead of the per-section "Friends (0)"
  // header so the first run feels welcoming, not like a broken UI.
  const allEmpty =
    (friendsData?.data?.length ?? 0) === 0 &&
    (requestsData?.incoming?.length ?? 0) === 0 &&
    (requestsData?.outgoing?.length ?? 0) === 0;
  if (allEmpty) {
    return <FriendsAllEmpty />;
  }

  return (
    <View className="flex-1 bg-background">
      <List
        data={rows}
        keyExtractor={(item) => item.key}
        renderItem={({ item }) => <RenderedRow row={item} />}
        // Headers are taller than rows; FlashList v2 uses runtime
        // measurement so we don't pin item sizes here.
      />
    </View>
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
        subtitle="Add-friend search lands in Phase 4.3."
      />
    </View>
  );
}
