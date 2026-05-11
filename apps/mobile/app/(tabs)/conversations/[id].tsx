// Phase 6.1 — conversation thread screen.
// Phase 6.2 — composer + optimistic send.
//
// Renders the §6.4 paginated /v1/conversations/{id}/messages feed
// via the shared <MessageList>, with a <Composer> pinned at the
// bottom that prepends an optimistic placeholder on submit. The
// conversation row is hydrated from the chats-tab list cache first
// (so the title appears instantly on the push transition) with a
// per-id refetch as the fallback when this screen is opened cold
// (deep link / search modal route).
//
// KeyboardAvoidingView wraps both the list and the composer so the
// composer rides up with the soft keyboard. `behavior="padding"` on
// BOTH platforms: with `edgeToEdgeEnabled` (app.json) Android's
// `windowSoftInputMode="adjustResize"` is a no-op — the window
// doesn't resize — so we can't lean on the OS there. `padding`
// works on both because RN's KAV applies bottom padding equal to
// the keyboard height directly. `keyboardVerticalOffset` =
// header + status-bar height so the padding lands at the right spot.
import { useHeaderHeight } from '@react-navigation/elements';
import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import { MoreVertical } from 'lucide-react-native';
import * as React from 'react';
import { KeyboardAvoidingView, Platform, Pressable, View } from 'react-native';
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';

import { Composer } from '@/components/composer';
import { ConversationActionMenu } from '@/components/conversation-action-menu';
import { MessageList } from '@/components/message-list';
import { MuteSheet } from '@/components/mute-sheet';
import { Text } from '@/components/ui/text';
import { ThemedBackButton } from '@/components/ui/themed-back-button';
import { WSReconnectBanner } from '@/components/ws-reconnect-banner';
import { useRefetchMessagesOnReconnect } from '@/lib/ws/use-refetch-on-reconnect';
import { useSendMessage } from '@/lib/use-send-message';
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

  // Keep the detail query ENABLED even when we have a cached row —
  // the cached row only seeds the initial render (so the title +
  // members paint instantly on the push transition). After
  // useMarkReadOnFocus invalidates this query, a disabled query
  // wouldn't refetch and the read-receipt avatars would freeze on
  // the seed snapshot (CR on PR #144). Feeding cachedRow as
  // `initialData` keeps the hook reactive while still avoiding a
  // blank flash on first paint.
  const detailQ = useGetV1ConversationsId(id ?? '', {
    query: {
      enabled: !!id,
      staleTime: 30_000,
      ...(cachedRow ? { initialData: cachedRow as never } : {}),
    },
  });
  const detail = detailQ.data as InternalHandlerHttpConversationResponse | undefined;
  const conversation = detail ?? cachedRow;

  const title = computeTitle(conversation, me?.id);
  const fg = useThemeColor('foreground');
  const card = useThemeColor('card');
  const border = useThemeColor('border');
  // Header + status-bar height — the offset KeyboardAvoidingView
  // needs so the keyboard padding starts below the chrome, not
  // from the top of the screen.
  const headerHeight = useHeaderHeight();

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
      {/* Web renders (tabs) as a sidebar layout, which doesn't show
          the native navigator header — so the thread has no back /
          title / actions chrome there. Render an in-content header
          bar on web; native keeps the Stack.Screen header above. */}
      {Platform.OS === 'web' ? (
        <View
          style={{ backgroundColor: card, borderBottomColor: border }}
          className="flex-row items-center border-b px-3 py-2">
          <ThemedBackButton label="Chats" testID="conversation-thread-back-web" />
          {/* Title centred in the *full* bar (not between the
              buttons) — absolute, with side padding so a long name
              clips under the chrome rather than overlapping it. */}
          <View pointerEvents="none" className="absolute inset-x-0 items-center">
            <Text
              numberOfLines={1}
              style={{ paddingHorizontal: 96 }}
              className="text-base font-semibold">
              {title}
            </Text>
          </View>
          <View className="flex-1" />
          {convId ? (
            <Pressable
              onPress={() => setSheet('menu')}
              accessibilityRole="button"
              accessibilityLabel="Conversation actions"
              testID="conversation-thread-more-web"
              hitSlop={8}
              className="h-9 w-9 items-center justify-center rounded-md active:bg-muted">
              <MoreVertical size={20} color={fg} />
            </Pressable>
          ) : null}
        </View>
      ) : null}
      <KeyboardAvoidingView
        behavior="padding"
        keyboardVerticalOffset={headerHeight}
        className="flex-1 bg-background">
        {convId ? (
          <ThreadBody
            conversationId={convId}
            myUserId={me?.id}
            isGroup={isGroup}
            members={conversation?.members ?? []}
          />
        ) : null}
      </KeyboardAvoidingView>

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

// Mounts the send-mutation once per conversation route and shares
// the same hook output across the message list (per-bubble status
// + retry) and the composer (send + isPending). Hoisting into the
// parent screen would re-run the hook on every screen-level
// render (header sheet open/close, conv detail invalidation, etc.).
function ThreadBody({
  conversationId,
  myUserId,
  isGroup,
  members,
}: {
  conversationId: string;
  myUserId: string | undefined;
  isGroup: boolean;
  members: InternalHandlerHttpConversationResponse['members'];
}) {
  const { send, retry, statusByTempId, isPending } = useSendMessage(conversationId, myUserId);
  useRefetchMessagesOnReconnect(conversationId);
  return (
    <>
      <WSReconnectBanner />
      <View className="flex-1">
        <MessageList
          conversationId={conversationId}
          myUserId={myUserId}
          isGroup={isGroup}
          members={members ?? []}
          sendStatusByTempId={statusByTempId}
          onRetrySend={retry}
        />
      </View>
      <Composer onSend={send} pending={isPending} testID="thread-composer" />
    </>
  );
}
