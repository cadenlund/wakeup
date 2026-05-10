// Shared friend-graph mutations: send + cancel friend request.
// One hook so both the global search modal and the friends tab
// share the same toast vocabulary, the same cache invalidations,
// and the same in-flight pending state. Without this, the two
// surfaces drifted (search.tsx had silent success, friends.tsx
// had different toast wording).
//
// `isAddingFor(username)` and `isCancelingFor(requestId)` let
// callers disable the right pill while a mutation is in flight,
// scoped to the specific row — pressing Add on row A shouldn't
// disable Add on row B.
import { useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetV1FriendsQueryKey,
  getGetV1FriendsRequestsQueryKey,
  useDeleteV1FriendsRequestsId,
  usePostV1FriendsRequests,
} from '@/lib/api/hooks/friends/friends';
import { toast } from '@/lib/toast';

export function useFriendActions() {
  const qc = useQueryClient();

  const send = usePostV1FriendsRequests({
    mutation: {
      onSuccess: (_data, vars) => {
        const username = vars?.data?.username;
        toast.success(username ? `Friend request sent to @${username}` : 'Friend request sent');
      },
      onSettled: () => {
        // Invalidate both lists — outgoing requests appear in
        // /v1/friends/requests; the user's friend status flip
        // also surfaces in the global friends list cache as a
        // pending row gets created.
        void qc.invalidateQueries({ queryKey: getGetV1FriendsRequestsQueryKey() });
        void qc.invalidateQueries({ queryKey: getGetV1FriendsQueryKey() });
      },
    },
  });

  const cancel = useDeleteV1FriendsRequestsId({
    mutation: {
      onSuccess: () => {
        toast.success('Friend request unsent');
      },
      onSettled: () => {
        void qc.invalidateQueries({ queryKey: getGetV1FriendsRequestsQueryKey() });
      },
    },
  });

  const sendFriendRequest = useCallback(
    (username: string) => {
      send.mutate({ data: { username } });
    },
    [send]
  );

  const cancelFriendRequest = useCallback(
    (requestId: string) => {
      cancel.mutate({ id: requestId });
    },
    [cancel]
  );

  // Per-row pending checks. We compare against the in-flight
  // mutation variables so the spinner only spins on the row whose
  // button was tapped, not on every "Add friend" pill on the
  // screen.
  const isAddingFor = useCallback(
    (username: string | undefined) =>
      send.isPending && username !== undefined && send.variables?.data?.username === username,
    [send.isPending, send.variables]
  );
  const isCancelingFor = useCallback(
    (requestId: string | undefined) =>
      cancel.isPending && requestId !== undefined && cancel.variables?.id === requestId,
    [cancel.isPending, cancel.variables]
  );

  return { sendFriendRequest, cancelFriendRequest, isAddingFor, isCancelingFor };
}
