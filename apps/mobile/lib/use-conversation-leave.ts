// Phase 5.7 — leave (or self-kick) a conversation.
//
// DELETE /v1/conversations/{id}/members/{user_id} is the same
// endpoint the admin "kick member" flow uses; when user_id == me,
// the backend treats it as Leave (membership row deleted, other
// members keep their view of the conversation).
//
// On success: invalidate the conversations list cache so the chats
// tab + search modal drop the row, surface a confirmation toast,
// and let the caller route away from the now-orphaned thread.
import * as React from 'react';
import { useQueryClient } from '@tanstack/react-query';

import { APIError } from '@/lib/api/client';
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import {
  getGetV1ConversationsQueryKey,
  useDeleteV1ConversationsIdMembersUserId,
} from '@/lib/api/hooks/conversations/conversations';
import { toast } from '@/lib/toast';

export function useLeaveConversation(): {
  leave: (conversationId: string) => Promise<void>;
  isPending: boolean;
} {
  const qc = useQueryClient();
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;
  const del = useDeleteV1ConversationsIdMembersUserId();

  const leave = React.useCallback(
    async (conversationId: string) => {
      const userId = me?.id;
      if (!userId) {
        // No caller id known — refuse to attempt the call rather
        // than letting the backend 404 ambiguously.
        throw new Error('Cannot leave without an authenticated user');
      }
      try {
        await del.mutateAsync({ id: conversationId, userId });
        await qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() });
        toast.info('Left group');
      } catch (err) {
        const msg =
          err instanceof APIError && err.message
            ? err.message
            : "Couldn't leave this group right now.";
        toast.error(msg);
        throw err;
      }
    },
    [del, me?.id, qc]
  );

  return { leave, isPending: del.isPending };
}
