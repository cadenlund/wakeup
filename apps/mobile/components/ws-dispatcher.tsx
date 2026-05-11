// Phase 7.3 — mounts the WS-event → React Query cache dispatcher.
//
// Renders nothing; just runs `useWSDispatcher()` for its effect.
// Placed in the root layout below the QueryClient provider.
import { useWSDispatcher } from '@/lib/ws/use-ws-dispatcher';

export function WSDispatcher(): null {
  useWSDispatcher();
  return null;
}
