// Phase 8.1 / 8.2 — push notification lifecycle + handlers.
//
// Mounted once app-wide via <PushNotifications/> in the root layout
// (below the QueryClient provider so it can read `useAuthState()`).
// Responsibilities:
//
//   - Foreground suppression: while the app is active the OS banner is
//     redundant — the in-app EventToast / unread badge already surface
//     the event — so `setNotificationHandler` returns "show nothing"
//     when AppState is 'active'. When the app is backgrounded the
//     handler isn't consulted at all; the OS renders the banner itself.
//   - Token registration: on auth (and on mount, if already authed)
//     fire `registerForPushNotificationsAsync()` — fire-and-forget, it
//     handles permissions / simulators / failures internally.
//   - Tap routing: `addNotificationResponseReceivedListener` →
//     `routeForNotificationData` → `router.push`. Also handles the
//     cold-start case (`getLastNotificationResponseAsync`) where the
//     app was launched *from* a notification.
//   - App-icon badge: mirrors the WS `heartbeat` ack's `unread_total`
//     onto the launcher icon (§7.5); clears it on logout.
//
// Logout deregistration is NOT here — the DELETE needs a live session
// cookie, so it runs from the logout mutation's onMutate
// (header-logout-pill). This hook only handles the local side.
import { router } from 'expo-router';
import * as Notifications from 'expo-notifications';
import * as React from 'react';
import { AppState } from 'react-native';

import { useAuthState } from '@/components/auth-gate';
import { useBadgeStore } from '@/lib/badge/store';
import { setAppBadgeCount } from '@/lib/push/badge';
import {
  registerForPushNotificationsAsync,
  setupAndroidNotificationChannelAsync,
} from '@/lib/push/register';
import { routeForNotificationData } from '@/lib/push/route';

// Set once at module load — independent of auth / mount timing.
// handleNotification is only invoked for notifications that arrive
// while the app is foregrounded — which normally never happens (a
// foregrounded app is WS-connected, so the backend sends the WS event
// instead of a push). It can fire on an offline→online race: we
// suppress the intrusive heads-up banner (an in-app EventToast /
// RoomBanner / unread badge covers it) but still let it land in the
// notification shade, so a message the in-app dispatcher didn't replay
// on reconnect isn't silently dropped.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: false,
    shouldShowList: true,
    shouldPlaySound: false,
    shouldSetBadge: false,
  }),
});

function openFromNotification(response: Notifications.NotificationResponse | null): void {
  if (!response) return;
  const route = routeForNotificationData(response.notification.request.content.data);
  if (route) router.push(route as Parameters<typeof router.push>[0]);
}

export function usePushNotifications(): void {
  const { isAuthenticated } = useAuthState();
  // Lets the AppState listener read the latest auth value without
  // re-subscribing every time it changes.
  const authedRef = React.useRef(isAuthenticated);
  authedRef.current = isAuthenticated;

  // Android notification channel — once per process.
  React.useEffect(() => {
    void setupAndroidNotificationChannelAsync();
  }, []);

  // Register the token when authenticated. `registerForPush…` is
  // idempotent (skips the POST when the cached token is unchanged), so
  // re-running on auth refetch / remount is harmless. On logout, clear
  // the app-icon badge (no session → no unread count to show) and the
  // badge store, so a re-login doesn't briefly paint the prior
  // account's count before the first heartbeat lands.
  React.useEffect(() => {
    if (isAuthenticated) {
      void registerForPushNotificationsAsync();
    } else {
      useBadgeStore.getState().reset();
      void setAppBadgeCount(0);
    }
  }, [isAuthenticated]);

  // Mirror the badge store onto the app-icon badge. The dispatcher
  // writes the store from each WS `heartbeat` ack (the client pings on
  // connect / every interval / after a MarkRead, so it stays fresh);
  // `null` means "no heartbeat yet this session" → leave the badge be.
  const unreadTotal = useBadgeStore((s) => s.unreadTotal);
  React.useEffect(() => {
    if (isAuthenticated && unreadTotal !== null) void setAppBadgeCount(unreadTotal);
  }, [isAuthenticated, unreadTotal]);

  // Re-check on foreground (a user may grant permission in Settings
  // while the app is backgrounded, or the OS may rotate the token).
  React.useEffect(() => {
    const sub = AppState.addEventListener('change', (next) => {
      if (next === 'active' && authedRef.current) {
        void registerForPushNotificationsAsync();
      }
    });
    return () => sub.remove();
  }, []);

  // Tap handling: while running, plus the cold-start launch case.
  React.useEffect(() => {
    let cancelled = false;
    Notifications.getLastNotificationResponseAsync()
      .then((response) => {
        if (!cancelled) openFromNotification(response);
      })
      .catch(() => {
        // no last response (normal launch) — nothing to do.
      });
    const sub = Notifications.addNotificationResponseReceivedListener(openFromNotification);
    return () => {
      cancelled = true;
      sub.remove();
    };
  }, []);
}
