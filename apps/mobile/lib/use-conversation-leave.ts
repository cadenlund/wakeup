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
import { getGetV1SearchQueryKey } from '@/lib/api/hooks/search/search';
import { toast } from '@/lib/toast';

// Hook resolves to a void Promise regardless of outcome. Callers
// fire-and-forget (`void leave(id)`); errors surface as toasts
// inside the hook so an unawaited rejection can't bubble up as
// an "unhandled promise" warning at the React tree level. The
// boolean return tells callers whether the call succeeded if
// they want to chain follow-up navigation.
export function useLeaveConversation(): {
  leave: (conversationId: string) => Promise<boolean>;
  isPending: boolean;
} {
  const qc = useQueryClient();
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meQ.data as { id?: string } | undefined;
  const del = useDeleteV1ConversationsIdMembersUserId();

  const leave = React.useCallback(
    async (conversationId: string): Promise<boolean> => {
      const userId = me?.id;
      if (!userId) {
        // Two paths land here:
        //   - meQ hasn't resolved yet (cold tap right after mount):
        //     don't toast, just short-circuit. The user retries on
        //     the next render with meQ.data populated.
        //   - meQ resolved with no user (genuinely signed-out):
        //     surface the toast so the user understands why
        //     nothing happened.
        if (!meQ.isLoading) {
          toast.error("Couldn't leave: not signed in.");
        }
        return false;
      }
      try {
        await del.mutateAsync({ id: conversationId, userId });
        // Invalidate both the conversations list (chats tab) AND
        // the unified-search query (search modal renders chat rows
        // straight from /v1/search) so a just-left group disappears
        // from every surface at once.
        await Promise.all([
          qc.invalidateQueries({ queryKey: getGetV1ConversationsQueryKey() }),
          qc.invalidateQueries({ queryKey: getGetV1SearchQueryKey() }),
        ]);
        toast.info('Left group');
        return true;
      } catch (err) {
        const msg =
          err instanceof APIError && err.message
            ? err.message
            : "Couldn't leave this group right now.";
        toast.error(msg);
        return false;
      }
    },
    [del, me?.id, meQ.isLoading, qc]
  );

  return { leave, isPending: del.isPending };
}
