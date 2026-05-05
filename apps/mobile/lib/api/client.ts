// Singleton fetch wrapper per spec §4.7 + §4.10. Every API call
// goes through here so:
//   - cookies (`credentials: 'include'`) round-trip — backend uses
//     scs cookies, not Bearer.
//   - mutations (POST/PATCH/PUT) carry an `Idempotency-Key` header.
//     Callers pass the key in via `init.idempotencyKey` so retries
//     can reuse the same key (per §4.7).
//   - non-2xx responses surface as `APIError` with the parsed
//     problem-details body. The error toaster (`onError` in the
//     React Query config) reads `.title` / `.detail`.
//
// We deliberately do NOT auto-retry inside this layer — that's
// React Query's job (`retry` + `retryDelay` config in §4.10). A
// raw fetch failure here just throws and lets the mutation layer
// decide whether to retry.
import { API_BASE_URL } from '@/lib/env';
import { newIdempotencyKey } from '@/lib/api/idempotency';

export type ProblemDetails = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
  errors?: { field: string; message: string }[];
};

export class APIError extends Error {
  status: number;
  body: ProblemDetails | null;

  constructor(status: number, body: ProblemDetails | null, message: string) {
    super(message);
    this.name = 'APIError';
    this.status = status;
    this.body = body;
  }
}

type RequestInitExt = RequestInit & {
  idempotencyKey?: string;
};

const MUTATING_METHODS = new Set(['POST', 'PATCH', 'PUT', 'DELETE']);

export async function apiFetch<T = unknown>(path: string, init: RequestInitExt = {}): Promise<T> {
  const url = path.startsWith('http') ? path : `${API_BASE_URL}${path}`;
  const method = (init.method ?? 'GET').toUpperCase();

  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  if (MUTATING_METHODS.has(method) && !headers.has('Idempotency-Key')) {
    headers.set('Idempotency-Key', init.idempotencyKey ?? newIdempotencyKey());
  }

  const res = await fetch(url, {
    ...init,
    method,
    headers,
    credentials: 'include',
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const contentType = res.headers.get('content-type') ?? '';
  const isJson = contentType.includes('application/json');
  const bodyText = await res.text();
  const body: unknown = isJson && bodyText ? JSON.parse(bodyText) : bodyText;

  if (!res.ok) {
    const problem = isJson && body && typeof body === 'object' ? (body as ProblemDetails) : null;
    const message = problem?.title ?? problem?.detail ?? `HTTP ${res.status}`;
    throw new APIError(res.status, problem, message);
  }

  return body as T;
}
