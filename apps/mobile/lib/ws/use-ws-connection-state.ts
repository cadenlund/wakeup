// Phase 7.1 — React binding for the WS client's connection state.
//
// `'connected' | 'reconnecting' | 'disconnected'`. The conversation
// screen's reconnect banner (Phase 7.4) reads this; for now it's
// the public surface that proves the client's state machine works.
//
// useSyncExternalStore is the canonical subscription primitive for
// an external mutable store — it handles tearing + StrictMode
// double-invoke correctly. The snapshot is a string primitive so
// reference identity isn't a concern.
import * as React from 'react';

import { getWSState, onWSStateChange, type WSConnectionState } from '@/lib/ws/client';

export function useWSConnectionState(): WSConnectionState {
  return React.useSyncExternalStore(onWSStateChange, getWSState, getWSState);
}
