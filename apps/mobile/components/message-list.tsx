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
import { ActivityIndicator, LayoutAnimation, View } from 'react-native';

import { MessageBubble } from '@/components/message-bubble';
import {
  MessageActionPopover,
  type MessageActionTarget,
} from '@/components/message-action-popover';
import { AGGREGATE_GAP_MS, TimeDivider } from '@/components/time-divider';
import { List, type ListRef } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { flatten, useInfiniteMessages } from '@/lib/api/use-infinite';
import type {
  InternalHandlerHttpConversationMemberRow,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpMessageResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useTypingUserIds } from '@/lib/typing/store';
import { useDeleteMessage } from '@/lib/use-delete-message';
import { useMarkReadOnFocus } from '@/lib/use-mark-read';
import { useReduceMotion } from '@/lib/use-reduce-motion';
import type { LocalSendStatus } from '@/lib/use-send-message';

type Message = InternalHandlerHttpMessageResponse;

// The FlashList renders a flat list of dividers + message bubbles.
type Row =
  | { kind: 'divider'; key: string; iso: string }
  | {
      kind: 'message';
      key: string;
      message: Message;
      sameAsOlder: boolean;
      sameAsNewer: boolean;
    };

function memberName(u: InternalHandlerHttpUserResponse): string {
  return u.display_name?.trim() || u.username?.trim() || 'Someone';
}

// "Seen by Ada" / "Seen by Ada and Ben" / "Seen by Ada, Ben and 2 others".
function formatSeenBy(users: InternalHandlerHttpUserResponse[]): string {
  const names = users.map(memberName);
  if (names.length === 1) return `Seen by ${names[0]}`;
  if (names.length === 2) return `Seen by ${names[0]} and ${names[1]}`;
  const rest = names.length - 2;
  return `Seen by ${names[0]}, ${names[1]} and ${rest} other${rest === 1 ? '' : 's'}`;
}

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
  // Per-bubble send status from useSendMessage. Absence of an
  // entry == delivered. Sending / failed both render an inline
  // status hint under the bubble; failed adds the retry tap.
  sendStatusByTempId: Map<string, { status: LocalSendStatus; body: string }>;
  // Re-fires a failed send for `tempId` reusing the same
  // Idempotency-Key. Wired to the inline "Retry" affordance.
  onRetrySend: (tempId: string) => void;
};

