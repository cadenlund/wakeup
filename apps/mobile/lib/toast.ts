// Cross-platform toast helper. Three preset shapes per spec §4.6 —
// `error`, `success`, `info`. Each platform uses the most native-
// feeling toast surface:
//
//   - Native (iOS, Android): `react-native-toast-message` with our
//     themed render (see `<ToastRoot>`). Top-centred pill.
//   - Web: `sonner` directly. Top-right pill, theme-aware via the
//     <Toaster theme="..." /> mounted in `toast-root.web.tsx`.
//
// Both surfaces follow the active light/dark mode and show title +
// description. Visibility durations match across platforms.
//
// Rule of when to fire (per §4.6) lives in the call sites — the
// API client always toasts errors, mutations toast success only on
// the explicit allowlist, and ambient WS/version events toast info.
import { Platform } from 'react-native';
import RNToast from 'react-native-toast-message';
import { toast as sonnerToast } from 'sonner';

const ERROR_VISIBILITY_MS = 4000;
const NORMAL_VISIBILITY_MS = 2500;

function showWeb(
  variant: 'error' | 'success' | 'info',
  title: string,
  message: string | undefined,
  duration: number
) {
  const opts = { description: message, duration };
  if (variant === 'error') sonnerToast.error(title, opts);
  else if (variant === 'success') sonnerToast.success(title, opts);
  else sonnerToast(title, opts);
}

function showNative(
  variant: 'error' | 'success' | 'info',
  title: string,
  message: string | undefined,
  duration: number
) {
  RNToast.show({
    type: variant,
    text1: title,
    text2: message,
    visibilityTime: duration,
  });
}

function fire(
  variant: 'error' | 'success' | 'info',
  title: string,
  message: string | undefined,
  duration: number
) {
  if (Platform.OS === 'web') showWeb(variant, title, message, duration);
  else showNative(variant, title, message, duration);
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

export const toast = { error, success, info, queueForNextMount, flushPending };
