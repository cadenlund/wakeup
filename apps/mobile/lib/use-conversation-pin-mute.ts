// Optimistic pin / mute mutations for a single conversation row.
//
// Wraps the orval-generated `usePatchV1ConversationsIdPin` and
// `usePatchV1ConversationsIdMute` hooks with an `onMutate` step
// that patches every cached `GET /v1/conversations` list (the
// query key includes pagination params, so multiple entries can
// exist simultaneously) and `onError` rollback on failure.
//
// Why optimistic: pin/mute are pure UI affordances — there's no
// 4xx the user can act on, just 5xx + network. The list resort
// on pin needs to feel instant, otherwise the row appears to
// "jump" half a second after the tap.
//
// Reused by Phase 5.6 (long-press on the row) and — when 5.7
// lands — the conversation header overflow menu.
import { useCallback } from 'react';
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';

import { haptics } from '@/lib/haptics';
import { toast } from '@/lib/toast';
import {
  getGetV1ConversationsQueryKey,
  usePatchV1ConversationsIdMute,
  usePatchV1ConversationsIdPin,
} from '@/lib/api/hooks/conversations/conversations';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
} from '@/lib/api/model';

type ListData = InternalHandlerHttpConversationListResponse;
type InfiniteList = InfiniteData<ListData>;
type CachedList = ListData | InfiniteList;

// Type-guard: useInfiniteQuery stores `{ pages, pageParams }`; the
// single-page `useQuery` shape is just the response body. Pin/mute
// has to patch BOTH because the search modal reads from one and the
// chats tab from the other (until everything migrates to infinite).
function isInfinite(data: CachedList | undefined): data is InfiniteList {
  return !!data && Array.isArray((data as InfiniteList).pages);
}

function patchRow(
  data: CachedList | undefined,
  conversationId: string,
  patch: Partial<InternalHandlerHttpConversationResponse>
): CachedList | undefined {
  if (!data) return data;
  if (isInfinite(data)) {
    let touched = false;
    const nextPages = data.pages.map((page) => {
      if (!page.data) return page;
      let pageTouched = false;
      const nextData = page.data.map((c) => {
        if (c.id !== conversationId) return c;
        pageTouched = true;
        touched = true;
        return { ...c, ...patch };
      });
      return pageTouched ? { ...page, data: nextData } : page;
    });
    return touched ? { ...data, pages: nextPages } : data;
  }
  if (!data.data) return data;
  let touched = false;
  const next = data.data.map((c) => {
    if (c.id !== conversationId) return c;
    touched = true;
    return { ...c, ...patch };
  });
  if (!touched) return data;
  return { ...data, data: next };
}

// Walk every cached `/v1/conversations` query (different `limit`
// or filter params produce separate cache entries, and the
// useInfiniteQuery shape lives under the same prefix with an
// `'infinite'` suffix) and apply `patch` to the matching row in
// each. Returns a snapshot of the previous values keyed by full
// query key, for rollback.
function patchAllConversationLists(
  qc: ReturnType<typeof useQueryClient>,
  conversationId: string,
  patch: Partial<InternalHandlerHttpConversationResponse>
) {
  const prefix = getGetV1ConversationsQueryKey()[0];
  const entries = qc.getQueriesData<CachedList>({ queryKey: [prefix] });
  const snapshots: { key: readonly unknown[]; data: CachedList | undefined }[] = [];
  for (const [key, data] of entries) {
    snapshots.push({ key: key as readonly unknown[], data });
    qc.setQueryData(key as readonly unknown[], (prev: CachedList | undefined) =>
      patchRow(prev, conversationId, patch)
    );
  }
  return snapshots;
}

function rollback(
  qc: ReturnType<typeof useQueryClient>,
  snapshots: { key: readonly unknown[]; data: CachedList | undefined }[]
) {
  for (const { key, data } of snapshots) {
    qc.setQueryData(key, data);
  }
}

export function useConversationPinMute() {
  const qc = useQueryClient();

  type Ctx = { snapshots: { key: readonly unknown[]; data: CachedList | undefined }[] };

  const pin = usePatchV1ConversationsIdPin({
    mutation: {
      onMutate: async ({ id, data }): Promise<Ctx> => {
        await qc.cancelQueries({ queryKey: [getGetV1ConversationsQueryKey()[0]] });
        const snapshots = patchAllConversationLists(qc, id, {
          pinned_at: data.pinned ? new Date().toISOString() : undefined,
        });
        return { snapshots };
      },
      onError: (_err, _vars, ctx) => {
        const snapshots = (ctx as Ctx | undefined)?.snapshots;
        if (snapshots) rollback(qc, snapshots);
        haptics.warning();
        toast.error("Couldn't update pin");
      },
      onSettled: () => {
        // Reconcile with server truth in the background. Don't
        // await — this fires after the optimistic patch has
        // already settled the UI.
        void qc.invalidateQueries({ queryKey: [getGetV1ConversationsQueryKey()[0]] });
      },
    },
  });

  const mute = usePatchV1ConversationsIdMute({
    mutation: {
      onMutate: async ({ id, data }): Promise<Ctx> => {
        await qc.cancelQueries({ queryKey: [getGetV1ConversationsQueryKey()[0]] });
        const snapshots = patchAllConversationLists(qc, id, {
          muted_until: data.until ?? undefined,
        });
        return { snapshots };
      },
      onError: (_err, _vars, ctx) => {
        const snapshots = (ctx as Ctx | undefined)?.snapshots;
        if (snapshots) rollback(qc, snapshots);
        haptics.warning();
        toast.error("Couldn't update mute");
      },
      onSettled: () => {
        void qc.invalidateQueries({ queryKey: [getGetV1ConversationsQueryKey()[0]] });
      },
    },
  });

  const togglePin = useCallback(
    (conversationId: string, currentlyPinned: boolean) => {
      haptics.tap();
      pin.mutate({ id: conversationId, data: { pinned: !currentlyPinned } });
    },
    [pin]
  );

  const setMute = useCallback(
    (conversationId: string, until: string) => {
      haptics.tap();
      mute.mutate({ id: conversationId, data: { until } });
    },
    [mute]
  );

  const unmute = useCallback(
    (conversationId: string) => {
      haptics.tap();
      // Backend treats an omitted `until` as null = unmute.
      mute.mutate({ id: conversationId, data: {} });
    },
    [mute]
  );

  return { togglePin, setMute, unmute };
}
