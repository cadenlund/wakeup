// Thin wrappers around the §6.4 paginated endpoints that drive
// FlashList's `onEndReached` cleanly. Orval's tags-split client
// only emits the single-page `useQuery` shape, so each consumer used
// to ask for `limit: 100` and ignore the cursor — fine for the first
// 100 rows, broken when a user has 1000 friends.
//
// We sit on top of the orval-generated fetchers (which already route
// through the cookie/idempotency-aware mutator) and let TanStack
// Query wire them into `useInfiniteQuery`. The shared invariant:
//
//   - Page params are opaque cursors from the previous page's
//     `next_cursor`. The first page passes `cursor: undefined`.
//   - `getNextPageParam` returns `undefined` when `has_more` is
//     false so TanStack stops fetching.
//   - `total` is read from the FIRST page only — it's stable
//     across pages by definition (the COUNT(*) the backend runs is
//     unfiltered by the keyset cursor) and the merge below pulls
//     `pages[0].total` out for callers.
//
// The `flatten` helper folds `pages[]` back into a single `data[]`
// array + `total` so the consumer's render code stays the same as
// the old single-page useQuery shape.
import {
  type InfiniteData,
  useInfiniteQuery,
  type UseInfiniteQueryOptions,
} from '@tanstack/react-query';

import {
  getGetV1ConversationsQueryKey,
  getV1Conversations,
} from '@/lib/api/hooks/conversations/conversations';
import { getGetV1FriendsQueryKey, getV1Friends } from '@/lib/api/hooks/friends/friends';
import {
  getGetV1ConversationsIdMessagesQueryKey,
  getV1ConversationsIdMessages,
} from '@/lib/api/hooks/messages/messages';
import { getGetV1UsersQueryKey, getV1Users } from '@/lib/api/hooks/users/users';
import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpFriendListResponse,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpUserListResponse,
} from '@/lib/api/model';

// Default page size. Smaller than the previous 100 so the first
// paint is fast and `total` shows in the header before the user has
// to scroll. Each subsequent page is the same size — FlashList's
// `onEndReached` handles the chaining.
const DEFAULT_LIMIT = 20;

// PaginatedShape constrains the response types we accept here so
// each helper can read `next_cursor` / `has_more` without per-call
// casting. The orval-generated DTOs all share this envelope (§6.4).
type PaginatedShape<T> = {
  data?: T[];
  total?: number;
  next_cursor?: string | null;
  has_more?: boolean;
};

// flatten merges the `pages[]` returned by useInfiniteQuery into the
// shape consumer screens already expect — one `data[]`, one `total`,
// one `nextCursor` for "show more" UI. Total comes from the FIRST
// page so it doesn't flicker as later pages land.
export function flatten<T, Page extends PaginatedShape<T>>(
  pages: Page[] | undefined
): { data: T[]; total: number; hasMore: boolean } {
  if (!pages || pages.length === 0) {
    return { data: [], total: 0, hasMore: false };
  }
  const data: T[] = [];
  for (const p of pages) {
    if (p.data) data.push(...p.data);
  }
  // total is stable across pages (unfiltered count). Read from the
  // first page so we don't briefly flicker to a higher count when
  // page 2 lands with the same value.
  const total = pages[0]?.total ?? data.length;
  const hasMore = pages[pages.length - 1]?.has_more ?? false;
  return { data, total, hasMore };
}

// nextCursorOf is the shared `getNextPageParam`. Returns undefined
// (which tells TanStack to stop) when the page reports has_more=false
// or omits the cursor. The cast goes through string | undefined so
// we never feed an empty string back into the fetcher (the backend
// would reject a zero-length cursor as malformed).
function nextCursorOf<T, Page extends PaginatedShape<T>>(
  last: Page | undefined
): string | undefined {
  if (!last?.has_more) return undefined;
  const c = last.next_cursor;
  return c ? c : undefined;
}

// ---------- friends list ----------

type FriendsResp = InternalHandlerHttpFriendListResponse;

