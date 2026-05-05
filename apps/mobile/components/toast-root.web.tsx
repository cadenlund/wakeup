// Web variant of <ToastRoot>. Uses sonner directly (top-right pill,
// theme-aware) instead of react-native-toast-message — the native
// lib only supports top-centred / bottom-centred positioning, no
// horizontal control. sonner natively supports `position="top-right"`
// which is the standard web toast placement.
//
// `theme` flips with the active light/dark mode so the chrome
// matches the rest of the app. Per-scheme accent colours aren't
// surfaced — sonner's theme prop only takes the broad mode.
//
// sonner v2 ships a separate styles.css that has to load on web —
// the Toaster renders invisibly without it. We pull it in via an
// `@import` at the top of `apps/mobile/global.css` (which Metro
// already processes for web through NativeWind's pipeline) rather
// than importing here, because Expo Web's Metro doesn't reliably
// resolve bare CSS imports from .tsx files.
import { Toaster } from 'sonner';

import { useThemeStore } from '@/lib/theme/store';

export function ToastRoot() {
  const mode = useThemeStore((s) => s.mode);
  return <Toaster theme={mode} position="top-right" richColors closeButton />;
}
