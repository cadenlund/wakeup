// App-icon badge count seam (WAKEUPEXPO §7.5).
//
// Same RN-free-dispatcher pattern as lib/banner/store.ts: the
// dispatcher is kept off `react-native` so its `bun test` suite runs,
// so it can't call `expo-notifications` directly. On a WS `heartbeat`
// ack it writes the server's `unread_total` here; <PushNotifications/>
// reads it and mirrors it onto the launcher icon via `setAppBadgeCount`.
//
// `unreadTotal` is `null` until the first heartbeat of a session so the
// bridge doesn't repaint a previous account's stale count after a
// re-login before the new total has arrived. Logout resets it.
import { create } from 'zustand';

type BadgeState = {
  unreadTotal: number | null;
  setUnreadTotal: (n: number) => void;
  reset: () => void;
};

export const useBadgeStore = create<BadgeState>((set) => ({
  unreadTotal: null,
  setUnreadTotal: (n) => set({ unreadTotal: Number.isFinite(n) ? Math.max(0, Math.floor(n)) : 0 }),
  reset: () => set({ unreadTotal: null }),
}));

// Non-React entry point for the dispatcher.
export function setBadgeUnreadTotal(n: number): void {
  useBadgeStore.getState().setUnreadTotal(n);
}
