// Extracts per-field validation messages from a 422 APIError. The
// backend returns `{ error: { code: 'VALIDATION_FAILED', fields:
// [{ field, code, message }, ...] } }` (see handler/http/respond.go).
// This hook flattens that into a `{ [fieldName]: message }` map so
// forms can render a `<FieldError message={errors.username} />`
// next to each input without manually walking the array on every
// render.
//
// Returns `{}` for any non-validation error (or no error at all),
// so callers can blindly index without null checks.
import * as React from 'react';

import { APIError } from '@/lib/api/client';

export type FieldErrors = Record<string, string>;

// Top-level error message for the form — what we'd show below the
// submit button as a one-line red summary. Returns undefined when:
//   - there's no error
//   - the error is a VALIDATION_FAILED (the per-field errors are
//     already shown next to each input — repeating the top-level
//     "validation failed" message would be noise)
// Otherwise returns the backend's `error.message` string, or a
// generic fallback for non-APIError throws.
export function useTopLevelError(error: unknown): string | undefined {
  return React.useMemo(() => {
    if (!error) return undefined;
    if (error instanceof APIError) {
      if (error.body?.code === 'VALIDATION_FAILED') return undefined;
      return error.body?.message ?? `Request failed (${error.status})`;
    }
    if (error instanceof Error) return error.message;
    return 'Request failed';
  }, [error]);
}

export function useFieldErrors(error: unknown): FieldErrors {
  return React.useMemo(() => {
    if (!(error instanceof APIError)) return {};
    if (error.body?.code !== 'VALIDATION_FAILED') return {};
    const out: FieldErrors = {};
    for (const f of error.body.fields ?? []) {
      out[f.field] = f.message;
    }
    return out;
  }, [error]);
}
