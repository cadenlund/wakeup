// One place that knows which queries a change to the friend graph
// touches — so every surface that mutates relationships (the friend-
// action hooks, the search modal, the friends tab) and every WS event
// that signals one (`friend.request_*`) refreshes the SAME set.
// Relationship state surfaces in all of these:
//
//   - /v1/friends           — your accepted friends list
//   - /v1/friends/requests  — pending requests in & out
//   - /v1/search            — each search result carries its
//                             per-viewer relationship state
//   - /v1/presence/friends  — a new friend's presence row appears /
//                             a removed one disappears
//   - /v1/conversations     — you can only DM friends; unfriend /
//                             block deletes the DM
//
// Before this, the search modal and the friend-action hooks each
// invalidated a partial subset (notably never /v1/search), so tapping
// "Add friend" in search left the row stuck on "Add friend" until a
// manual refresh.
//
// Plain string-prefix keys (not the orval `getGetV1*QueryKey()` fns)
// so this module stays react-native-free — the WS dispatcher imports
// it, and the dispatcher's `bun test` suite can't pull in RN. The
// strings mirror `getGetV1*QueryKey()[0]` (the stable API path).
import type { QueryClient } from '@tanstack/react-query';

const RELATIONSHIP_KEYS = [
  '/v1/friends',
  '/v1/friends/requests',
  '/v1/search',
  '/v1/presence/friends',
  '/v1/conversations',
] as const;

export async function invalidateRelationships(qc: QueryClient): Promise<void> {
  await Promise.all(RELATIONSHIP_KEYS.map((key) => qc.invalidateQueries({ queryKey: [key] })));
}
