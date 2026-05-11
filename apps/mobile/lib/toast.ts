// Cross-platform toast helper. Preset shapes per spec §4.6 —
// `error`, `success`, `info` — plus `event`: a heads-up about
// something that happened elsewhere (a message in a thread you're
// not on, a friend request, getting added to a group) with a
// tap/action that routes to it. The §4.13 in-app banner used to be
// its own surface; it's folded into the toast so there's a single
// notification slot. Each platform uses the most native-feeling
// surface:
//
//   - Native (iOS, Android): `react-native-toast-message` with our
//     themed render (see `<ToastRoot>`). Top-centred pill; the
//     whole `event` pill is tappable.
//   - Web: `sonner` directly. Top-right pill, theme-aware via the
//     <Toaster theme="..." /> mounted in `toast-root.web.tsx`;
//     `event` toasts carry a "View" action button.
//
// Both surfaces follow the active light/dark mode and show title +
// description. Visibility durations match across platforms.
//
// Rule of when to fire (per §4.6 / §4.13) lives in the call sites —
// the API client always toasts errors, mutations toast success only
// on the explicit allowlist, and ambient WS events toast info /
// event (the dispatcher in `lib/ws/dispatcher.ts` owns that
// decision; `<EventToastBridge>` drains its queue into `toast.event`).
import { Platform } from 'react-native';
import { router } from 'expo-router';
import RNToast from 'react-native-toast-message';
import { toast as sonnerToast } from 'sonner';

type Variant = 'error' | 'success' | 'info' | 'event';

const ERROR_VISIBILITY_MS = 4000;
const NORMAL_VISIBILITY_MS = 2500;
// Event toasts are actionable, so they linger a little longer than a
// plain info toast — long enough to read + reach for the action.
const EVENT_VISIBILITY_MS = 5000;

function navigate(route: string) {
  router.push(route as Parameters<typeof router.push>[0]);
}

function showWeb(
  variant: Variant,
  title: string,
  message: string | undefined,
  duration: number,
  route?: string
) {
  const opts = {
    description: message,
    duration,
    action: route ? { label: 'View', onClick: () => navigate(route) } : undefined,
  };
  if (variant === 'error') sonnerToast.error(title, opts);
  else if (variant === 'success') sonnerToast.success(title, opts);
  else sonnerToast(title, opts);
}

function showNative(
  variant: Variant,
  title: string,
  message: string | undefined,
  duration: number,
  route?: string
) {
  RNToast.show({
    type: variant,
    text1: title,
    text2: message,
    visibilityTime: duration,
    onPress: route
      ? () => {
          RNToast.hide();
          navigate(route);
        }
      : undefined,
  });
}

function fire(
  variant: Variant,
  title: string,
  message: string | undefined,
  duration: number,
  route?: string
) {
  if (Platform.OS === 'web') showWeb(variant, title, message, duration, route);
  else showNative(variant, title, message, duration, route);
}

function error(title: string, message?: string) {
  fire('error', title, message, ERROR_VISIBILITY_MS);
}

function success(title: string, message?: string) {
  fire('success', title, message, NORMAL_VISIBILITY_MS);
}

function info(title: string, message?: string) {
  fire('info', title, message, NORMAL_VISIBILITY_MS);
}

// Heads-up about something elsewhere. `route` (an expo-router path)
// makes the toast tappable / adds a "View" action that navigates
// there; omit it for a non-actionable notice.
function event(title: string, message?: string, route?: string) {
  fire('event', title, message, EVENT_VISIBILITY_MS, route);
}

// Cross-navigation toast: stash a single toast in sessionStorage so it
// survives a full page reload (e.g. after `window.location.assign` on
// the post-reset → /login flow). The receiving page calls `flushPending`
// on mount to display + clear it. Web-only; native has no equivalent
// because we never hard-navigate there.
const PENDING_KEY = 'wakeup:toast:pending';

type PendingToast = {
  variant: 'error' | 'success' | 'info';
  title: string;
  message?: string;
};

function queueForNextMount(variant: PendingToast['variant'], title: string, message?: string) {
  if (typeof window === 'undefined' || !window.sessionStorage) return;
  try {
    window.sessionStorage.setItem(
      PENDING_KEY,
      JSON.stringify({ variant, title, message } satisfies PendingToast)
    );
  } catch {
    // sessionStorage can throw in privacy modes / over-quota — non-
    // critical, the user just won't see a delayed toast.
  }
}

function flushPending() {
  if (typeof window === 'undefined' || !window.sessionStorage) return;
  let raw: string | null = null;
  try {
    raw = window.sessionStorage.getItem(PENDING_KEY);
    if (raw) window.sessionStorage.removeItem(PENDING_KEY);
  } catch {
    return;
  }
  if (!raw) return;
  try {
    const p = JSON.parse(raw) as PendingToast;
    fire(
      p.variant,
      p.title,
      p.message,
      p.variant === 'error' ? ERROR_VISIBILITY_MS : NORMAL_VISIBILITY_MS
    );
  } catch {
    // malformed payload — drop silently.
  }
}

export const toast = { error, success, info, event, queueForNextMount, flushPending };
