// Phase 6.5 — delete (own) message.
//
// `DELETE /v1/messages/{id}` soft-deletes the row server-side
// (sets `deleted_at`). The client mirrors that optimistically:
// the cached message flips to `is_deleted: true` + an empty body,
// so <MessageBubble> renders the "Message deleted" placeholder
// instead of disappearing — keeping a reply chain readable. On
// failure we roll the cache back to the pre-delete snapshot and
// surface a toast.
//
// Like the send hook, we hand-roll `useMutation` (not the
// orval-generated hook) so we can attach a per-call Idempotency-Key
// — a re-fired delete dedupes against a server-side success the
// client never saw.
import { type InfiniteData, useMutation, useQueryClient } from '@tanstack/react-query';
import * as React from 'react';
import { useCallback } from 'react';

import { APIError } from '@/lib/api/client';
import { deleteV1MessagesId } from '@/lib/api/hooks/messages/messages';
import { newIdempotencyKey } from '@/lib/api/idempotency';
import type {
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpMessageResponse,
} from '@/lib/api/model';
import { haptics } from '@/lib/haptics';
import { toast } from '@/lib/toast';

type Message = InternalHandlerHttpMessageResponse;
type ListData = InternalHandlerHttpMessageListResponse;
type InfiniteList = InfiniteData<ListData>;
type CachedList = ListData | InfiniteList;
type Snapshot = { key: readonly unknown[]; data: CachedList | undefined };

function isInfinite(data: CachedList | undefined): data is InfiniteList {
  return !!data && Array.isArray((data as InfiniteList).pages);
}

// Flip the message (by id) to its soft-deleted shape in every page.
function markDeleted(data: CachedList | undefined, messageId: string): CachedList | undefined {
  if (!data) return data;
  const patch = (m: Message): Message =>
    m.id === messageId
      ? { ...m, is_deleted: true, body: '', deleted_at: new Date().toISOString() }
      : m;
  if (isInfinite(data)) {
    let touched = false;
    const pages = data.pages.map((p) => {
      if (!p.data) return p;
      let pageTouched = false;
      const next = p.data.map((m) => {
        if (m.id !== messageId) return m;
        pageTouched = true;
        touched = true;
        return patch(m);
      });
      return pageTouched ? { ...p, data: next } : p;
    });
    return touched ? { ...data, pages } : data;
  }
  if (!data.data) return data;
  let touched = false;
  const next = data.data.map((m) => {
    if (m.id !== messageId) return m;
    touched = true;
    return patch(m);
  });
  return touched ? { ...data, data: next } : data;
}

export function useDeleteMessage(conversationId: string): {
  deleteMessage: (messageId: string) => void;
  isPending: boolean;
} {
  const qc = useQueryClient();

  type Vars = { messageId: string; idempotencyKey: string };
  type Ctx = { snapshots: Snapshot[] };

  const mut = useMutation<unknown, Error, Vars, Ctx>({
    mutationFn: async (vars) => {
      return deleteV1MessagesId(vars.messageId, {
        idempotencyKey: vars.idempotencyKey,
      } as RequestInit & { idempotencyKey: string });
    },
    onMutate: async (vars): Promise<Ctx> => {
      await qc.cancelQueries({
        queryKey: [`/v1/conversations/${conversationId}/messages`],
      });
      const entries = qc.getQueriesData<CachedList>({
        queryKey: [`/v1/conversations/${conversationId}/messages`],
      });
      const snapshots: Snapshot[] = [];
      for (const [key, current] of entries) {
        snapshots.push({ key: key as readonly unknown[], data: current });
        qc.setQueryData(key as readonly unknown[], (prev: CachedList | undefined) =>
          markDeleted(prev, vars.messageId)
        );
      }
      return { snapshots };
    },
    onError: (err, _vars, ctx) => {
      if (ctx) {
        for (const { key, data } of ctx.snapshots) {
          qc.setQueryData(key, data);
        }
      }
      haptics.warning();
      const msg = err instanceof APIError && err.message ? err.message : "Couldn't delete.";
      toast.error(msg);
    },
  });

  // Identity-stable mutate handle (mut object identity flips every
  // render in TanStack v5) — same pattern as useSendMessage.
  const mutateRef = React.useRef(mut.mutate);
  mutateRef.current = mut.mutate;

  const deleteMessage = useCallback((messageId: string) => {
    if (!messageId) return;
    haptics.tap();
    mutateRef.current({ messageId, idempotencyKey: newIdempotencyKey() });
  }, []);

  return { deleteMessage, isPending: mut.isPending };
}
