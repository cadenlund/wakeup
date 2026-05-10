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

import * as React from 'react';
// `react-dom` ships no .d.ts in our node_modules tree (we don't pull
// in `@types/react-dom`); the runtime export exists since react-dom
// is a direct dep. Suppress the implicit-any on the import — every
// other surface in this file is fully typed.
// @ts-expect-error — react-dom typings not installed; runtime safe.
import { createPortal } from 'react-dom';
import { Toaster } from 'sonner';

import { toast } from '@/lib/toast';
import { useThemeStore } from '@/lib/theme/store';

export function ToastRoot() {
  const mode = useThemeStore((s) => s.mode);
  // Drain any toast that was queued before a full-page navigation
  // (e.g. the "Password reset" success toast that fires right before
  // window.location.assign('/login')). Runs once per mount on web.
  React.useEffect(() => {
    toast.flushPending();
  }, []);
  // Mount via portal at document.body so the toaster escapes every
  // parent stacking context — without this the ThemeProvider's
  // `style={vars(...)}` wrapper (or any other ancestor that creates
  // a new context) caps sonner's z-index and our route-modal /
  // drawer overlays render on top of toasts. Returning null on the
  // SSR / pre-mount pass avoids hydration warnings.
  if (typeof document === 'undefined') return null;
  // `style` attaches to sonner's toaster section. Forcing the
  // max safe int as zIndex via inline style overrides the
  // sonner.css default (999_999_999) — RN-web Modal renders into
  // a portal whose children inherit position: fixed with a high
  // implicit z-index, which on certain stacking-context paths
  // ended up painting over the toast even though the static CSS
  // value was nominally higher. Inline style guarantees the
  // toast is drawn above any modal portal at body root.
  return createPortal(
    <Toaster
      theme={mode}
      position="top-right"
      richColors
      closeButton
      style={{ zIndex: 2147483647 }}
    />,
    document.body
  );
}
