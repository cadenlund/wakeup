// Auth + onboarding gate. Mounted inside the route stack so
// `useSegments()` reports the current group. Cold-start order:
//
//   1. Wait for `/v1/auth/me` to settle.
//      - 401 / network / 5xx / empty data → unauthenticated.
//        Redirect into (auth) unless the user is already there
//        (or on /forgot or /reset, which are part of the (auth)
//        group anyway).
//      - 200 with `me.onboarded_at == null` → authenticated but
//        first-launch. Route to (onboarding); the carousel calls
//        `POST /v1/users/me/onboarding/complete` on finish, which
//        invalidates the `me` query, which flips this branch on the
//        next render and falls through to (tabs).
//      - 200 with onboarded_at set → fully signed in. If the user
//        is currently in (auth) or (onboarding), replace to `/`.
//   2. Refetch failures with cached data don't kick the user out;
//      cached `data` keeps `isAuthenticated` true through transient
//      errors.
//
// We rely on the QueryClient's 401 toast suppression (§4.6) so the
// initial unauthenticated boot doesn't flash a "Request failed"
// banner before redirecting.
//
// While the gate is still resolving we render `null`, leaving the
// splash screen visible. This avoids the "tab bar flashes for
// 200ms then snaps to login" flicker.
import { useRouter, useSegments } from 'expo-router';
import * as React from 'react';

import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';

export function AuthGate({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const segments = useSegments();
  const { data, error, isLoading, isFetched } = useGetV1AuthMe({
    query: {
      retry: false,
      refetchOnWindowFocus: true,
    },
  });

  // Route group `(auth)` / `(onboarding)` are layout-only segments,
  // so `segments[0]` shows them literally. Wider-string comparison
  // sidesteps the typed-route narrowing of segments[].
  const segment0 = (segments as string[])[0];
  const inAuthGroup = segment0 === '(auth)';
  const inOnboardingGroup = segment0 === '(onboarding)';

  // apiFetch returns the unwrapped JSON body, but Orval types
  // the response as `{data, status, headers}`. Cast to the actual
  // runtime shape the backend sends (MeResponse fields directly).
  const me = data as { id?: string; onboarded_at?: string } | undefined;
  const isAuthenticated = !!me?.id && !error;
  const isUnauthenticated = isFetched && !isAuthenticated;
  const onboardingDone = !!me?.onboarded_at;

  React.useEffect(() => {
    if (isLoading) return;

    if (isUnauthenticated && !inAuthGroup) {
      router.replace('/login');
      return;
    }

    if (isAuthenticated) {
      if (!onboardingDone && !inOnboardingGroup) {
        router.replace('/(onboarding)');
      } else if (onboardingDone && (inAuthGroup || inOnboardingGroup)) {
        // Explicit '/(tabs)' instead of '/' — when navigating from a
        // non-tabs group, expo-router can resolve '/' to whatever
        // segment the AuthGate previously redirected to (the
        // onboarding group, in our case), bouncing the user back
        // through the carousel after re-login.
        router.replace('/(tabs)');
      }
    }
  }, [
    isLoading,
    isAuthenticated,
    isUnauthenticated,
    onboardingDone,
    inAuthGroup,
    inOnboardingGroup,
    router,
  ]);

  if (isLoading) return null;

  return <>{children}</>;
}
