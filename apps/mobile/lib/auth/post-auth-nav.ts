// Single source of truth for post-auth-state-change navigation.
// Five flows used to each have their own setQueryData + invalidate +
// setTimeout combination — login, register, onboarding-complete,
// logout, and the native branch of password-reset success. The
// shapes drifted enough that bugs only reproduced in one of them at
// a time. Callers now use `signedInAs` / `signedOut` and don't have
// to remember the order.
//
// What each helper does:
//
//   signedInAs(qc, router, user)
//     1. setQueryData(meKey, user) — flips the auth-gate state
//        synchronously so Stack.Protected guards re-evaluate.
//     2. invalidateQueries(meKey) — fire-and-forget refetch to
//        reconcile against the canonical /me. We don't await it;
//        on iOS the URLSession cookie store occasionally hasn't
//        processed the Set-Cookie header by the time the next
//        request goes out, and a 401 there would knock the cache
//        back to error before the navigate could fire.
//     3. setTimeout(0) → router.replace — defers one tick so the
//        cache update has propagated to Stack.Protected guards
//        before the route change lands.
//
//   signedOut(qc, router)
//     1. cancelQueries(meKey) — pre-empts any in-flight /me refetch
//        triggered by the logout response.
//     2. setQueryData(meKey, null) — definitive "no me" signal that
//        forces isAuthenticated=false on next render. Does NOT use
//        removeQueries — that would signal observers the data is
//        gone and trigger an immediate refetch, which can race the
//        cookie-clear and resurrect a stale me before
//        Stack.Protected reacts.
//     3. setTimeout(0) → router.replace('/login') — same defer
//        rationale as signedInAs.
//
// Reset-success on web isn't covered here because it does a full
// page reload (`window.location.assign('/login')`) instead — see
// (auth)/reset.tsx for that branch.
import type { Router } from 'expo-router';
import type { QueryClient } from '@tanstack/react-query';

import { getGetV1AuthMeQueryKey } from '@/lib/api/hooks/auth/auth';

// `id` is required — a partial user payload would silently skip the
// cache prime and recreate the guard-race this helper exists to
// prevent. Callers narrow before calling.
export type AuthUser = {
  id: string;
  onboarded_at?: string | null;
};

type RouterReplace = Pick<Router, 'replace'>;

// Decide which route group a freshly-authenticated user belongs in.
// `'/'` resolves to (tabs)/index via Stack.Protected's group guards;
// `'/(onboarding)'` is explicit because '/' would otherwise pick
// (tabs) for an onboarded-but-just-logged-in user when the
// onboarding-pending cache flip hasn't fully landed.
function targetForUser(user: AuthUser): '/' | '/(onboarding)' {
  return user.onboarded_at ? '/' : '/(onboarding)';
}

export function signedInAs(qc: QueryClient, router: RouterReplace, user: AuthUser) {
  qc.setQueryData(getGetV1AuthMeQueryKey(), user);
  void qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
  const target = targetForUser(user);
  setTimeout(() => router.replace(target), 0);
}

export async function signedOut(qc: QueryClient, router: RouterReplace) {
  await qc.cancelQueries({ queryKey: getGetV1AuthMeQueryKey() });
  qc.setQueryData(getGetV1AuthMeQueryKey(), null);
  setTimeout(() => router.replace('/login'), 0);
}
