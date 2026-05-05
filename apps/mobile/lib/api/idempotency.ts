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
// idempotency key. The argument is required (no default) because a
// shared default would silently dedupe distinct submissions when
// callers forget to bump it. Pass a stable value (e.g. 0) to keep
// retries idempotent within one mutation invocation; bump it per
// submit to force a new dedupe window. (CR on PR #115.)
export function useIdempotencyKey(freshKey: number): string {
  return React.useMemo(() => {
    // `freshKey` reference signals to the linter that it's an
    // intentional dependency — bumping it regenerates the key.
    void freshKey;
    return uuidv7();
  }, [freshKey]);
}
