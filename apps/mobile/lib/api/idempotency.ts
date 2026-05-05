// Idempotency-key helpers per spec §4.7. Every POST/PATCH/PUT
// mutation generates a UUID v7 client-side; retries reuse the same
// key so the backend de-dupes. v7 is preferred over v4 because it's
// time-ordered, which makes server-side log correlation easier.
//
// The hook returns a memoised key for the lifetime of one mutation
// invocation. The `freshKey` argument lets callers force regenerate
// (e.g., user manually retries a failed message — they want a new
// dedupe window, not the prior key).
import * as React from 'react';
import { v7 as uuidv7 } from 'uuid';

export function newIdempotencyKey(): string {
  return uuidv7();
}

// `freshKey` is a caller-controlled token — bumping it forces a new
// idempotency key. Default 0 means "stable for the component's
// lifetime"; callers that want a fresh key per submit pass an
// incrementing counter.
export function useIdempotencyKey(freshKey: number = 0): string {
  return React.useMemo(() => {
    // `freshKey` reference signals to the linter that it's an
    // intentional dependency — bumping it regenerates the key.
    void freshKey;
    return uuidv7();
  }, [freshKey]);
}
