// One rendered message bubble. Sender direction picks the side
// (mine = right, theirs = left) and the colour token (mine =
// primary, theirs = card). Group conversations may render a small
// sender label above the bubble — only when the sender changes
// from the previous bubble in the visual stack, so a streak of
// messages from one user reads as one block (Discord / iMessage
// convention).
//
// Deleted rows render the body as italicised "Message deleted" —
// we still draw a placeholder so the conversation history stays
// coherent (gaps would make a reply chain unreadable).
import * as React from 'react';
import { ActivityIndicator, Pressable, View } from 'react-native';

import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import type { InternalHandlerHttpUserResponse } from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import type { LocalSendStatus } from '@/lib/use-send-message';
import { cn } from '@/lib/utils';

type Props = {
  body: string | null | undefined;
  isDeleted: boolean | undefined;
  mine: boolean;
  // True when the surrounding thread is a group. DM threads hide
  // the avatar gutter AND the sender label entirely so "theirs"
  // bubbles hug the left edge (Apple Messages convention). Groups
  // keep the gutter for sender identity.
  isGroup: boolean;
  // Identity for the avatar fallback (always supplied in groups so
  // mid-streak bubbles still resolve to initials when the user has
  // no avatar_url). DMs / "mine" rows leave it undefined.
  senderName?: string;
  senderUsername?: string | null;
  senderAvatarUrl?: string | null;
  // Render the "X said:" label above the bubble. Streak-head only —
  // the caller computes this from the visually-older neighbor.
  showSenderLabel?: boolean;
  // Render the avatar slot in the gutter on the "theirs" side. False
  // for messages in a streak (only the last bubble of a streak shows
  // the avatar); always false for "mine".
  showAvatar?: boolean;
  // Members whose last_read_message_id sits at this exact bubble.
  // Only meaningful on "mine" bubbles in groups — the list builder
  // (<MessageList>) leaves it undefined for everything else. Renders
  // as a row of tiny avatars under the bubble (iMessage convention:
  // only the latest read position per recipient is shown).
  readBy?: InternalHandlerHttpUserResponse[];
  // Send-pipeline status from useSendMessage. `undefined` = the
  // message is delivered (server-issued row); `'sending'` shows
  // a small spinner + "Sending…" caption; `'failed'` swaps in a
  // tappable "Not sent · Retry" affordance that calls onRetrySend.
  sendStatus?: LocalSendStatus;
  onRetrySend?: () => void;
  testID?: string;
};

export function MessageBubble({
  body,
  isDeleted,
  mine,
  isGroup,
  senderName,
  senderUsername,
  senderAvatarUrl,
  showSenderLabel,
  showAvatar,
  readBy,
  sendStatus,
  onRetrySend,
  testID,
}: Props) {
  const displayName = senderName?.trim() || senderUsername?.trim() || undefined;
  const mutedFg = useThemeColor('muted-foreground');
  const destructive = useThemeColor('destructive');
  // Per-bubble timestamps moved to centered <TimeDivider> rows in
  // the list (Apple Messages convention — gaps between message
  // bursts get a divider; individual bubbles stay quiet).
  // edited_at is in the backend response but v1 has no message-edit
  // UI (deferred to v2 — §6.5 context menu lands a stub only), so
  // we don't surface an "edited" indicator yet. The prop stays
  // omitted to avoid drifting away from the locked v1 scope.

  // Avatar gutter only renders in groups. In DMs the "theirs"
  // bubble hugs the left edge so the conversation reads tighter
  // — matches Apple Messages.
  const showGutter = isGroup && !mine;
  return (
    <View
      testID={testID}
      // Row: optional avatar gutter (groups only) + bubble column.
      // "Mine" rows flush right; "theirs" rows align left against
      // either the gutter (groups) or the edge (DMs).
      className={cn('flex-row items-end gap-2 px-3 py-1', mine ? 'justify-end' : 'justify-start')}>
      {showGutter ? (
        <View className="w-8">
          {showAvatar ? (
            <Avatar source={senderAvatarUrl} fallbackName={displayName} size={32} />
          ) : null}
        </View>
      ) : null}

      <View className={cn('max-w-[80%]', mine ? 'items-end' : 'items-start')}>
        {showSenderLabel && !mine && displayName ? (
          <Text variant="muted" className="mb-0.5 px-1 text-xs">
            {displayName}
          </Text>
        ) : null}

        <View
          className={cn(
            'rounded-2xl px-3 py-2',
            mine ? 'rounded-br-sm bg-primary' : 'rounded-bl-sm bg-card'
          )}>
          {isDeleted ? (
            <Text
              className={cn(
                'text-base italic',
                mine ? 'text-primary-foreground/70' : 'text-muted-foreground'
              )}>
              Message deleted
            </Text>
          ) : (
            <Text className={cn('text-base', mine ? 'text-primary-foreground' : 'text-foreground')}>
              {body ?? ''}
            </Text>
          )}
        </View>

        {readBy && readBy.length > 0 ? (
          <View className="mt-0.5 flex-row gap-1 px-1">
            {readBy.map((u) => {
              const name = u.display_name?.trim() || u.username?.trim() || 'Member';
              return <Avatar key={u.id} source={u.avatar_url} fallbackName={name} size={14} />;
            })}
          </View>
        ) : null}

        {mine && sendStatus === 'sending' ? (
          <View className="mt-0.5 flex-row items-center gap-1 px-1">
            <ActivityIndicator size="small" color={mutedFg} />
            <Text variant="muted" className="text-[10px]">
              Sending…
            </Text>
          </View>
        ) : null}

        {mine && sendStatus === 'failed' ? (
          onRetrySend ? (
            <Pressable
              onPress={onRetrySend}
              accessibilityRole="button"
              accessibilityLabel="Retry send"
              testID={testID ? `${testID}-retry` : undefined}
              hitSlop={6}
              className="mt-0.5 flex-row items-center gap-1 px-1 active:opacity-70">
              <Text style={{ color: destructive }} className="text-[10px] font-medium">
                Not sent · Retry
              </Text>
            </Pressable>
          ) : (
            // No retry handler wired (shouldn't happen in practice —
            // every "mine" bubble gets one from <MessageList> — but
            // keep the failure visible rather than rendering a dead
            // tap target).
            <Text style={{ color: destructive }} className="mt-0.5 px-1 text-[10px] font-medium">
              Not sent
            </Text>
          )
        ) : null}
      </View>
    </View>
  );
}
