// Singleton fetch wrapper per spec §4.7 + §4.10. Every API call
// goes through here so:
//   - cookies (`credentials: 'include'`) round-trip — backend uses
//     scs cookies, not Bearer.
//   - mutations (POST/PATCH/PUT) carry an `Idempotency-Key` header.
//     Callers pass the key in via `init.idempotencyKey` so retries
//     can reuse the same key (per §4.7).
//   - non-2xx responses surface as `APIError` with the parsed error
//     envelope. Backend wire shape is `{ error: { code, message,
//     fields, retry_after_seconds } }` (handler/http/respond.go's
//     `ErrorResponse`); the error toaster reads `.code` / `.message`.
//
// We deliberately do NOT auto-retry inside this layer — that's
// React Query's job (`retry` + `retryDelay` config in §4.10). A
// raw fetch failure here just throws and lets the mutation layer
// decide whether to retry.
import { API_BASE_URL } from '@/lib/env';
import { newIdempotencyKey } from '@/lib/api/idempotency';

// Mirrors apps/backend/internal/handler/http/respond.go's
// ErrorBody / ErrorField / ErrorResponse — kept in sync via
// `just gen-client` (the generated lib/api/model contains the
// canonical Swagger-derived types; this mirror exists so the
// fetcher can extract message / code without importing every
// generated model file).
export type APIErrorField = {
  field: string;
  code: string;
  message: string;
};

export type APIErrorBody = {
  code: string;
  message: string;
  fields?: APIErrorField[];
  retry_after_seconds?: number;
};

export type APIErrorResponse = {
  error: APIErrorBody;
};

function isAPIErrorResponse(value: unknown): value is APIErrorResponse {
  if (!value || typeof value !== 'object') return false;
  const err = (value as { error?: unknown }).error;
  return (
    !!err &&
    typeof err === 'object' &&
    typeof (err as APIErrorBody).code === 'string' &&
    typeof (err as APIErrorBody).message === 'string'
  );
}

export class APIError extends Error {
  status: number;
  body: APIErrorBody | null;

  constructor(status: number, body: APIErrorBody | null, message: string) {
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
  // FormData bodies (avatar / attachment uploads) need fetch to set
  // the multipart boundary itself — pre-setting Content-Type strips
  // the boundary and the upload fails on the server side. (CR on
  // PR #115.)
  const isFormData = typeof FormData !== 'undefined' && init.body instanceof FormData;
  if (init.body && !isFormData && !headers.has('Content-Type')) {
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
  let body: unknown = bodyText;
  if (isJson && bodyText) {
    try {
      body = JSON.parse(bodyText);
    } catch {
      // Malformed JSON shouldn't surface as a raw SyntaxError —
      // callers depend on `error instanceof APIError` for retry +
      // toast logic. Preserve that contract on error responses;
      // for ok-but-invalid bodies, throw a plain Error so the
      // failure stays visible. (CR on PR #115.)
      if (!res.ok) {
        throw new APIError(res.status, null, `HTTP ${res.status}`);
      }
      throw new Error('Invalid JSON response body');
    }
  }

  if (!res.ok) {
    const errorBody = isJson && isAPIErrorResponse(body) ? body.error : null;
    const message = errorBody?.message ?? `HTTP ${res.status}`;
    throw new APIError(res.status, errorBody, message);
  }

  return body as T;
}
