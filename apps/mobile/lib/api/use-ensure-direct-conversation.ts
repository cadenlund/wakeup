// Phase 5.3 — `useEnsureDirectConversation`. Per §16:
//
// > Tap a friend in (tabs)/friends → DM auto-create on first
// > message. Helper: useEnsureDirectConversation(friendId).
//
// Behaviour: given a friend's user_id, return the id of the direct
// conversation between us. If we already have one in cache, hand
// back the existing id (no network); otherwise POST a new one and
// invalidate the conversations list so the next render reflects
// reality.
//
// Returning the id (rather than navigating internally) keeps the
// hook reusable from anywhere a row tap might lead to a DM —
// friends list now, the @-mention popover later, the user-profile
// screen in 5.x. Callers compose the navigation themselves.
import * as React from 'react';
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';

import {
  getGetV1ConversationsQueryKey,
  usePostV1Conversations,
} from '@/lib/api/hooks/conversations/conversations';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
} from '@/lib/api/model';

export type EnsureDirectResult = {
  conversationId: string;
  // True when the conversation didn't exist in cache and the helper
  // had to create it. Lets the caller fire a haptic / toast on the
  // genuine "new chat" path and stay quiet on the warm path.
  created: boolean;
};

export function useEnsureDirectConversation(): {
  ensure: (friendUserId: string) => Promise<EnsureDirectResult>;
  isPending: boolean;
} {
  const qc = useQueryClient();
  const create = usePostV1Conversations();

  const ensure = React.useCallback(
    async (friendUserId: string): Promise<EnsureDirectResult> => {
      // 1. Cache hit. Walk every cached `/v1/conversations` query
      //    (the chats tab uses an infinite-query shape; pages live
      //    under `data.pages[].data[]`) for an existing direct
      //    conversation that includes this friend. Direct
      //    conversations are 1:1 so a single member match is
      //    enough — type === 'direct' keeps us from picking a
      //    1-other-member group.
      const prefix = getGetV1ConversationsQueryKey()[0];
      type CachedList =
        | InternalHandlerHttpConversationListResponse
        | InfiniteData<InternalHandlerHttpConversationListResponse>;
      const isInfinite = (
        d: CachedList
      ): d is InfiniteData<InternalHandlerHttpConversationListResponse> =>
        Array.isArray((d as InfiniteData<InternalHandlerHttpConversationListResponse>).pages);
      const cachedConversations: InternalHandlerHttpConversationResponse[] = [];
      for (const [, data] of qc.getQueriesData<CachedList>({ queryKey: [prefix] })) {
        if (!data) continue;
        if (isInfinite(data)) {
          for (const page of data.pages) {
            if (page.data) cachedConversations.push(...page.data);
          }
        } else if (data.data) {
          cachedConversations.push(...data.data);
        }
      }
      const existing = cachedConversations.find(
        (c) =>
          c.type === 'direct' &&
          !!c.id &&
          (c.members ?? []).some((m) => m.user?.id === friendUserId)
      );
      if (existing?.id) {
        return { conversationId: existing.id, created: false };
      }

      // 2. Cache miss → create. Backend dedupes on (caller_id,
      //    member_ids) so racing two callers with the same friend
      //    settles on a single row server-side.
      const res = (await create.mutateAsync({
        data: { type: 'direct', member_ids: [friendUserId] },
      })) as InternalHandlerHttpConversationResponse | undefined;

      if (!res?.id) {
        // The server returned 2xx without an id (shouldn't happen
        // per the schema). Surface as a thrown error so the caller's
        // error path runs rather than navigating to a broken route.
        throw new Error('Conversation created without an id');
      }

      // Refresh the list cache so the chats tab picks up the new
      // row on its next render.
      await qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() });
      return { conversationId: res.id, created: true };
    },
    [qc, create]
  );

  return { ensure, isPending: create.isPending };
}
