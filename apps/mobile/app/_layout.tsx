import '../global.css';
// Side-effect import: `lib/sentry` runs `Sentry.init()` at module load
// before the React tree mounts. The export is the integration that
// the navigation container ref hooks into below.
import { Sentry, navigationIntegration } from '@/lib/sentry';

import { Stack, useNavigationContainerRef } from 'expo-router';
import * as React from 'react';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { NetworkBanner } from '@/components/network-banner';
import { ToastRoot } from '@/components/toast-root';
import { RootErrorBoundary } from '@/components/ui/root-error-boundary';
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
    <SafeAreaProvider>
      <ThemeProvider>
        <NetworkBanner />
        <RootErrorBoundary>
          <Stack>
            <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
            <Stack.Screen name="modal" options={{ presentation: 'modal' }} />
          </Stack>
        </RootErrorBoundary>
        <ToastRoot />
      </ThemeProvider>
    </SafeAreaProvider>
  );
}

export default Sentry.wrap(RootLayout);
