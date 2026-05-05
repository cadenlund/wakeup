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

export const toast = { error, success, info };
