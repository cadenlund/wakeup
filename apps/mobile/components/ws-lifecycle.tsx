// Phase 7.2 — mounts the WebSocket connection-lifecycle policy.
//
// Renders nothing; just runs `useWSLifecycle()` for its effects.
// Placed in the root layout below the QueryClient provider (the
// hook reads `useAuthState()`).
import { useWSLifecycle } from '@/lib/ws/use-ws-lifecycle';

export function WSLifecycle(): null {
  useWSLifecycle();
  return null;
}
