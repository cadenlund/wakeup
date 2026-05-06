import '../global.css';
// Side-effect import: silences known third-party dev-log noise
// (Reanimated strict mode false-positives, RN-screens pointerEvents
// deprecation) before any worklet or screen mounts.
import '@/lib/dev-warnings';
// Side-effect import: `lib/sentry` runs `Sentry.init()` at module load
// before the React tree mounts. The export is the integration that
// the navigation container ref hooks into below.
import { Sentry, navigationIntegration, sentryEnabled } from '@/lib/sentry';

import {
  Stack,
  useGlobalSearchParams,
  useNavigationContainerRef,
  usePathname,
  useRootNavigationState,
  useRouter,
} from 'expo-router';
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
  const params = useGlobalSearchParams<{ token?: string }>();
  const router = useRouter();
  // `useRootNavigationState()` returns null until the root navigator
  // is mounted. router.replace before that throws "Attempted to
  // navigate before mounting the Root Layout component." We gate the
  // imperative redirect effect below on `navReady` so it can't fire
  // until the Stack underneath us has registered with expo-router.
  const navReady = !!useRootNavigationState()?.key;
  // Token-bearing routes derived from expo-router's resolved
  // pathname + params, NOT window.location. The browser URL bar can
  // lag the router state during the post-reset → /login transition,
  // which previously left `hasToken=true` for a render after expo-
  // router had already moved on — keeping (auth) guards loose past
  // their welcome.
  const tokenParam = typeof params[TOKEN_PARAM] === 'string' ? params[TOKEN_PARAM] : '';
  const hasToken = TOKEN_BEARING_PATHS.has(pathname) && !!tokenParam;

  // Belt-and-braces redirect alongside Stack.Protected. The Protected
  // groups handle initial mount (don't show /(tabs) on cold start
  // when auth isn't loaded yet, etc.), but mid-session transitions
  // can race the React render — most visibly on the post-reset login
  // path, where the (auth) back-stack carries /reset history that
  // confuses Stack.Protected's group switch and leaves the user
  // stranded on /login until a manual reload.
  //
  // The actual replace is wrapped in setTimeout(0) so it lands on the
  // next tick — `useRootNavigationState()` flips truthy a render
  // before `router.replace` is genuinely safe, and the bare call
  // throws "Attempted to navigate before mounting the Root Layout
  // component." Deferring one tick clears that.
  React.useEffect(() => {
    if (!navReady) return;
    if (auth.isLoading || hasToken) return;
    const inAuth = AUTH_PATHS.has(pathname);

    let target: '/' | '/(onboarding)' | '/login' | null = null;
    if (auth.isAuthenticated && auth.onboardingDone && inAuth) {
      target = '/';
    } else if (auth.isAuthenticated && !auth.onboardingDone && inAuth) {
      target = '/(onboarding)';
    } else if (!auth.isAuthenticated && !inAuth) {
      target = '/login';
    }
    if (!target) return;

    const t = target;
    const id = setTimeout(() => router.replace(t), 0);
    return () => clearTimeout(id);
  }, [
    navReady,
    auth.isLoading,
    auth.isAuthenticated,
    auth.onboardingDone,
    hasToken,
    pathname,
    router,
  ]);

  // While auth is loading, ALL three groups are in the navigation
  // tree. This is the only way to keep Stack.Protected from trying to
  // fix up the URL during the cold-start window — if the URL matches
  // a route that isn't currently in the tree, Stack.Protected
  // synchronously redirects, which on the first render throws
  // "Attempted to navigate before mounting the Root Layout component."
  // Once auth resolves, the loaded-state guards tighten to expose
  // exactly the group the user belongs in.
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
