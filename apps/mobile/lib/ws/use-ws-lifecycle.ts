// Phase 7.2 — WebSocket connection lifecycle.
//
// The client (`lib/ws/client.ts`) owns the mechanics (open / close /
// backoff reconnect); this hook owns the POLICY of *when* a
// connection should exist:
//
//   - Connect when the app is foreground AND the user is
//     authenticated. `connectWS()` is idempotent, so re-running the
//     effect (auth refetch, AppState churn) is harmless.
//   - Disconnect immediately on logout (auth flips to
//     unauthenticated) and on unmount.
//   - Disconnect after the app has been backgrounded for >30s — a
//     brief task-switch or notification-shade pull shouldn't tear
//     down the socket, but a genuinely-backgrounded app shouldn't
//     hold one open (battery, and iOS will suspend it anyway).
//     Returning to foreground before the timer fires cancels it;
//     returning after it fired re-connects (subject to auth).
//
// Matches WAKEUPEXPO §4.4 / Phase 7.2. Mounted once, app-wide, via
// `<WSLifecycle />` in the root layout — below the QueryClient
// provider so `useAuthState()` works.
import * as React from 'react';
import { AppState, type AppStateStatus } from 'react-native';

import { useAuthState } from '@/components/auth-gate';
import { connectWS, disconnectWS } from '@/lib/ws/client';

// How long the app may sit in the background before we drop the
// socket. Short enough that a suspended app isn't holding a dead
// connection; long enough to ride out a glance at another app.
const BACKGROUND_GRACE_MS = 30_000;

export function useWSLifecycle(): void {
  const { isAuthenticated } = useAuthState();

  // `authedRef` lets the AppState listener read the latest auth
  // value without re-subscribing every time it changes.
  const authedRef = React.useRef(isAuthenticated);
  authedRef.current = isAuthenticated;

  // Connect/disconnect on auth flips (and on mount, if already
  // foreground + authed). On logout we tear down right away rather
  // than waiting for a background.
  React.useEffect(() => {
    if (isAuthenticated && AppState.currentState === 'active') {
      connectWS();
    } else if (!isAuthenticated) {
      disconnectWS();
    }
  }, [isAuthenticated]);

  // Background-grace timer + foreground reconnect.
  React.useEffect(() => {
    let backgroundTimer: ReturnType<typeof setTimeout> | null = null;

    const clearBackgroundTimer = () => {
      if (backgroundTimer) {
        clearTimeout(backgroundTimer);
        backgroundTimer = null;
      }
    };

    const handleChange = (next: AppStateStatus) => {
      if (next === 'active') {
        clearBackgroundTimer();
        if (authedRef.current) connectWS();
        return;
      }
      // 'background' (iOS/Android) or 'inactive' (iOS app-switcher
      // peek / incoming call). Treat both as "not foreground" and
      // start the grace timer if one isn't already running.
      if (!backgroundTimer) {
        backgroundTimer = setTimeout(() => {
          backgroundTimer = null;
          disconnectWS();
        }, BACKGROUND_GRACE_MS);
      }
    };

    const sub = AppState.addEventListener('change', handleChange);
    return () => {
      sub.remove();
      clearBackgroundTimer();
      // Component teardown only happens when the root layout
      // unmounts (app shutdown) — close the socket so it can't
      // outlive the React tree.
      disconnectWS();
    };
  }, []);
}
