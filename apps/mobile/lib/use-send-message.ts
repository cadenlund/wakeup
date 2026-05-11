// Phase 6.2 — send-message hook with optimistic insertion.
//
// Wraps `postV1ConversationsIdMessages` so the thread feels
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
//
// Idempotency: we hand-roll the mutation (`useMutation` directly,
// not the orval-generated hook) so the caller can pre-generate
// the Idempotency-Key, pass it INTO the HTTP request via
// `apiFetch`'s `idempotencyKey` init field, AND reuse it as the
// optimistic placeholder's tempId. That way a user re-tapping
// send after a failed network attempt sends the same header and
// the backend de-dupes (CR on PR #142).
import { useMutation, type InfiniteData, useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';

import { APIError } from '@/lib/api/client';
import {
  postV1ConversationsIdMessages,
  type PostV1ConversationsIdMessagesMutationBody,
} from '@/lib/api/hooks/messages/messages';
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

// Variables for each send. idempotencyKey is generated by the
// caller and threaded BOTH into the HTTP request init (for
// backend de-dup on retry) and into the placeholder bubble's id
// (so onSuccess can replace it without a second lookup).
type SendVars = {
  idempotencyKey: string;
  data: PostV1ConversationsIdMessagesMutationBody;
};

type Ctx = { tempId: string; snapshots: Snapshot[] };

export function useSendMessage(
  conversationId: string,
  myUserId: string | undefined
): { send: (body: string) => void; isPending: boolean } {
  const qc = useQueryClient();

  const mut = useMutation<Message, Error, SendVars, Ctx>({
    mutationFn: async (vars) => {
      // Pass the pre-generated key down into apiFetch via the
      // `idempotencyKey` init field — the client layer wires that
      // into the `Idempotency-Key` header and the backend uses it
      // to dedupe a duplicate request on retry.
      const resp = await postV1ConversationsIdMessages(conversationId, vars.data, {
        idempotencyKey: vars.idempotencyKey,
      } as RequestInit & { idempotencyKey: string });
      return resp as unknown as Message;
    },
    onMutate: async (vars): Promise<Ctx> => {
      const tempId = vars.idempotencyKey;
      const placeholder: Message = {
        id: tempId,
        conversation_id: conversationId,
        sender_id: myUserId,
        body: vars.data.body,
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
    onSuccess: (serverRow, _vars, ctx) => {
      if (!ctx) return;
      for (const { key } of ctx.snapshots) {
        qc.setQueryData(key, (prev: CachedList | undefined) =>
          replace(prev, ctx.tempId, serverRow)
        );
      }
    },
    onError: (err, _vars, ctx) => {
      if (!ctx) return;
      for (const { key } of ctx.snapshots) {
        qc.setQueryData(key, (prev: CachedList | undefined) => remove(prev, ctx.tempId));
      }
      haptics.warning();
      const msg = err instanceof APIError && err.message ? err.message : "Couldn't send.";
      toast.error(msg);
    },
  });

  const send = useCallback(
    (body: string) => {
      const trimmed = body.trim();
      if (!trimmed) return;
      haptics.tap();
      mut.mutate({
        // Key generated HERE so the same value lands on the HTTP
        // request, the optimistic placeholder id, and any future
        // resend triggered by the caller.
        idempotencyKey: newIdempotencyKey(),
        data: { body: trimmed },
      });
    },
    [mut]
  );

  return { send, isPending: mut.isPending };
}
