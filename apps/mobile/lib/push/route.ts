// Maps an Expo push notification's `data` payload to the expo-router
// path the app should open when the user taps it. Mirrors the routing
// the in-app EventToast does — same destinations, different transport.
//
// The backend builds `data` in internal/service/{message,friend}: a
// message push carries `{ type: "message", conversation_id, message_id }`,
// a friend-request push `{ type: "friend_request", friendship_id }`.
// Unknown / malformed payloads return null → fall back to "just open the
// app on whatever the last screen was."

export function routeForNotificationData(data: unknown): string | null {
  if (!data || typeof data !== 'object') return null;
  const d = data as Record<string, unknown>;
  switch (d.type) {
    case 'message':
      return typeof d.conversation_id === 'string' && d.conversation_id
        ? `/conversations/${d.conversation_id}`
        : null;
    case 'friend_request':
      return '/friends';
    default:
      return null;
  }
}
