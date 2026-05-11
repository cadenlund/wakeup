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
import { View } from 'react-native';

import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { formatRelative } from '@/lib/relative-time';
import { cn } from '@/lib/utils';

type Props = {
  body: string | null | undefined;
  createdAt: string | null | undefined;
  isDeleted: boolean | undefined;
  mine: boolean;
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
  testID?: string;
};

export function MessageBubble({
  body,
  createdAt,
  isDeleted,
  mine,
  senderName,
  senderUsername,
  senderAvatarUrl,
  showSenderLabel,
  showAvatar,
  testID,
}: Props) {
  const displayName = senderName?.trim() || senderUsername?.trim() || undefined;
  const time = formatRelative(createdAt);
  // edited_at is in the backend response but v1 has no message-edit
  // UI (deferred to v2 — §6.5 context menu lands a stub only), so
  // we don't surface an "edited" indicator yet. The prop stays
  // omitted to avoid drifting away from the locked v1 scope.

  return (
    <View
      testID={testID}
      // Row: avatar gutter + bubble column. "Mine" rows put the
      // bubble flush right with no avatar; "theirs" rows align
      // bubble against an avatar gutter on the left.
      className={cn('flex-row items-end gap-2 px-3 py-1', mine ? 'justify-end' : 'justify-start')}>
      {!mine ? (
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

        {time ? (
          <Text variant="muted" className="mt-0.5 px-1 text-[10px]">
            {time}
          </Text>
        ) : null}
      </View>
    </View>
  );
}
