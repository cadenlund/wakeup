import '../global.css';
// Side-effect import: silences known third-party dev-log noise
// (Reanimated strict mode false-positives, RN-screens pointerEvents
// deprecation) before any worklet or screen mounts.
import '@/lib/dev-warnings';
// Side-effect import: `lib/sentry` runs `Sentry.init()` at module load
// before the React tree mounts. The export is the integration that
// the navigation container ref hooks into below.
import { Sentry, navigationIntegration, sentryEnabled } from '@/lib/sentry';

import { Stack, useGlobalSearchParams, useNavigationContainerRef, usePathname } from 'expo-router';
import * as React from 'react';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';

import { useAuthState } from '@/components/auth-gate';
import { ForceUpgradeGate } from '@/components/force-upgrade-gate';
import { NetworkBanner } from '@/components/network-banner';
import { ToastRoot } from '@/components/toast-root';
import { RootErrorBoundary } from '@/components/ui/root-error-boundary';
import { queryClient, queryPersister, shouldPersistQuery } from '@/lib/api/query-client';
import { ThemeProvider } from '@/lib/theme/provider';

// `unstable_settings.initialRouteName` is intentionally NOT set here:
// at the root it rewrites cold-start deep-link URLs (e.g.
// `/reset?token=…`) to the named route's path before user-space code
// runs, which broke our `/reset` flow on web. Per
// https://docs.expo.dev/router/advanced/router-settings/ the setting
// is back-stack scaffolding, not URL routing — apply it in nested
// `_layout.tsx` files (e.g. (modal)) when those flows need it.

// Routes that carry a one-time token in their query string. The
// (auth) group's guard stays open while the user is sitting on one
// of these so a logged-in stale-session user can still complete the
// reset email click (otherwise they'd be routed straight to (tabs)
// and lose the token). Adding a future verify-email route means
// adding `/verify` here.
const TOKEN_BEARING_PATHS = new Set(['/reset', '/forgot']);
const TOKEN_PARAM = 'token';

// `useAuthState` reads from the QueryClient, so this lives below
// PersistQueryClientProvider. The Stack stays mounted at all times —
// unmounting it destroys expo-router's route tree and a deep-link
// URL (`/reset?token=…`) on cold start falls back to `/` before it
// can be matched. We fold `isLoading` into the guards instead.
//
// Auth-state-changing flows (login, register, logout, reset,
// onboarding-complete) imperatively `router.replace` from
// `lib/auth/post-auth-nav` so the user lands on the right group
// after a state flip; Stack.Protected then enforces the guard so
// the wrong group can't render.
function ProtectedStack() {
  const auth = useAuthState();
  const pathname = usePathname();
  const params = useGlobalSearchParams<{ token?: string }>();
  // Token-bearing routes derived from expo-router's resolved
  // pathname + params (not window.location, which lags during
  // mid-session transitions). The (auth) group's guard stays open
  // while the user sits on a token URL so a logged-in stale-session
  // user can still complete the reset email click.
  const tokenParam = typeof params[TOKEN_PARAM] === 'string' ? params[TOKEN_PARAM] : '';
  const hasToken = TOKEN_BEARING_PATHS.has(pathname) && !!tokenParam;

  // While auth is loading, ALL three groups are in the navigation
  // tree. This keeps Stack.Protected from trying to fix up the URL
  // during the cold-start window — if the URL matches a route that
  // isn't currently in the tree, Stack.Protected synchronously
  // redirects, which on the first render throws "Attempted to
  // navigate before mounting the Root Layout component." Once auth
  // resolves, the loaded-state guards tighten to expose exactly the
  // group the user belongs in.
  return (
    <Stack>
      {/* `Stack.Protected` is the canonical Expo Router auth pattern:
          https://docs.expo.dev/router/advanced/protected/ */}
      <Stack.Protected guard={auth.isLoading || !auth.isAuthenticated || hasToken}>
        <Stack.Screen name="(auth)" options={{ headerShown: false }} />
      </Stack.Protected>

      <Stack.Protected
        guard={auth.isLoading || (auth.isAuthenticated && !auth.onboardingDone && !hasToken)}>
        <Stack.Screen name="(onboarding)" options={{ headerShown: false }} />
      </Stack.Protected>

      <Stack.Protected
        guard={auth.isLoading || (auth.isAuthenticated && auth.onboardingDone && !hasToken)}>
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="modal" options={{ presentation: 'modal' }} />
        <Stack.Screen name="conversations/new" options={{ presentation: 'modal' }} />
        {/* `headerShown: false` lives here, not inside search.tsx —
            toggling it from inside the screen body triggers an
            infinite-remount loop on iOS modals (the screen re-mounts
            on each header-options diff, and the screen always sets
            the same option, so it never settles). */}
        <Stack.Screen name="search" options={{ presentation: 'modal', headerShown: false }} />
      </Stack.Protected>
    </Stack>
  );
}

function RootLayout() {
  const navContainerRef = useNavigationContainerRef();

  React.useEffect(() => {
    if (sentryEnabled && navContainerRef) {
      navigationIntegration.registerNavigationContainer(navContainerRef);
    }
  }, [navContainerRef]);

  return (
    <PersistQueryClientProvider
      client={queryClient}
      persistOptions={{
        persister: queryPersister,
        maxAge: 24 * 60 * 60 * 1000,
        // Only allowlisted, non-sensitive queries get dehydrated to
        // AsyncStorage. Chat / friends / profile data stays in
        // memory and refetches on launch. (CR on PR #115.)
        dehydrateOptions: {
          shouldDehydrateQuery: (query) => shouldPersistQuery(query.queryKey),
        },
      }}>
      <SafeAreaProvider>
        <ThemeProvider>
          <NetworkBanner />
          <RootErrorBoundary>
            <ForceUpgradeGate>
              <ProtectedStack />
            </ForceUpgradeGate>
          </RootErrorBoundary>
          <ToastRoot />
        </ThemeProvider>
      </SafeAreaProvider>
    </PersistQueryClientProvider>
  );
}

// Only wrap when Sentry actually initialised — wrap-without-init
// triggers a "App Start Span could not be finished" warning in dev
// where the DSN is empty.
export default sentryEnabled ? Sentry.wrap(RootLayout) : RootLayout;
