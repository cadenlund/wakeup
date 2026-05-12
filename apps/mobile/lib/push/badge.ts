// App-icon badge count (WAKEUPEXPO §7.5).
//
// The source of truth is the WS heartbeat ack's `unread_total` — the
// backend recomputes the caller's unread count server-side on every
// heartbeat, so the client just mirrors that number onto the launcher
// icon. <PushNotifications/> listens for `heartbeat` envelopes and
// calls `setAppBadgeCount`; the client pings the server on connect,
// periodically, and right after a MarkRead so the count stays fresh.
//
// No-op on web (no app-icon badge) and best-effort everywhere — a
// failed setBadgeCountAsync is cosmetic, never worth surfacing.
import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';

export async function setAppBadgeCount(count: number): Promise<void> {
  if (Platform.OS === 'web') return;
  const n = Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
  try {
    await Notifications.setBadgeCountAsync(n);
  } catch (err) {
    console.warn('[push] setBadgeCount failed', err);
  }
}
