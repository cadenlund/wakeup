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
import { TypingIndicator } from '@/components/typing-indicator';
import {
  MessageActionPopover,
  type MessageActionTarget,
} from '@/components/message-action-popover';
import { AGGREGATE_GAP_MS, TimeDivider } from '@/components/time-divider';
import { List } from '@/components/ui/list';
import { Text } from '@/components/ui/text';
import { flatten, useInfiniteMessages } from '@/lib/api/use-infinite';
import type {
  InternalHandlerHttpConversationMemberRow,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpMessageResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useDeleteMessage } from '@/lib/use-delete-message';
import { useMarkReadOnFocus } from '@/lib/use-mark-read';
import type { LocalSendStatus } from '@/lib/use-send-message';

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

  // Long-press action popover: which bubble was pressed (null = closed).
  const [actionTarget, setActionTarget] = React.useState<MessageActionTarget | null>(null);
  const { deleteMessage } = useDeleteMessage(conversationId);

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

  // Read-receipt index: messageId → users who have read up to (and
  // exactly at) that message. We render the resulting avatar row
  // under "mine" bubbles only — that's the side the caller cares
  // about ("did they see it yet?"). Members with NULL last_read
  // never opened the thread and contribute nothing.
  //
  // Note this surfaces each member at exactly one bubble — their
  // "current pointer." Older bubbles get implicit reads (the
  // pointer has moved past them), matching the iMessage / Discord
  // convention where only the latest read receipt is shown.
  const readByMessageId = React.useMemo(() => {
    const m = new Map<string, InternalHandlerHttpUserResponse[]>();
    if (!isGroup) return m;
    for (const row of members) {
      const uid = row.user?.id;
      const readId = row.last_read_message_id;
      if (!uid || !readId) continue;
      if (uid === myUserId) continue; // skip self — own receipts are noise
      const arr = m.get(readId);
      if (arr) arr.push(row.user!);
      else m.set(readId, [row.user!]);
    }
    return m;
  }, [members, isGroup, myUserId]);

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

  type Row =
    | { kind: 'divider'; key: string; iso: string }
    | {
        kind: 'message';
        key: string;
        message: Message;
        sameAsOlder: boolean;
        sameAsNewer: boolean;
      };
  const list = (
    <List<Row>
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
      // Footer sits at the BOTTOM of the scroll — the typing
      // indicator lives here so it scrolls with the messages and
      // the list makes room for it (it renders null when nobody's
      // typing).
      ListFooterComponent={
        <TypingIndicator conversationId={conversationId} members={members} isGroup={isGroup} />
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
            ? (rect: { x: number; y: number; width: number; height: number } | undefined) =>
                setActionTarget({
                  id: m.id as string,
                  body: m.body ?? '',
                  mine,
                  isDeleted: !!m.is_deleted,
                  createdAt: m.created_at,
                  rect,
                })
            : undefined;
        return (
          <MessageBubble
            body={m.body}
            isDeleted={m.is_deleted}
            mine={mine}
            isGroup={isGroup}
            // Per-bubble send status only applies to "mine" rows
            // — the recipient never has a local send state for
            // someone else's message.
            sendStatus={mine && m.id ? sendStatusByTempId.get(m.id)?.status : undefined}
            onRetrySend={mine && m.id ? () => onRetrySend(m.id as string) : undefined}
            onLongPress={onLongPress}
            // Hide the in-thread copy while the popover holds it —
            // its pinned snapshot is the visible one (no duplicate).
            lifted={!!actionTarget && !!m.id && actionTarget.id === m.id}
            // Read-receipt avatars only appear under "mine" bubbles
            // in group threads (per §6.3 spec). The bubble component
            // ignores `readBy` for non-mine rows.
            readBy={mine && m.id ? readByMessageId.get(m.id) : undefined}
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
        onClose={() => setActionTarget(null)}
        onDelete={deleteMessage}
        testID="message-action-popover"
      />
    </>
  );
}
