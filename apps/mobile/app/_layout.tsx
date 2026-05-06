import '../global.css';
// Side-effect import: silences known third-party dev-log noise
// (Reanimated strict mode false-positives, RN-screens pointerEvents
// deprecation) before any worklet or screen mounts.
import '@/lib/dev-warnings';
// Side-effect import: `lib/sentry` runs `Sentry.init()` at module load
// before the React tree mounts. The export is the integration that
// the navigation container ref hooks into below.
import { Sentry, navigationIntegration, sentryEnabled } from '@/lib/sentry';

import { Stack, useNavigationContainerRef, usePathname, useRouter } from 'expo-router';
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

// Token-bearing URLs (password reset, email verify) keep `(auth)`
// reachable even for logged-in users with stale sessions. We read the
// flag from `window.location.search` and keep it in state — refreshed
// whenever the pathname changes so the flag drops once the user has
// navigated away from the token URL (e.g. router.replace('/login')
// after a successful reset).
function readURLToken(): boolean {
  if (typeof window === 'undefined' || !window.location) return false;
  return /[?&]token=/.test(window.location.search ?? '');
}

// `useAuthState` reads from the QueryClient, so this lives below
// PersistQueryClientProvider. It also intentionally never returns
// `null` — unmounting the Stack destroys expo-router's route tree,
// which causes deep-link URLs (e.g. `/reset?token=…`) to fall back to
// `/` before they can be matched. We instead fold `isLoading` into the
// group guards.
// Routes the (auth) group claims. Used by the redirect effect below
// to decide whether the user is currently sitting in /(auth) — the
// (auth) layout strips the group prefix so pathname is `/login` etc.,
// not `/(auth)/login`. Listing them here is cheaper than walking the
// route tree at render time.
const AUTH_PATHS = new Set(['/login', '/register', '/forgot', '/reset']);

function ProtectedStack() {
  const auth = useAuthState();
  const pathname = usePathname();
  const router = useRouter();
  const [hasToken, setHasToken] = React.useState(readURLToken);
  // Re-read the URL whenever pathname changes — once the user has
  // navigated away from `/reset?token=…` (e.g. after submitting the
  // form), `hasToken` drops to false and the (auth)-only guard relaxes
  // so a subsequent successful sign-in can route to (tabs).
  React.useEffect(() => {
    setHasToken(readURLToken());
  }, [pathname]);

  // Belt-and-braces redirect alongside Stack.Protected. The Protected
  // groups handle initial mount (don't show /(tabs) on cold start
  // when auth isn't loaded yet, etc.), but mid-session transitions
  // can race the React render — most visibly on the post-reset login
  // path, where the (auth) back-stack carries /reset history that
  // confuses Stack.Protected's group switch and leaves the user
  // stranded on /login until a manual reload. This effect runs AFTER
  // every reconciliation so the cache flip from setQueryData has
  // already landed by the time we look at auth state. (CR-style
  // belt: AuthGate's old imperative redirect, alongside the new
  // declarative Stack.Protected gating.)
  React.useEffect(() => {
    if (auth.isLoading || hasToken) return;
    const inAuth = AUTH_PATHS.has(pathname);

    if (auth.isAuthenticated && auth.onboardingDone && inAuth) {
      router.replace('/');
    } else if (auth.isAuthenticated && !auth.onboardingDone && inAuth) {
      router.replace('/(onboarding)');
    } else if (!auth.isAuthenticated && !inAuth) {
      router.replace('/login');
    }
  }, [auth.isLoading, auth.isAuthenticated, auth.onboardingDone, hasToken, pathname, router]);

  return (
    <Stack>
      {/* `Stack.Protected` is the canonical Expo Router auth pattern:
          each group is in the navigation tree only when its guard is
          true, so the wrong screen never mounts on cold start.
          https://docs.expo.dev/router/advanced/protected/ */}
      <Stack.Protected guard={auth.isLoading || !auth.isAuthenticated || hasToken}>
        <Stack.Screen name="(auth)" options={{ headerShown: false }} />
      </Stack.Protected>

      <Stack.Protected
        guard={!auth.isLoading && auth.isAuthenticated && !auth.onboardingDone && !hasToken}>
        <Stack.Screen name="(onboarding)" options={{ headerShown: false }} />
      </Stack.Protected>

      <Stack.Protected
        guard={!auth.isLoading && auth.isAuthenticated && auth.onboardingDone && !hasToken}>
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="modal" options={{ presentation: 'modal' }} />
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
