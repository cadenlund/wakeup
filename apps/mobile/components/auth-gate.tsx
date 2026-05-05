// Auth-state redirect (Phase 3.4). Mounted inside the route stack
// so `useSegments()` reports the current group. Behaviour:
//
//   - First successful /v1/auth/me with data → signed in.
//   - Anything else once the query has settled (401, network error,
//     5xx, undefined data) on cold launch → signed out → /login.
//     We stay aggressive here because the gate is the FIRST gate the
//     user hits; better to show the login screen than a tab bar
//     against a backend the device can't reach.
//   - On subsequent refetch failures with cached data already on
//     screen, we keep the user signed in. A network blip during use
//     shouldn't kick anyone to login.
//
// We rely on the QueryClient's 401 toast suppression (§4.6) so the
// initial unauthenticated boot doesn't flash a "Request failed"
// banner before redirecting.
//
// While `useGetV1AuthMe` is in-flight we render `null`, leaving the
// splash screen visible. Once auth resolves we either redirect or
// render the children. This avoids the "tab bar appears for 200ms
// then snaps to login" flicker.
import { useRouter, useSegments } from 'expo-router';
import * as React from 'react';

import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';

export function AuthGate({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const segments = useSegments();
  const { data, error, isLoading, isFetched } = useGetV1AuthMe({
    query: {
      retry: false,
      // Refetch on focus so the gate notices "session expired" when
      // the user backgrounds + foregrounds the app.
      refetchOnWindowFocus: true,
    },
  });

  // Route group `(auth)` is a layout-only segment, so `segments[0]`
  // shows it as `(auth)` literally. Comparing as a wider string lets
  // the typed-route narrowing of segments[] not fight us.
  const inAuthGroup = (segments as string[])[0] === '(auth)';
  // Any settled state with no usable data (401, network error, 5xx,
  // or empty body) is treated as signed out at gate time. `data` is
  // truthy only when /v1/auth/me returned a real user payload.
  const isAuthenticated = !!data && !error;
  const isUnauthenticated = isFetched && !isAuthenticated;

  React.useEffect(() => {
    if (isLoading) return;
    if (isUnauthenticated && !inAuthGroup) {
      router.replace('/login');
    } else if (isAuthenticated && inAuthGroup) {
      router.replace('/');
    }
  }, [isLoading, isUnauthenticated, isAuthenticated, inAuthGroup, router]);

  // Hide the tree until /v1/auth/me has resolved at least once. The
  // splash screen stays visible (assuming SplashScreen.preventAutoHide
  // hasn't been called); after first resolve we render normally.
  if (isLoading) return null;

  return <>{children}</>;
}
