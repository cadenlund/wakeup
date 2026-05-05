// Single helper around `expo-haptics` per spec §4.11. Components must
// never call `Haptics.*` directly — going through this module is what
// keeps the feedback shape consistent across send buttons, pull-to-
// refresh, friend acceptance, call decline, etc.
//
// Locked semantics:
//   - tap:     light impact, for user-initiated taps (send, long-press,
//              PiP toggle, refresh past threshold)
//   - success: notification-success, for completed flows (friend
//              accepted, conversation created, theme switched, bio unlock)
//   - warning: notification-warning, for soft failures (call declined,
//              optimistic message marked failed)
//
// On Android devices without a haptic engine these are no-ops, which
// is the spec'd behaviour — the call is fire-and-forget either way.
import * as Haptics from 'expo-haptics';

function tap() {
  void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
}

function success() {
  void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
}

function warning() {
  void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Warning);
}

export const haptics = { tap, success, warning };
