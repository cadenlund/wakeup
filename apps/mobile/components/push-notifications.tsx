// Phase 8.1 / 8.2 — mounts the push-notification lifecycle + handlers.
//
// Renders nothing; just runs `usePushNotifications()` for its effects.
// Placed in the root layout below the QueryClient provider (the hook
// reads `useAuthState()`), alongside <WSLifecycle /> / <WSDispatcher />.
import { usePushNotifications } from '@/lib/push/use-push-notifications';

export function PushNotifications(): null {
  usePushNotifications();
  return null;
}
