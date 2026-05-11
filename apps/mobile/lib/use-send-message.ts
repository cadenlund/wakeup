// Phase 6.2 — send-message hook with optimistic insertion.
// Send-status follow-up — keeps the placeholder visible on failure
// and exposes a per-bubble status + retry affordance.
//
// Wraps `postV1ConversationsIdMessages` so the thread feels
// instant: a tapped send paints a placeholder bubble at the bottom
// of the list immediately. On success the server-issued row
// replaces the placeholder. On failure the placeholder STAYS in
// cache and its state map entry flips to 'failed' — the bubble
// renders a "Not sent · Retry" affordance the user can tap to
// re-fire the same Idempotency-Key (so the backend dedupes if the
// original silently reached the server).
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
import * as React from 'react';
import { useCallback } from 'react';

import { APIError } from '@/lib/api/client';
import { postV1ConversationsIdMessages } from '@/lib/api/hooks/messages/messages';
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

// Variables for each send / retry. tempId doubles as the optimistic
// bubble id AND the Idempotency-Key — same value across initial
// send and retries so the backend de-dupes deterministically.
//
// isRetry tells onMutate to skip the cache prepend: the placeholder
// is already sitting in the cache from the original failed send;
// re-prepending would draw a duplicate bubble. The retry path just
// flips the per-bubble status back to 'sending' and re-fires the
// HTTP request.
type SendVars = {
  tempId: string;
  body: string;
  isRetry: boolean;
};

// Per-bubble status surfaced to the UI. Successful sends remove
// their entry from the map; the absence of an entry is the
// "delivered" state.
export type LocalSendStatus = 'sending' | 'failed';

export function useSendMessage(
  conversationId: string,
  myUserId: string | undefined
): {
  send: (body: string) => void;
  retry: (tempId: string) => void;
  // tempId → { status, body }. Body is captured so retry can
  // re-send the same content without depending on the bubble's
  // cached body field (which we still write but is conceptually
  // a render concern, not a control-plane store).
  statusByTempId: Map<string, { status: LocalSendStatus; body: string }>;
  isPending: boolean;
} {
  const qc = useQueryClient();
  const [statusByTempId, setStatusByTempId] = React.useState<
    Map<string, { status: LocalSendStatus; body: string }>
  >(() => new Map());

  const mut = useMutation<Message, Error, SendVars, undefined>({
    mutationFn: async (vars) => {
      // Pre-generated key threaded into apiFetch via the
      // `idempotencyKey` init field — the client layer wires it
      // into the `Idempotency-Key` header. Retries reuse the same
      // key so the backend dedupes deterministically.
      const resp = await postV1ConversationsIdMessages(conversationId, { body: vars.body }, {
        idempotencyKey: vars.tempId,
      } as RequestInit & { idempotencyKey: string });
      return resp as unknown as Message;
    },
    onMutate: async (vars) => {
      // Mark the bubble as sending — the bubble component reads
      // this to render the per-row loading hint.
      setStatusByTempId((prev) => {
        const next = new Map(prev);
        next.set(vars.tempId, { status: 'sending', body: vars.body });
        return next;
      });

      if (vars.isRetry) {
        // Retry path: the placeholder is already in the cache from
        // the original send; just re-fire the network. No
        // cancel/prepend needed.
        return undefined;
      }

      const placeholder: Message = {
        id: vars.tempId,
        conversation_id: conversationId,
        sender_id: myUserId,
        body: vars.body,
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
      for (const [key] of entries) {
        qc.setQueryData(key as readonly unknown[], (prev: CachedList | undefined) =>
          prepend(prev, placeholder)
        );
      }
      return undefined;
    },
    onSuccess: (serverRow, vars) => {
      const entries = qc.getQueriesData<CachedList>({
        queryKey: [`/v1/conversations/${conversationId}/messages`],
      });
      for (const [key] of entries) {
        qc.setQueryData(key as readonly unknown[], (prev: CachedList | undefined) =>
          replace(prev, vars.tempId, serverRow)
        );
      }
      // Clear the status entry — absence means "delivered."
      setStatusByTempId((prev) => {
        if (!prev.has(vars.tempId)) return prev;
        const next = new Map(prev);
        next.delete(vars.tempId);
        return next;
      });
    },
    onError: (err, vars) => {
      // Keep the placeholder in cache; flip its status to failed
      // so the bubble renders the "Not sent · Retry" affordance.
      // Toast/haptic surfaces the failure but doesn't yank the
      // bubble out from under the user — they need to be able to
      // tap retry without first re-typing the body.
      setStatusByTempId((prev) => {
        const next = new Map(prev);
        next.set(vars.tempId, { status: 'failed', body: vars.body });
        return next;
      });
      haptics.warning();
      const msg = err instanceof APIError && err.message ? err.message : "Couldn't send.";
      toast.error(msg);
    },
  });

  // Use `mut.mutate` via a ref so the send/retry callbacks are
  // identity-stable across renders (the mut object identity flips
  // every render in TanStack v5, which would otherwise re-create
  // every Composer prop on each keystroke). Pattern is the same
  // one TanStack docs use for stable mutation hooks.
  const mutateRef = React.useRef(mut.mutate);
  mutateRef.current = mut.mutate;

  const send = useCallback((body: string) => {
    const trimmed = body.trim();
    if (!trimmed) return;
    haptics.tap();
    mutateRef.current({
      // Key generated HERE so the same value lands on the HTTP
      // request, the optimistic placeholder id, and the retry
      // path.
      tempId: newIdempotencyKey(),
      body: trimmed,
      isRetry: false,
    });
  }, []);

  const retry = useCallback(
    (tempId: string) => {
      const entry = statusByTempId.get(tempId);
      if (!entry || entry.status !== 'failed') return;
      haptics.tap();
      mutateRef.current({
        tempId,
        body: entry.body,
        isRetry: true,
      });
    },
    [statusByTempId]
  );

  return { send, retry, statusByTempId, isPending: mut.isPending };
}
