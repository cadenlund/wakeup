// Phase 7.5 — which conversation thread is on screen right now.
//
// The WS dispatcher suppresses a `message.new` event banner when the
// user is already looking at that conversation (WAKEUPEXPO §4.13).
// The dispatcher is a plain module (no React, no router hooks), so
// the thread screen pushes its id here on focus and clears it on
// blur via `useFocusEffect`.
//
// `null` means "not on any conversation screen" — the chats list, a
// settings screen, etc.
let activeConversationId: string | null = null;

export function setActiveConversation(id: string | null): void {
  activeConversationId = id;
}

export function getActiveConversation(): string | null {
  return activeConversationId;
}