export function MessageList({
  conversationId,
  myUserId,
  isGroup,
  members,
  sendStatusByTempId,
  onRetrySend,
}: Props) {
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const reduceMotion = useReduceMotion();

  // Long-press action popover: which bubble was pressed (null = closed).
  const [actionTarget, setActionTarget] = React.useState<MessageActionTarget | null>(null);
  const { deleteMessage } = useDeleteMessage(conversationId);
  // The typing indicator (sibling below this list) takes vertical
  // space when it appears, shrinking this list's viewport — so we
  // re-pin the bottom when typing starts, otherwise the last message
  // gets clipped under the typing bubble.
  const someoneTyping = useTypingUserIds(conversationId).length > 0;
  const listRef = React.useRef<ListRef<Row>>(null);
  React.useEffect(() => {
    if (!someoneTyping) return;
    const id = requestAnimationFrame(() => listRef.current?.scrollToEnd({ animated: true }));
    return () => cancelAnimationFrame(id);
  }, [someoneTyping]);

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

  // When a new message lands at the *bottom* (incoming, or your own
  // optimistic placeholder), ease the layout so the thread lifts up
  // smoothly instead of teleporting. Older-page prepends (pagination)
  // leave the last id unchanged, so they don't trigger this. Done
  // during render — `configureNext` must run before React commits
  // the new row.
  const lastMessageId = messages.length > 0 ? messages[messages.length - 1]?.id : undefined;
  const prevLastMessageId = React.useRef<string | undefined>(undefined);
  if (
    !reduceMotion &&
    lastMessageId &&
    prevLastMessageId.current &&
    lastMessageId !== prevLastMessageId.current
  ) {
    LayoutAnimation.configureNext({
      duration: 220,
      create: { type: 'easeInEaseOut', property: 'opacity' },
      update: { type: 'easeInEaseOut' },
    });
  }
  if (lastMessageId) prevLastMessageId.current = lastMessageId;

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

  // Each loaded message's index, for read-pointer comparisons.
  const msgIdxById = React.useMemo(() => {
    const m = new Map<string, number>();
    messages.forEach((msg, i) => {
      if (msg.id) m.set(msg.id, i);
    });
    return m;
  }, [messages]);

  // Each member's read-pointer position in the loaded window (-1 if
  // their last_read_message_id is null or older than what's loaded).
  // Self excluded — own pointer isn't a "receipt".
  const readPointerIdxByUser = React.useMemo(() => {
    const m = new Map<string, number>();
    for (const row of members) {
      const uid = row.user?.id;
      if (!uid || uid === myUserId) continue;
      const readId = row.last_read_message_id;
      m.set(uid, readId ? (msgIdxById.get(readId) ?? -1) : -1);
    }
    return m;
  }, [members, msgIdxById, myUserId]);

  // Per-message read-receipt caption:
  //   - DM: a single caption under your last delivered sent message
  //     — "Delivered" until the peer's read pointer reaches it,
  //     then "Seen".
  //   - Group: under EVERY message at a member's read frontier —
  //     "Seen by Ada" / "Seen by Ada and Ben" / "Seen by Ada, Ben
  //     and 2 others". A member only shows at their frontier (older
  //     messages are implicitly read); the message's own sender is
  //     never listed for their message.
  // Members with a NULL read pointer never opened the thread and
  // contribute nothing. Self is skipped (own receipts are noise).
  const receiptByMessageId = React.useMemo(() => {
    const out = new Map<string, string>();
    if (messages.length === 0) return out;

    if (isGroup) {
      const frontier = new Map<string, InternalHandlerHttpUserResponse[]>();
      for (const row of members) {
        const uid = row.user?.id;
        const readId = row.last_read_message_id;
        if (!uid || !readId || uid === myUserId || !row.user) continue;
        const arr = frontier.get(readId);
        if (arr) arr.push(row.user);
        else frontier.set(readId, [row.user]);
      }
      for (const msg of messages) {
        if (!msg.id) continue;
        const seers = (frontier.get(msg.id) ?? []).filter((u) => u.id !== msg.sender_id);
        if (seers.length > 0) out.set(msg.id, formatSeenBy(seers));
      }
      return out;
    }

    // DM: a single caption under your last delivered sent message.
    let lastSentIdx = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
      const msg = messages[i];
      if (msg?.id && msg.sender_id === myUserId && !sendStatusByTempId.has(msg.id)) {
        lastSentIdx = i;
        break;
      }
    }
    const lastSentId = lastSentIdx >= 0 ? messages[lastSentIdx]?.id : undefined;
    if (!lastSentId) return out;
    const otherReadId = members.find(
      (row) => row.user?.id && row.user.id !== myUserId
    )?.last_read_message_id;
    const otherPtrIdx = otherReadId ? (msgIdxById.get(otherReadId) ?? -1) : -1;
    out.set(lastSentId, otherPtrIdx >= lastSentIdx ? 'Seen' : 'Delivered');
    return out;
  }, [messages, members, isGroup, myUserId, sendStatusByTempId, msgIdxById]);

  // Mark-read on focus: post the latest *delivered* message id to
  // the backend so the per-member read pointer advances. The
  // newest rendered row can be an optimistic placeholder (its id
  // is a client temp id, present in sendStatusByTempId) — posting
  // that would set the read pointer to a non-existent message. Walk
  // back from the end and skip anything that's still in flight /
  // failed. The hook gates re-posts internally — re-focusing on
  // the same screen with no new delivered message is a no-op.
  const latestDeliveredMessageId = React.useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const id = messages[i]?.id;
      if (id && !sendStatusByTempId.has(id)) return id;
    }
    return undefined;
  }, [messages, sendStatusByTempId]);
  useMarkReadOnFocus(conversationId, latestDeliveredMessageId);

  // Build the interleaved row list:
  // - A divider precedes every message that starts a new burst
  //   (the first message overall, or one whose timestamp is more
  //   than AGGREGATE_GAP_MS after the previous message). Messages
  //   within the same burst share the single divider at the top.
  // - Each message row carries pre-computed sameAsOlder /
  //   sameAsNewer flags. Neighbor lookups happen here (in the
  //   `messages` array, not the row array, because the row array
  //   has dividers interspersed and "previous row" wouldn't be a
  //   sibling message).
  const rows = React.useMemo(() => {
    type DividerRow = { kind: 'divider'; key: string; iso: string };
    type MessageRow = {
      kind: 'message';
      key: string;
      message: Message;
      sameAsOlder: boolean;
      sameAsNewer: boolean;
    };
    const out: (DividerRow | MessageRow)[] = [];
    let lastTs = 0;
    for (let i = 0; i < messages.length; i++) {
      const m = messages[i];
      const iso = m.created_at;
      const t = iso ? Date.parse(iso) : NaN;
      if (Number.isFinite(t) && (lastTs === 0 || t - lastTs >= AGGREGATE_GAP_MS)) {
        out.push({ kind: 'divider', key: `div-${m.id ?? t}`, iso: iso as string });
        lastTs = t;
      } else if (Number.isFinite(t)) {
        lastTs = t;
      }
      const senderId = m.sender_id ?? '';
      const sameAsOlder = messages[i - 1]?.sender_id === senderId;
      const sameAsNewer = messages[i + 1]?.sender_id === senderId;
      out.push({
        kind: 'message',
        key: m.id ?? `idx-${out.length}`,
        message: m,
        sameAsOlder,
        sameAsNewer,
      });
    }
    return out;
  }, [messages]);

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

  const list = (
    <List<Row>
      ref={listRef}
      data={rows}
      // Stable per-row keys. Dividers carry a synthetic
      // `div-<id>` key; messages reuse the server-issued id.
      // FlashList depends on the key being unique across the
      // whole data array, not just per type.
      keyExtractor={(row) => row.key}
      // getItemType lets FlashList recycle dividers separately
      // from message bubbles — without this, the recycler would
      // try to reuse a divider's tiny height for a full bubble
      // and vice-versa, causing flicker on prepend.
      getItemType={(row) => row.kind}
      // FlashList memoizes rows by `item`; the `lifted` prop flips
      // when the action popover opens/closes on a bubble, which
      // doesn't change the row's `item`. extraData forces a
      // re-render of the visible rows when the target changes so
      // the in-thread copy actually hides while the popover holds
      // it (otherwise a ghost duplicate stays visible).
      extraData={actionTarget?.id}
      // Breathing room so the newest bubble doesn't sit flush
      // against the composer's top edge.
      contentContainerStyle={{ paddingBottom: 8 }}
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
      renderItem={({ item }) => {
        if (item.kind === 'divider') {
          return <TimeDivider iso={item.iso} />;
        }
        const m = item.message;
        const senderId = m.sender_id ?? '';
        const mine = !!myUserId && senderId === myUserId;
        const sender = senderId ? senderByUserId.get(senderId) : undefined;
        // Always pass sender identity in group threads so the avatar
        // fallback resolves to initials even on mid-streak bubbles.
        // The streak-head label is gated separately via
        // showSenderLabel.
        // Long-press opens the action popover — on any real
        // message, including deleted ones (you can still see when
        // it was sent + react). Only pending/failed optimistic
        // placeholders are exempt: there's nothing to copy/delete
        // on an unsent bubble. The bubble measures its own window
        // rect and passes it up so the popover anchors to it.
        const isPlaceholder = !!m.id && sendStatusByTempId.has(m.id);
        const onLongPress =
          m.id && !isPlaceholder
            ? (rect: { x: number; y: number; width: number; height: number } | undefined) => {
                // Who's read this message — every member (≠ self,
                // ≠ this message's sender) whose read pointer is at
                // or past this message. The popover lists them.
                const msgIdx = msgIdxById.get(m.id as string) ?? -1;
                const seenBy =
                  msgIdx < 0
                    ? []
                    : members.flatMap((row) => {
                        const u = row.user;
                        if (!u?.id || u.id === myUserId || u.id === m.sender_id) return [];
                        const ptr = readPointerIdxByUser.get(u.id) ?? -1;
                        if (ptr < msgIdx) return [];
                        return [
                          {
                            id: u.id,
                            name: u.display_name?.trim() || u.username?.trim() || 'Someone',
                            avatarUrl: u.avatar_url,
                          },
                        ];
                      });
                setActionTarget({
                  id: m.id as string,
                  body: m.body ?? '',
                  mine,
                  isDeleted: !!m.is_deleted,
                  createdAt: m.created_at,
                  rect,
                  seenBy,
                });
              }
            : undefined;
        return (
          <MessageBubble
            body={m.body}
            isDeleted={m.is_deleted}
            mine={mine}
            isGroup={isGroup}
            reduceMotion={reduceMotion}
            // Per-bubble send status only applies to "mine" rows
            // — the recipient never has a local send state for
            // someone else's message.
            sendStatus={mine && m.id ? sendStatusByTempId.get(m.id)?.status : undefined}
            onRetrySend={mine && m.id ? () => onRetrySend(m.id as string) : undefined}
            onLongPress={onLongPress}
            // Hide the in-thread copy while the popover holds it —
            // its pinned snapshot is the visible one (no duplicate).
            lifted={!!actionTarget && !!m.id && actionTarget.id === m.id}
            // Read-receipt caption. DM: only on your last sent
            // message ("Delivered"/"Seen"). Group: on every message
            // at someone's read frontier ("Seen by …"), including
            // other people's messages.
            receiptText={m.id ? receiptByMessageId.get(m.id) : undefined}
            senderName={isGroup ? (sender?.display_name ?? undefined) : undefined}
            senderUsername={isGroup ? (sender?.username ?? undefined) : undefined}
            senderAvatarUrl={isGroup ? (sender?.avatar_url ?? undefined) : undefined}
            showSenderLabel={isGroup && !item.sameAsOlder}
            // Avatar slot anchors to the streak's tail (newest
            // bubble in a same-sender burst), or to the freshest
            // message overall.
            showAvatar={isGroup && !item.sameAsNewer}
            testID={m.id ? `message-${m.id}` : undefined}
          />
        );
      }}
    />
  );

  return (
    <>
      {list}
      <MessageActionPopover
        target={actionTarget}
        isGroup={isGroup}
        onClose={() => setActionTarget(null)}
        onDelete={deleteMessage}
        testID="message-action-popover"
      />
    </>
  );
}
