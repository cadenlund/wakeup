import '../global.css';
// Side-effect import: `lib/sentry` runs `Sentry.init()` at module load
// before the React tree mounts. The export is the integration that
// the navigation container ref hooks into below.
import { Sentry, navigationIntegration } from '@/lib/sentry';

import { Stack, useNavigationContainerRef } from 'expo-router';
import * as React from 'react';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';

import { ForceUpgradeGate } from '@/components/force-upgrade-gate';
import { NetworkBanner } from '@/components/network-banner';
import { ToastRoot } from '@/components/toast-root';
import { RootErrorBoundary } from '@/components/ui/root-error-boundary';
import { queryClient, queryPersister, shouldPersistQuery } from '@/lib/api/query-client';
import { ThemeProvider } from '@/lib/theme/provider';

export const unstable_settings = {
  // Ensure that reloading on `/modal` keeps a back button present.
  initialRouteName: '(tabs)',
};

function RootLayout() {
  const navContainerRef = useNavigationContainerRef();

  React.useEffect(() => {
    if (navContainerRef) {
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
              <Stack>
                <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
                <Stack.Screen name="modal" options={{ presentation: 'modal' }} />
              </Stack>
            </ForceUpgradeGate>
          </RootErrorBoundary>
          <ToastRoot />
        </ThemeProvider>
      </SafeAreaProvider>
    </PersistQueryClientProvider>
  );
}

export default Sentry.wrap(RootLayout);
