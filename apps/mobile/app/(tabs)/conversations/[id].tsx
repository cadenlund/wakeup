// Stub thread screen. Phase 5.2 fills in the message list, 5.3
// adds the composer, 5.5 adds the typing indicator, etc. For 5.1
// we just need a route to push to from the conversations list so
// taps don't dead-end — this placeholder reads the conversation
// row from the list cache (so we have a title without an extra
// fetch) and shows an "in progress" message.
import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import { MessageCircle, MoreVertical } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';

import { ConversationActionMenu } from '@/components/conversation-action-menu';
import { MuteSheet } from '@/components/mute-sheet';
import { Text } from '@/components/ui/text';
import { ThemedBackButton } from '@/components/ui/themed-back-button';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1ConversationsQueryKey,
  useGetV1ConversationsId,
} from '@/lib/api/hooks/conversations/conversations';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
} from '@/lib/api/model';
import { isCurrentlyMuted } from '@/lib/conversation-display';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { useConversationPinMute } from '@/lib/use-conversation-pin-mute';
import { useLeaveConversation } from '@/lib/use-conversation-leave';

export default function ConversationThreadScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;

  // The list cache already has the row we want; pull it from there
  // before falling back to a per-id fetch, so the thread title
  // appears immediately on the push transition. Walk every cached
  // /v1/conversations query — the chats tab uses an infinite-query
  // shape (`pages[].data[]`).
  const qc = useQueryClient();
  const cachedRow = React.useMemo<InternalHandlerHttpConversationResponse | undefined>(() => {
    if (!id) return undefined;
    const prefix = getGetV1ConversationsQueryKey()[0];
    type CachedList =
      | InternalHandlerHttpConversationListResponse
      | InfiniteData<InternalHandlerHttpConversationListResponse>;
    const isInfinite = (
      d: CachedList
    ): d is InfiniteData<InternalHandlerHttpConversationListResponse> =>
      Array.isArray((d as InfiniteData<InternalHandlerHttpConversationListResponse>).pages);
    for (const [, data] of qc.getQueriesData<CachedList>({ queryKey: [prefix] })) {
      if (!data) continue;
      if (isInfinite(data)) {
        for (const page of data.pages) {
          const hit = page.data?.find((c) => c.id === id);
          if (hit) return hit;
        }
      } else {
        const hit = data.data?.find((c) => c.id === id);
        if (hit) return hit;
      }
    }
    return undefined;
  }, [qc, id]);

  const detailQ = useGetV1ConversationsId(id ?? '', {
    query: { enabled: !!id && !cachedRow, staleTime: 30_000 },
  });
  const detail = detailQ.data as InternalHandlerHttpConversationResponse | undefined;
  const conversation = cachedRow ?? detail;

  const title = computeTitle(conversation, me?.id);
  const mutedFg = useThemeColor('muted-foreground');
  const fg = useThemeColor('foreground');
  const card = useThemeColor('card');
  const border = useThemeColor('border');

  // Header three-dots reuses the same ConversationActionMenu the
  // chats tab opens on row long-press. State machine: 'menu' is
  // the row of Pin/Mute/Manage/Leave entries; 'mute' is the
  // duration sheet that opens when the user picks "Mute…".
  const router = useRouter();
  const [sheet, setSheet] = React.useState<'menu' | 'mute' | null>(null);
  const closeSheet = React.useCallback(() => setSheet(null), []);
  const openMute = React.useCallback(() => setSheet('mute'), []);
  const { togglePin, setMute, unmute } = useConversationPinMute();
  const { leave } = useLeaveConversation();
  const isMuted = isCurrentlyMuted(conversation?.muted_until);
  const isPinned = !!conversation?.pinned_at;
  const isGroup = conversation?.type === 'group';
  const convId = conversation?.id;

  return (
    <>
      <Stack.Screen
        options={{
          title,
          // Native back chevron looks foreign next to the rest of
          // the app's muted-text affordances; replace it with the
          // shared <ThemedBackButton>. headerBackVisible:false
          // suppresses the native one underneath.
          headerLeft: () => <ThemedBackButton label="Chats" testID="conversation-thread-back" />,
          headerBackVisible: false,
          headerRight: () =>
            convId ? (
              <Pressable
                onPress={() => {
                  haptics.tap();
                  setSheet('menu');
                }}
                accessibilityRole="button"
                accessibilityLabel="Conversation actions"
                testID="conversation-thread-more"
                hitSlop={8}
                className="h-9 w-9 items-center justify-center rounded-md active:bg-muted">
                <MoreVertical size={20} color={fg} />
              </Pressable>
            ) : null,
          headerStyle: { backgroundColor: card },
          headerTintColor: fg,
          headerShadowVisible: false,
          // 1px hairline so the themed border still reads on dark
          // mode where headerShadowVisible already hides the native
          // line.
          headerBackground: () => (
            <View
              style={{
                flex: 1,
                backgroundColor: card,
                borderBottomWidth: 1,
                borderBottomColor: border,
              }}
            />
          ),
        }}
      />
      <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
        <MessageCircle size={48} color={mutedFg} />
        <Text variant="h3" className="text-center">
          {title}
        </Text>
        <Text variant="muted" className="text-center">
          The message thread lands in Phase 6.
        </Text>
      </View>

      <ConversationActionMenu
        visible={sheet === 'menu'}
        title={title}
        isPinned={isPinned}
        isMuted={isMuted}
        isGroup={isGroup}
        onTogglePin={() => {
          if (!convId) return;
          togglePin(convId, isPinned);
          closeSheet();
        }}
        onMutePress={openMute}
        onUnmute={() => {
          if (!convId) return;
          unmute(convId);
          closeSheet();
        }}
        onManageMembers={() => {
          if (!convId) return;
          closeSheet();
          setTimeout(() => router.push(`/conversations/${convId}/members`), 0);
        }}
        onLeave={() => {
          if (!convId) return;
          closeSheet();
          // After leaving the conversation is no longer in the
          // user's list — bounce back to the chats tab so the
          // route doesn't dead-end. Only route on success; a
          // failed leave (network blip, etc.) keeps the user
          // here with the toast already surfaced by the hook.
          void leave(convId).then((ok) => {
            if (!ok) return;
            if (router.canGoBack()) router.back();
            else router.replace('/');
          });
        }}
        onClose={closeSheet}
        testID="conversation-thread-action-menu"
      />
      <MuteSheet
        visible={sheet === 'mute'}
        isMuted={isMuted}
        onPickUntil={(until) => {
          if (!convId) return;
          setMute(convId, until);
          closeSheet();
        }}
        onUnmute={() => {
          if (!convId) return;
          unmute(convId);
          closeSheet();
        }}
        onClose={closeSheet}
        testID="conversation-thread-mute-sheet"
      />
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
