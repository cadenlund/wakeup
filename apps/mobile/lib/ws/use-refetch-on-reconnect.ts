// Phase 7.4 — refetch the open thread's messages after a WS reconnect.
//
// Per WAKEUPEXPO §4.4: on every successful (re)connect, re-fetch the
// visible conversation's messages so any frames that arrived while
// the socket was down (and weren't replayed) get filled in. Marking
// the query stale is enough — the mounted `useInfiniteMessages`
// observer refetches the pages on its own.
//
// Only fires on a transition *into* `connected` from a prior
// non-connected state; the first render (no prior state) is a no-op
// since the query is already fetching its initial page.
import { useQueryClient } from '@tanstack/react-query';
import * as React from 'react';

import { useWSConnectionState } from '@/lib/ws/use-ws-connection-state';

export function useRefetchMessagesOnReconnect(conversationId: string): void {
  const qc = useQueryClient();
  const state = useWSConnectionState();
  const prev = React.useRef<typeof state | undefined>(undefined);

  React.useEffect(() => {
    const wasConnected = prev.current === 'connected' || prev.current === undefined;
    prev.current = state;
    if (state === 'connected' && !wasConnected && conversationId) {
      void qc.invalidateQueries({ queryKey: [`/v1/conversations/${conversationId}/messages`] });
    }
  }, [state, conversationId, qc]);
}
