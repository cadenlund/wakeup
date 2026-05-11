// Phase 7.4 — refetch the open thread after a WS reconnect.
//
// Per WAKEUPEXPO §4.4: on every successful (re)connect, re-fetch the
// visible conversation's messages so any frames that arrived while
// the socket was down (and weren't replayed) get filled in. We also
// re-fetch the conversation detail — it carries every member's
// `last_read_message_id`, and a `message.read` frame missed during
// the outage would otherwise leave the §6.3 receipt captions stale
// until the detail query's own staleTime elapsed. Marking the
// queries stale is enough — the mounted observers refetch on their
// own.
//
// Only fires on a transition *into* `connected` from a prior
// non-connected state; the first render (no prior state) is a no-op
// since the queries are already fetching their initial data.
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
      void qc.invalidateQueries({ queryKey: [`/v1/conversations/${conversationId}`] });
    }
  }, [state, conversationId, qc]);
}
