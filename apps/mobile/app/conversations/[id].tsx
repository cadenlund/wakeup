// Stub thread screen. Phase 5.2 fills in the message list, 5.3
// adds the composer, 5.5 adds the typing indicator, etc. For 5.1
// we just need a route to push to from the conversations list so
// taps don't dead-end — this placeholder reads the conversation
// row from the list cache (so we have a title without an extra
// fetch) and shows an "in progress" message.
import { Stack, useLocalSearchParams } from 'expo-router';
import { MessageCircle } from 'lucide-react-native';
import * as React from 'react';
import { View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1ConversationsQueryKey,
  useGetV1ConversationsId,
} from '@/lib/api/hooks/conversations/conversations';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
} from '@/lib/api/model';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function ConversationThreadScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;

  // The list cache already has the row we want; pull it from there
  // before falling back to a per-id fetch, so the thread title
  // appears immediately on the push transition.
  const qc = useQueryClient();
  const cachedRow = React.useMemo<InternalHandlerHttpConversationResponse | undefined>(() => {
    const list = qc.getQueryData<InternalHandlerHttpConversationListResponse>(
      getGetV1ConversationsQueryKey({ limit: 100 })
    );
    return list?.data?.find((c) => c.id === id);
  }, [qc, id]);

  const detailQ = useGetV1ConversationsId(id ?? '', {
    query: { enabled: !!id && !cachedRow, staleTime: 30_000 },
  });
  const detail = detailQ.data as InternalHandlerHttpConversationResponse | undefined;
  const conversation = cachedRow ?? detail;

  const title = computeTitle(conversation, me?.id);
  const mutedFg = useThemeColor('muted-foreground');

  return (
    <>
      <Stack.Screen options={{ title, headerBackTitle: 'Chats' }} />
      <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
        <MessageCircle size={48} color={mutedFg} />
        <Text variant="h3" className="text-center">
          {title}
        </Text>
        <Text variant="muted" className="text-center">
          The message thread lands in Phase 5.2.
        </Text>
      </View>
    </>
  );
}

function computeTitle(
  c: InternalHandlerHttpConversationResponse | undefined,
  myUserId: string | undefined
): string {
  if (!c) return 'Conversation';
  if (c.type === 'direct') {
    const other = (c.members ?? []).find((m) => m.user?.id !== myUserId)?.user;
    return other?.display_name?.trim() || other?.username?.trim() || 'Direct message';
  }
  return c.name?.trim() || 'Group';
}
