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
// CSS: sonner ships its styles in a separate file. We mirror it to
// `apps/mobile/sonner.css` (committed) and import it here so Metro's
// web pipeline bundles it without going through `global.css` —
// global.css is loaded on native too, and NativeWind's native CSS
// parser chokes on web-only properties (`aspect-ratio: 1 / 1`) that
// sonner's stylesheet uses. Native bundles never see this `.web.tsx`
// file, so the import is naturally web-only.
import '../sonner.css';

import { Toaster } from 'sonner';

import { useThemeStore } from '@/lib/theme/store';

export function ToastRoot() {
  const mode = useThemeStore((s) => s.mode);
  return <Toaster theme={mode} position="top-right" richColors closeButton />;
}
