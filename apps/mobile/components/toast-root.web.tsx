// Web variant of <ToastRoot>. burnt routes web toasts through sonner,
// which needs a single <Toaster /> mounted in the React tree as the
// portal target. Metro's platform extension resolver picks this file
// over `toast-root.tsx` when bundling for web.
import { Toaster } from 'burnt/web';

export function ToastRoot() {
  return <Toaster />;
}