export function useInfiniteFriends(
  opts?: { limit?: number } & {
    query?: Omit<
      UseInfiniteQueryOptions<
        FriendsResp,
        Error,
        InfiniteData<FriendsResp>,
        readonly unknown[],
        string | undefined
      >,
      'queryKey' | 'queryFn' | 'getNextPageParam' | 'initialPageParam'
    >;
  }
) {
  const limit = opts?.limit ?? DEFAULT_LIMIT;
  return useInfiniteQuery<
    FriendsResp,
    Error,
    InfiniteData<FriendsResp>,
    readonly unknown[],
    string | undefined
  >({
    queryKey: [...getGetV1FriendsQueryKey({ limit }), 'infinite'] as const,
    queryFn: async ({ pageParam, signal }) =>
      // The orval fetcher's runtime return is the bare body (the
      // `{data,status,headers}` envelope is a generated TYPE
      // overlay; the mutator returns the unwrapped JSON). Cast
      // matches the runtime shape — same pattern as friends.tsx etc.
      (await getV1Friends({ limit, cursor: pageParam }, { signal })) as unknown as FriendsResp,
    initialPageParam: undefined,
    getNextPageParam: (last) => nextCursorOf(last),
    ...opts?.query,
  });
}

// ---------- conversations list (chats) ----------

type ConvsResp = InternalHandlerHttpConversationListResponse;

export function useInfiniteConversations(
  opts?: { limit?: number } & {
    query?: Omit<
      UseInfiniteQueryOptions<
        ConvsResp,
        Error,
        InfiniteData<ConvsResp>,
        readonly unknown[],
        string | undefined
      >,
      'queryKey' | 'queryFn' | 'getNextPageParam' | 'initialPageParam'
    >;
  }
) {
  const limit = opts?.limit ?? DEFAULT_LIMIT;
  return useInfiniteQuery<
    ConvsResp,
    Error,
    InfiniteData<ConvsResp>,
    readonly unknown[],
    string | undefined
  >({
    queryKey: [...getGetV1ConversationsQueryKey({ limit }), 'infinite'] as const,
    queryFn: async ({ pageParam, signal }) =>
      (await getV1Conversations({ limit, cursor: pageParam }, { signal })) as unknown as ConvsResp,
    initialPageParam: undefined,
    getNextPageParam: (last) => nextCursorOf(last),
    ...opts?.query,
  });
}

// ---------- user search ----------

type UsersResp = InternalHandlerHttpUserListResponse;

export function useInfiniteUsers(
  params: { q: string; limit?: number },
  opts?: {
    query?: Omit<
      UseInfiniteQueryOptions<
        UsersResp,
        Error,
        InfiniteData<UsersResp>,
        readonly unknown[],
        string | undefined
      >,
      'queryKey' | 'queryFn' | 'getNextPageParam' | 'initialPageParam'
    >;
  }
) {
  const limit = params.limit ?? DEFAULT_LIMIT;
  return useInfiniteQuery<
    UsersResp,
    Error,
    InfiniteData<UsersResp>,
    readonly unknown[],
    string | undefined
  >({
    queryKey: [...getGetV1UsersQueryKey({ q: params.q, limit }), 'infinite'] as const,
    queryFn: async ({ pageParam, signal }) =>
      (await getV1Users(
        { q: params.q, limit, cursor: pageParam },
        { signal }
      )) as unknown as UsersResp,
    initialPageParam: undefined,
    getNextPageParam: (last) => nextCursorOf(last),
    ...opts?.query,
  });
}

// ---------- messages list (conversation thread) ----------

type MsgsResp = InternalHandlerHttpMessageListResponse;

export function useInfiniteMessages(
  conversationId: string,
  params?: { limit?: number; q?: string },
  opts?: {
    query?: Omit<
      UseInfiniteQueryOptions<
        MsgsResp,
        Error,
        InfiniteData<MsgsResp>,
        readonly unknown[],
        string | undefined
      >,
      'queryKey' | 'queryFn' | 'getNextPageParam' | 'initialPageParam'
    >;
  }
) {
  const limit = params?.limit ?? DEFAULT_LIMIT;
  const q = params?.q;
  return useInfiniteQuery<
    MsgsResp,
    Error,
    InfiniteData<MsgsResp>,
    readonly unknown[],
    string | undefined
  >({
    queryKey: [
      ...getGetV1ConversationsIdMessagesQueryKey(conversationId, { limit, q }),
      'infinite',
    ] as const,
    queryFn: async ({ pageParam, signal }) =>
      (await getV1ConversationsIdMessages(
        conversationId,
        { limit, cursor: pageParam, q },
        { signal }
      )) as unknown as MsgsResp,
    initialPageParam: undefined,
    getNextPageParam: (last) => nextCursorOf(last),
    ...opts?.query,
    // `enabled` is computed AFTER the spread so a caller can't
    // override the conversationId guard (passing { enabled: true }
    // with an empty id would otherwise fire a request that 400s
    // immediately). The caller's enabled is still respected as an
    // upper bound — if they passed false, we honour that
    // (CodeRabbit on PR #138).
    enabled: Boolean(conversationId) && (opts?.query?.enabled ?? true),
  });
}
