// Last-line-of-defense error boundary mounted at the route stack
// root (per spec §4.10). Catches uncaught render errors anywhere in
// the tree, reports them to Sentry tagged with the surface, and
// renders a minimal fallback with a single "Reload" CTA.
//
// We intentionally don't try to be clever here — no retry, no
// auto-recovery, no analytics on the dismissal. The error already
// took down the React tree; the user's only path forward is to
// restart the JS bundle, which `Updates.reloadAsync()` does.
//
// Note: React's class-component error boundaries do NOT swallow
// `SuspenseException` (that bubbles to the nearest `<Suspense>`),
// which is the behaviour the spec requires. No special handling
// needed — it's the default.
import * as Updates from 'expo-updates';
import * as React from 'react';
import { View } from 'react-native';

import { Button } from '@/components/ui/button';
import { Text } from '@/components/ui/text';
import { Sentry } from '@/lib/sentry';

type Props = {
  children: React.ReactNode;
};

type State = {
  error: Error | null;
};

export class RootErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    Sentry.captureException(error, {
      tags: { surface: 'react-error-boundary' },
      extra: { componentStack: info.componentStack },
    });
  }

  reload = () => {
    void Updates.reloadAsync();
  };

  render() {
    if (this.state.error) {
      return (
        <View className="flex-1 items-center justify-center gap-4 bg-background px-8">
          <Text variant="h3" className="text-center">
            Something went wrong
          </Text>
          <Text variant="muted" className="text-center">
            Try restarting the app. The error has been reported.
          </Text>
          <Button onPress={this.reload}>
            <Text>Reload</Text>
          </Button>
        </View>
      );
    }

    return this.props.children;
  }
}
