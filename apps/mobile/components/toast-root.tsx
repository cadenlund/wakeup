// Mount point for `react-native-toast-message` at the app root.
// Visual config lives in `toast-config.tsx` so it can be reused
// by per-screen `<Toast>` instances mounted inside iOS native
// modals (search, conversations/new) — without those, the
// system Modal renders above the React tree and the root toast
// gets covered.
import { Toast, toastConfig, TOAST_TOP_OFFSET } from '@/components/toast-config';

export function ToastRoot() {
  return <Toast config={toastConfig} topOffset={TOAST_TOP_OFFSET} />;
}
