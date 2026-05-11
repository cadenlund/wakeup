// Phase 6.3 — read pointer maintenance.
//
// Fires `POST /v1/conversations/{id}/read` whenever the screen is
// focused AND the visible-latest message has changed since the
// last call. The backend stores `last_read_message_id` per member,
// drives unread counts off it, and (Phase 7) broadcasts a
// `message.read` WS event so other participants can render the
// read-receipt avatar under the matching bubble.
//
// We deliberately don't fire on every render — only when the
// latest message id flips, so a parked screen with no new
// messages doesn't make a request per second.
//
// `useFocusEffect` is the Expo Router equivalent of focus / blur:
// it re-runs when the screen mounts AND every time the screen
// returns to the foreground via navigation. That's enough
// resolution for "the user looked at this thread."
import { useFocusEffect } from 'expo-router';
import * as React from 'react';

import { APIError } from '@/lib/api/client';
import {
  getGetV1ConversationsIdQueryKey,
  getGetV1ConversationsQueryKey,
  usePostV1ConversationsIdRead,
} from '@/lib/api/hooks/conversations/conversations';
import { useQueryClient } from '@tanstack/react-query';

export function useMarkReadOnFocus(
  conversationId: string,
  latestMessageId: string | undefined
): void {
  const mut = usePostV1ConversationsIdRead();
  const qc = useQueryClient();
  // Remember the last id we acknowledged so a re-focus on the
  // same screen doesn't re-fire when nothing new has arrived.
  const lastAckedRef = React.useRef<string | undefined>(undefined);

  useFocusEffect(
    React.useCallback(() => {
      if (!conversationId || !latestMessageId) return;
      if (lastAckedRef.current === latestMessageId) return;
      lastAckedRef.current = latestMessageId;
      mut.mutate(
        { id: conversationId, data: { up_to_message_id: latestMessageId } },
        {
          onSuccess: () => {
            // Refresh the conversation detail so the member rows
            // pick up the new last_read_message_id (drives the
            // read-receipt avatars + unread counts). Also touch
            // the list query so the chats-tab unread badge clears.
            void qc.invalidateQueries({
              queryKey: getGetV1ConversationsIdQueryKey(conversationId),
            });
            void qc.invalidateQueries({
              queryKey: [getGetV1ConversationsQueryKey()[0]],
            });
          },
          onError: (err) => {
            // Silent fail — MarkRead is best-effort. If the
            // network is down the user will resync on next focus,
            // and the backend treats the unsynced state correctly
            // (the read pointer stays where it was, not where the
            // user is). Still log the swallow so a real outage
            // surfaces in Sentry per the project's
            // log-swallowed-errors rule.
            const message =
              err instanceof APIError && err.message ? err.message : 'mark-read failed';
            console.warn('[markRead]', message);
          },
        }
      );
    }, [conversationId, latestMessageId, mut, qc])
  );
}
