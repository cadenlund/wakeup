// TanStack Query singleton + persistence wiring per spec §4.10.
//
// Defaults (locked):
//   - networkMode: 'offlineFirst' on both queries and mutations so
//     the optimistic cache renders while requests are in flight,
//     and mutations queue while offline.
//   - mutations retry only for 5xx + network errors, never 4xx,
//     up to 3 attempts with exponential backoff capped at 30s.
//     The same idempotency key is reused across retries (the
//     React-Query mutation context preserves variables, and our
//     fetcher only generates a new key when none is passed in).
//
// Persistence: AsyncStorage-backed `persistQueryClient` survives a
// relaunch. Sensitive payloads (chat messages, friends list, profile
// details) DO NOT get persisted — only the explicit allowlist in
// `PERSIST_ALLOWED_PATHS` does. Sensitive data lives in expo-secure-
// store per the project storage policy. (CR on PR #115.) Cache
// version (`query-cache:v1`) bumps deliberately on schema changes.
import AsyncStorage from '@react-native-async-storage/async-storage';
import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { createAsyncStoragePersister } from '@tanstack/query-async-storage-persister';

import { APIError } from '@/lib/api/client';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import { toast } from '@/lib/toast';

const RETRY_DELAY_BASE_MS = 1000;
const RETRY_DELAY_CAP_MS = 30_000;
const MAX_RETRIES = 3;
const ONE_DAY_MS = 24 * 60 * 60 * 1000;

function shouldRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof APIError && error.status >= 400 && error.status < 500) {
    return false;
  }
  return failureCount < MAX_RETRIES;
}

function backoff(attempt: number): number {
  return Math.min(RETRY_DELAY_BASE_MS * 2 ** attempt, RETRY_DELAY_CAP_MS);
}

// Prettify a SNAKE_CASE error code into a "Sentence case" title so
// the toast reads "Validation failed" / "Rate limited" / "Resource
// not found" instead of yelling the raw enum at the user.
function prettifyCode(code: string | undefined): string {
  if (!code) return 'Request failed';
  return code
    .toLowerCase()
    .replace(/_/g, ' ')
    .replace(/^./, (c) => c.toUpperCase());
}

// Shared toast formatter. `error.body` is the inner `error` object
// (already unwrapped by apiFetch); `message` is the human-readable
// string the backend sent back.
//
// For 429s the backend includes `retry_after_seconds`. We prepend a
// human-readable "Try again in Xs / Xm" hint to the description so
// the user sees an actionable wait time instead of a generic
// "you've been rate limited" line.
function formatRetryAfter(seconds: number): string {
  if (seconds < 60) return `Try again in ${seconds}s`;
  const minutes = Math.ceil(seconds / 60);
  return `Try again in ${minutes}m`;
}

function describeAPIError(body: NonNullable<APIError['body']>): string | undefined {
  if (body.code === 'RATE_LIMITED' && body.retry_after_seconds && body.retry_after_seconds > 0) {
    const retry = formatRetryAfter(body.retry_after_seconds);
    return body.message ? `${retry}. ${body.message}` : retry;
  }
  return body.message;
}

function toastErrorBody(error: unknown) {
  if (error instanceof APIError && error.body) {
    toast.error(prettifyCode(error.body.code), describeAPIError(error.body));
    return;
  }
  const detail = error instanceof Error ? error.message : undefined;
  toast.error('Request failed', detail);
}

// Query errors: silence the auth.me cold-start 401 because the
// AuthGate is already redirecting the user to login; a toast on top
// would just clutter that transition. Everything else toasts.
function toastQueryError(error: unknown) {
  if (error instanceof APIError && error.status === 401) return;
  toastErrorBody(error);
}

// Mutation errors: always toast — including 401s like a bad-creds
// login attempt. Validation failures still toast (showing the
// prettified code + message) AND the form's `useFieldErrors` hook
// renders the per-field red text below each input. Both surfaces
// are useful: the toast is the global "something didn't work"
// signal, the inline text says exactly what to fix.
function toastMutationError(error: unknown) {
  toastErrorBody(error);
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err, query) => {
      // Background refetches that already had data shouldn't toast —
      // the user already sees the stale data and a flashing toast is
      // more noise than signal. Only toast if there's nothing on
      // screen yet.
      if (query.state.data === undefined) {
        toastQueryError(err);
      }
    },
  }),
  mutationCache: new MutationCache({
    onError: toastMutationError,
  }),
  defaultOptions: {
    queries: {
      networkMode: 'offlineFirst',
      retry: shouldRetry,
      retryDelay: backoff,
      gcTime: ONE_DAY_MS,
      staleTime: 30_000,
    },
    mutations: {
      networkMode: 'offlineFirst',
      // No automatic retries on mutations. The Orval-generated
      // `mutationFn`s don't capture a stable idempotency key across
      // retries — every retry would call `newIdempotencyKey()`
      // again, breaking backend dedupe. (CR on PR #115.) Screens
      // that need retry semantics will adopt a wrapper hook that
      // holds the key in a ref; lands alongside the first mutation-
      // bearing screen in Phase 3.
      retry: 0,
      gcTime: ONE_DAY_MS,
    },
  },
});

export const queryPersister = createAsyncStoragePersister({
  storage: AsyncStorage,
  key: STORAGE_KEYS.queryCache,
});

// Allowlist of query-key prefixes that may be persisted to
// AsyncStorage. Anything not on this list is dehydrated-and-dropped
// at persist time. Add new entries deliberately, only for endpoints
// whose response is non-sensitive and stable enough to be useful at
// relaunch time. (CR on PR #115.)
const PERSIST_ALLOWED_PATHS: ReadonlySet<string> = new Set([
  '/v1/healthz', // ForceUpgradeGate's min_client_version check.
]);

export function shouldPersistQuery(queryKey: readonly unknown[]): boolean {
  if (queryKey.length === 0) return false;
  const root = String(queryKey[0]);
  return PERSIST_ALLOWED_PATHS.has(root);
}
