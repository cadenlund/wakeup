// Phase 7.3 — bridges the WS client's inbound stream to the dispatcher.
//
// Subscribes to `onWSMessage` for the life of the app and feeds each
// envelope to `applyWSEvent` with the app's QueryClient. Mounted once,
// app-wide, via `<WSDispatcher />` in the root layout — below the
// QueryClient provider so `useQueryClient()` resolves.
import { useQueryClient } from '@tanstack/react-query';
import * as React from 'react';

import { applyWSEvent } from '@/lib/ws/dispatcher';
import { onWSMessage } from '@/lib/ws/client';

export function useWSDispatcher(): void {
  const qc = useQueryClient();
  React.useEffect(() => onWSMessage((env) => applyWSEvent(qc, env)), [qc]);
}
