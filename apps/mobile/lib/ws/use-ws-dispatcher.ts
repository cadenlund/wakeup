// Phase 7.3 — bridges the WS client's inbound stream to the dispatcher.
//
// Subscribes to `onWSMessage` for the life of the app and feeds each
// envelope to `applyWSEvent` with the app's QueryClient + a small
// screen-derived context (the local user's id, so member-added
// banners can tell whether *you* were added). Mounted once, app-wide,
// via `<WSDispatcher />` in the root layout — below the QueryClient
// provider so `useQueryClient()` resolves.
//
// `myUserId` is read through a ref so the `onWSMessage` subscription
// isn't torn down and re-armed every time auth re-fetches.
import { useQueryClient } from '@tanstack/react-query';
import * as React from 'react';

import { useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { applyWSEvent } from '@/lib/ws/dispatcher';
import { onWSMessage } from '@/lib/ws/client';

export function useWSDispatcher(): void {
  const qc = useQueryClient();
  // Read the local user id off the cached /v1/auth/me query (the
  // auth gate already populates it) so member-added banners can tell
  // whether *you* were added.
  const meQ = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const myUserId = (meQ.data as { id?: string } | undefined)?.id;
  const myUserIdRef = React.useRef(myUserId);
  myUserIdRef.current = myUserId;

  React.useEffect(
    () => onWSMessage((env) => applyWSEvent(qc, env, { myUserId: myUserIdRef.current })),
    [qc]
  );
}
