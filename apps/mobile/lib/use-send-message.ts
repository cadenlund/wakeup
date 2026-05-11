// Phase 6.2 — send-message hook with optimistic insertion.
//
// Wraps `usePostV1ConversationsIdMessages` so the thread feels
// instant: a tapped send paints a placeholder bubble at the bottom
// of the list immediately, then either swaps in the server-issued
// row on success or rolls back + toasts on failure.
//
// Cache shape: every variant of
// `/v1/conversations/{id}/messages` (different `limit` / `q`
// params produce separate cache entries) gets patched in
// lock-step. The §6.4 endpoint orders reverse-chrono so the
// optimistic row prepends to `pages[0].data[0]` — exactly where
// `<MessageList>` will see it as the newest message (the array
// is reversed once at render time so the bubble lands at the
// bottom of the visible scroll).
//
// Why optimistic: §4.8 — send is the path the user *most* notices
// latency on, and the backend response carries no UI-affecting
// data we couldn't already predict from the request. We reconcile
// by id on success rather than re-fetching the first page, which
// keeps the visible scroll position stable.
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';

import { APIError } from '@/lib/api/client';
import { usePostV1ConversationsIdMessages } from '@/lib/api/hooks/messages/messages';
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

// Prepend a placeholder to the first page of the cached list. The
// API ordering is newest-first, so the freshest message belongs at
// pages[0].data[0].
function prepend(data: CachedList | undefined, placeholder: Message): CachedList | undefined {
  if (!data) return data;
  if (isInfinite(data)) {
    if (data.pages.length === 0) {
      return { ...data, pages: [{ data: [placeholder] } as ListData] };
    }
    const [first, ...rest] = data.pages;
    return {
      ...data,
      pages: [{ ...first, data: [placeholder, ...(first.data ?? [])] }, ...rest],
    };
  }
  return { ...data, data: [placeholder, ...(data.data ?? [])] };
}

// Replace the placeholder (by tempId) with the server-issued row.
// Walks every page because a long-running thread might have the
// placeholder sitting in a non-first page after a paginated
// refetch races with the optimistic insert (rare, but cheap to
// handle correctly).
function replace(
  data: CachedList | undefined,
  tempId: string,
  serverRow: Message
): CachedList | undefined {
  if (!data) return data;
  if (isInfinite(data)) {
    let touched = false;
    const pages = data.pages.map((p) => {
      if (!p.data) return p;
      let pageTouched = false;
      const next = p.data.map((m) => {
        if (m.id !== tempId) return m;
        pageTouched = true;
        touched = true;
        return serverRow;
      });
      return pageTouched ? { ...p, data: next } : p;
    });
    return touched ? { ...data, pages } : data;
  }
  if (!data.data) return data;
  let touched = false;
  const next = data.data.map((m) => {
    if (m.id !== tempId) return m;
    touched = true;
    return serverRow;
  });
  return touched ? { ...data, data: next } : data;
}

// Drop the placeholder on send failure so the user doesn't see a
// phantom bubble that never made it to the server.
function remove(data: CachedList | undefined, tempId: string): CachedList | undefined {
  if (!data) return data;
  if (isInfinite(data)) {
    let touched = false;
    const pages = data.pages.map((p) => {
      if (!p.data) return p;
      const next = p.data.filter((m) => m.id !== tempId);
      if (next.length === p.data.length) return p;
      touched = true;
      return { ...p, data: next };
    });
    return touched ? { ...data, pages } : data;
  }
  if (!data.data) return data;
  const next = data.data.filter((m) => m.id !== tempId);
  if (next.length === data.data.length) return data;
  return { ...data, data: next };
}

export function useSendMessage(
  conversationId: string,
  myUserId: string | undefined
): { send: (body: string) => void; isPending: boolean } {
  const qc = useQueryClient();

  type Ctx = { tempId: string; snapshots: Snapshot[] };

  const mut = usePostV1ConversationsIdMessages({
    mutation: {
      onMutate: async ({ data }): Promise<Ctx> => {
        const tempId = newIdempotencyKey();
        const placeholder: Message = {
          id: tempId,
          conversation_id: conversationId,
          sender_id: myUserId,
          body: data.body,
          created_at: new Date().toISOString(),
          is_deleted: false,
        };
        // Cancel in-flight refetches so a late page response can't
        // overwrite our optimistic prepend.
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
            prepend(prev, placeholder)
          );
        }
        return { tempId, snapshots };
      },
      onSuccess: (resp, _vars, ctx) => {
        const c = ctx as Ctx | undefined;
        if (!c) return;
        // The orval mutator returns the unwrapped JSON body at
        // runtime (the `{ data, status, headers }` envelope is a
        // TYPE overlay only) — same cast pattern useInfiniteMessages
        // uses.
        const serverRow = resp as unknown as Message;
        for (const { key } of c.snapshots) {
          qc.setQueryData(key, (prev: CachedList | undefined) =>
            replace(prev, c.tempId, serverRow)
          );
        }
      },
      onError: (err, _vars, ctx) => {
        const c = ctx as Ctx | undefined;
        if (!c) return;
        for (const { key } of c.snapshots) {
          qc.setQueryData(key, (prev: CachedList | undefined) => remove(prev, c.tempId));
        }
        haptics.warning();
        const msg = err instanceof APIError && err.message ? err.message : "Couldn't send.";
        toast.error(msg);
      },
    },
  });

  const send = useCallback(
    (body: string) => {
      const trimmed = body.trim();
      if (!trimmed) return;
      haptics.tap();
      mut.mutate({ id: conversationId, data: { body: trimmed } });
    },
    [conversationId, mut]
  );

  return { send, isPending: mut.isPending };
}
