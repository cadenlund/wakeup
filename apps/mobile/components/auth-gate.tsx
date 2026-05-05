// Auth state hook for the route gates in `app/_layout.tsx`. Reads
// `/v1/auth/me` and exposes the small flag bag the layout's
// `<Stack.Protected guard={…}>` blocks need.
//
// Route-level protection (Stack.Protected) is the canonical Expo
// Router auth pattern — see
// https://docs.expo.dev/router/advanced/protected/.
import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';

export type AuthState = {
  /** True until `/v1/auth/me` settles (success OR error). */
  isLoading: boolean;
  isAuthenticated: boolean;
  isUnauthenticated: boolean;
  onboardingDone: boolean;
};

export function useAuthState(): AuthState {
  const { data, isLoading, isFetched } = useGetV1AuthMe({
    query: {
      retry: false,
      refetchOnWindowFocus: true,
    },
  });
  // apiFetch returns the unwrapped JSON body, but Orval types the
  // response as `{data, status, headers}`. Cast to the runtime shape
  // the backend actually sends (MeResponse fields directly).
  const me = data as { id?: string; onboarded_at?: string } | undefined;
  // Trust cached `me` over a transient `error` — TanStack keeps the
  // last successful response when a refetch fails (offline, cold 5xx,
  // etc.) and CR rightly flagged that AND-ing in `!error` would bounce
  // a logged-in user out on the first network blip. (CR on PR #117.)
  const isAuthenticated = !!me?.id;
  return {
    isLoading,
    isAuthenticated,
    isUnauthenticated: isFetched && !isAuthenticated,
    onboardingDone: !!me?.onboarded_at,
  };
}
