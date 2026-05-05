// Bridge between Orval's `httpClient: 'fetch'` mutator signature and
// our apiFetch wrapper. Orval generates calls of the form:
//   orvalMutator<T>(url, { method, headers, body, ... })
// We forward those into apiFetch so cookies, idempotency keys, and
// APIError surfacing all route through one place — the spec rule
// "no hand-rolled HTTP fetches" depends on this being the only HTTP
// entry point.
import { apiFetch } from '@/lib/api/client';

export const orvalMutator = <T = unknown>(url: string, init?: RequestInit): Promise<T> => {
  return apiFetch<T>(url, init);
};

export default orvalMutator;
