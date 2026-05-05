// Single helper around `burnt`. Three preset shapes per spec §4.6 —
// `error`, `success`, `info` — with consistent durations so toasts
// don't flicker through at unpredictable speeds.
//
// burnt.alert renders App-Store-like banners on iOS, falls back to
// ToastAndroid on Android, and (with a `<Toaster />` at root) maps to
// sonner toasts on web.
//
// Rule of when to fire (per §4.6) lives in the call sites — the
// API client always toasts errors, mutations toast success only on
// the explicit allowlist, and ambient WS/version events toast info.
import * as Burnt from 'burnt';

const ERROR_DURATION = 4;
const NORMAL_DURATION = 2;

function error(title: string, message?: string) {
  Burnt.alert({ preset: 'error', title, message, duration: ERROR_DURATION });
}

function success(title: string, message?: string) {
  Burnt.alert({ preset: 'done', title, message, duration: NORMAL_DURATION });
}

function info(title: string, message?: string) {
  Burnt.alert({ preset: 'none', title, message, duration: NORMAL_DURATION });
}

export const toast = { error, success, info };
