// Phase 6.3 — read pointer maintenance.
//
// Fires `POST /v1/conversations/{id}/read` whenever the screen is
// focused AND the visible-latest message has changed since the
// last *successful* ack. The backend stores `last_read_message_id`
// per member, drives unread counts off it, and (Phase 7) broadcasts
// a `message.read` WS event so other participants can render the
// read-receipt avatar under the matching bubble.
//
// Acking only on success matters: pre-acking before the mutation
// finishes would permanently suppress re-send for the same id on
// a transient failure (CR on PR #143). We retry naturally on the
// next focus event by gating off `ackedRef.current`.
//
// Idempotency: every mutation in this codebase attaches an
// `Idempotency-Key` (per the project rule + WAKEUP §4.7). We
// pre-generate the key BEFORE the first attempt for a given
// message id and REUSE it on every retry of that same id, so the
// backend dedupes if the original silently reached the server.
// `useFocusEffect` is the Expo Router equivalent of focus / blur:
// it re-runs when the screen mounts AND every time the screen
// returns to the foreground via navigation.
import { useFocusEffect } from 'expo-router';
import * as React from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import { APIError } from '@/lib/api/client';
import {
  getGetV1ConversationsIdQueryKey,
  getGetV1ConversationsQueryKey,
  postV1ConversationsIdRead,
} from '@/lib/api/hooks/conversations/conversations';
import { newIdempotencyKey } from '@/lib/api/idempotency';

type Vars = { messageId: string; idempotencyKey: string };

export function useMarkReadOnFocus(
  conversationId: string,
  latestMessageId: string | undefined
): void {
  const qc = useQueryClient();
  // Last successfully-acked message id. Re-focus on the same id is
  // a no-op; a new id (or a previous attempt that failed and never
  // wrote to ackedRef) triggers another mutate.
  const ackedRef = React.useRef<string | undefined>(undefined);
  // Pre-generated Idempotency-Key for the in-flight / failed
  // mutation. Reused on retry of the SAME message id so the backend
  // dedupes; cleared once the id advances (a fresh attempt for a
  // newer message gets a fresh key).
  const pendingRef = React.useRef<{ messageId: string; idempotencyKey: string } | undefined>(
    undefined
  );

  const mut = useMutation<unknown, Error, Vars>({
    mutationFn: async (vars) => {
      return postV1ConversationsIdRead(conversationId, { up_to_message_id: vars.messageId }, {
        idempotencyKey: vars.idempotencyKey,
      } as RequestInit & { idempotencyKey: string });
    },
    onSuccess: (_resp, vars) => {
      ackedRef.current = vars.messageId;
      pendingRef.current = undefined;
      // Refresh the conversation detail so the member rows pick up
      // the new last_read_message_id (drives the read-receipt
      // avatars + unread counts). Also touch the list query so the
      // chats-tab unread badge clears.
      void qc.invalidateQueries({
        queryKey: getGetV1ConversationsIdQueryKey(conversationId),
      });
      void qc.invalidateQueries({
        queryKey: [getGetV1ConversationsQueryKey()[0]],
      });
    },
    onError: (err) => {
      // Silent fail — MarkRead is best-effort. ackedRef stays
      // unchanged so the next focus will retry; pendingRef keeps
      // the same key so the retry dedupes against a server-side
      // success we never saw. Log the swallow per the project's
      // log-swallowed-errors rule.
      const message = err instanceof APIError && err.message ? err.message : 'mark-read failed';
      console.warn('[markRead]', message);
    },
  });

  // Identity-stable mutate handle so the focus callback doesn't
  // re-create per render.
  const mutateRef = React.useRef(mut.mutate);
  mutateRef.current = mut.mutate;

  useFocusEffect(
    React.useCallback(() => {
      if (!conversationId || !latestMessageId) return;
      if (ackedRef.current === latestMessageId) return;
      // Reuse the key if a previous attempt for this same id is
      // still pending / had failed; otherwise mint a fresh one.
      const existing = pendingRef.current;
      const idempotencyKey =
        existing?.messageId === latestMessageId ? existing.idempotencyKey : newIdempotencyKey();
      pendingRef.current = { messageId: latestMessageId, idempotencyKey };
      mutateRef.current({ messageId: latestMessageId, idempotencyKey });
    }, [conversationId, latestMessageId])
  );
}
