// Phase 6.4 — "X is typing…" row for the conversation thread.
//
// Reads `useTypingUserIds` (the WS-fed typing store) and resolves
// ids to display names off the conversation's member list. Renders
// nothing when nobody's typing — zero pixel cost in the common case.
// Sits between the message list and the composer in the thread.
import * as React from 'react';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';
import type { InternalHandlerHttpConversationMemberRow } from '@/lib/api/model';
import { useTypingUserIds } from '@/lib/typing/store';

type Member = InternalHandlerHttpConversationMemberRow;

function nameFor(members: Member[] | undefined, userId: string): string {
  const u = members?.find((m) => m.user?.id === userId)?.user;
  return u?.display_name?.trim() || u?.username?.trim() || 'Someone';
}

export function TypingIndicator({
  conversationId,
  members,
}: {
  conversationId: string;
  members?: Member[];
}): React.ReactElement | null {
  const ids = useTypingUserIds(conversationId);
  if (ids.length === 0) return null;

  let text: string;
  if (ids.length === 1) text = `${nameFor(members, ids[0])} is typing…`;
  else if (ids.length === 2)
    text = `${nameFor(members, ids[0])} and ${nameFor(members, ids[1])} are typing…`;
  else text = 'Several people are typing…';

  return (
    <View
      className="bg-background px-4 py-1"
      accessibilityLiveRegion="polite"
      testID="typing-indicator">
      <Text className="text-xs text-muted-foreground">{text}</Text>
    </View>
  );
}
