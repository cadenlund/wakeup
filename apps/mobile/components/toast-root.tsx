// Native default for <ToastRoot>. burnt's iOS/Android paths use
// system-level alert APIs (UIAlertController on iOS, ToastAndroid
// on Android), so no React-tree mount point is required — this
// component is a no-op on native and only the `.web.tsx` sibling
// renders a real toaster (sonner).
export function ToastRoot() {
  return null;
}
