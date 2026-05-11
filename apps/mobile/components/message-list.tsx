// Phase 6.1 — message list inside a conversation thread.
//
// Reads from the §6.4-style paginated /v1/conversations/{id}/messages
// endpoint via useInfiniteMessages. The API returns reverse-chrono
// (newest first); the hook's pages flatten into a single array in
// that same newest-first order, with `next_cursor` walking back
// through older rows.
//
// FlashList v2 dropped the `inverted` prop in favour of
// `maintainVisibleContentPosition: { startRenderingFromBottom }`
// (see the v2-migration notes). The recipe for chat is:
//
//   1. Reverse the flattened pages so the array is oldest → newest
//      (data[0] = oldest currently loaded, data[N-1] = newest).
//   2. Render data straight through — FlashList starts at the
//      bottom, the scroll anchors there, and the user lands on
//      the newest message.
//   3. `onStartReached` fires at the top of the scroll = the user
//      is reading history → fetchNextPage pulls the next older
//      page; FlashList's MVCP keeps the visible row pinned while
//      the new rows are prepended.
//
// Per-bubble metadata is then read in the natural direction:
//
//   - showSenderLabel = head of a chronological streak =
//     data[i-1]?.sender_id !== data[i].sender_id (the older
//     neighbor is a different sender, or there isn't one).
//   - showAvatar = tail of a chronological streak =
//     data[i+1]?.sender_id !== data[i].sender_id (the newer
//     neighbor is different, or this is the newest message).
//     Mirrors iMessage / Discord — avatar pins to the streak's tail.
//
// Group conversations show the sender label + avatar slot.
// DMs hide both — the screen header already names the peer.
import * as React from 'react';
import { ActivityIndicator, View } from 'react-native';

import { MessageBubble } from '@/components/message-bubble';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { flatten, useInfiniteMessages } from '@/lib/api/use-infinite';
import type {
  InternalHandlerHttpConversationMemberRow,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpMessageResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type Message = InternalHandlerHttpMessageResponse;

type Props = {
  conversationId: string;
  // Caller's user id, used to pick "mine" vs "theirs" per bubble.
  // Undefined while useGetV1AuthMe is still loading — every bubble
  // renders as "theirs" until the id arrives (one tick).
  myUserId: string | undefined;
  // Drives the per-row sender-label decision. DM = false hides the
  // label everywhere (the screen header already names the peer);
  // group = true surfaces the label at the top of each streak.
  isGroup: boolean;
  // Cached members from the parent screen — lets us resolve a
  // message's sender_id into a display name + avatar without an
  // extra users fetch.
  members: InternalHandlerHttpConversationMemberRow[];
};

export function MessageList({ conversationId, myUserId, isGroup, members }: Props) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  const messagesQ = useInfiniteMessages(conversationId, undefined, {
    query: { staleTime: 15_000 },
  });

  // flatten() returns newest-first because that's the API ordering.
  // Reverse once so the array reads oldest → newest for the chat
  // layout — see the file header for the rationale.
  const messages = React.useMemo(() => {
    const { data } = flatten<Message, InternalHandlerHttpMessageListResponse>(
      messagesQ.data?.pages
    );
    return data.slice().reverse();
  }, [messagesQ.data]);

  // Sender lookup once per render. The members array is small
  // (cap-25 per group; 2 in DMs), so a plain Map is cheaper than
  // anything fancier.
  const senderByUserId = React.useMemo(() => {
    const m = new Map<string, InternalHandlerHttpConversationMemberRow['user']>();
    for (const row of members) {
      if (row.user?.id) m.set(row.user.id, row.user);
    }
    return m;
  }, [members]);

  // Older history loads when the user scrolls to the TOP of the
  // list — onStartReached is the v2 equivalent of inverted+onEndReached.
  const onStartReached = React.useCallback(() => {
    if (messagesQ.hasNextPage && !messagesQ.isFetchingNextPage) {
      void messagesQ.fetchNextPage();
    }
  }, [messagesQ]);

  // Loading: cold first paint.
  if (messagesQ.isLoading && !messagesQ.data) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator color={fg} />
      </View>
    );
  }

  // Error: cold failure (no data ever landed). Per-page failures
  // mid-scroll surface in the footer; this branch only covers the
  // "screen mounts but never paints" case.
  if (messagesQ.isError && messages.length === 0) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-6">
        <Text variant="muted" className="text-center">
          Couldn&apos;t load this conversation. Pull down or come back in a moment.
        </Text>
      </View>
    );
  }

  // Empty: a brand-new conversation with zero messages. The composer
  // (Phase 6.2) lives below this component; the placeholder here
  // just acknowledges the empty thread without pretending to be a
  // call to action.
  if (messages.length === 0) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-6">
        <Text variant="muted" className="text-center">
          No messages yet. Send the first one.
        </Text>
      </View>
    );
  }

  return (
    <List<Message>
      data={messages}
      keyExtractor={(m, i) => m.id ?? `idx-${i}`}
      // Anchor at the bottom on first paint so the user lands on
      // the newest message; lets older content prepend cleanly as
      // the cursor walks back through history.
      maintainVisibleContentPosition={{
        startRenderingFromBottom: true,
        autoscrollToBottomThreshold: 0.2,
      }}
      onStartReachedThreshold={0.5}
      onStartReached={onStartReached}
      // Header sits at the TOP of the scroll — natural home for
      // the older-page spinner.
      ListHeaderComponent={
        messagesQ.isFetchingNextPage ? (
          <View className="items-center py-3">
            <ActivityIndicator color={mutedFg} />
          </View>
        ) : null
      }
      renderItem={({ item, index }) => {
        const senderId = item.sender_id ?? '';
        const mine = !!myUserId && senderId === myUserId;
        // Array runs oldest → newest, so the older neighbor sits
        // at index - 1 and the newer one at index + 1.
        const olderNeighbor = messages[index - 1];
        const newerNeighbor = messages[index + 1];
        const sameAsOlder = olderNeighbor?.sender_id === senderId;
        const sameAsNewer = newerNeighbor?.sender_id === senderId;
        const sender = senderId ? senderByUserId.get(senderId) : undefined;
        // Always pass sender identity in group threads so the avatar
        // fallback resolves to initials even on mid-streak bubbles.
        // The streak-head label is gated separately via
        // showSenderLabel.
        return (
          <MessageBubble
            body={item.body}
            createdAt={item.created_at}
            editedAt={item.edited_at}
            isDeleted={item.is_deleted}
            mine={mine}
            senderName={isGroup ? (sender?.display_name ?? undefined) : undefined}
            senderUsername={isGroup ? (sender?.username ?? undefined) : undefined}
            senderAvatarUrl={isGroup ? (sender?.avatar_url ?? undefined) : undefined}
            showSenderLabel={isGroup && !sameAsOlder}
            // Avatar slot anchors to the streak's tail (newest
            // bubble in a same-sender burst), or to the freshest
            // message overall.
            showAvatar={isGroup && !sameAsNewer}
            testID={item.id ? `message-${item.id}` : undefined}
          />
        );
      }}
    />
  );
}
