// Phase 7.5 — the local user's sticky presence intent, for banner
// suppression.
//
// §4.13: the WS dispatcher must NOT enqueue an event banner while the
// user's presence intent is `dnd` (same gate as pushes). The intent
// is set via `POST /v1/presence/status` from the `<PresencePicker>`
// (a later phase) — that phase will call `setPresenceIntent(...)`
// when it writes through. Until then this stays at the default
// (`online`), so the dnd guard is structurally present but inert.
//
// Module-level (not React state) so the dispatcher — a plain module
// — can read it without hooks; mirrors `active-conversation.ts`.

export type PresenceIntent = 'online' | 'away' | 'sleeping' | 'dnd';

let intent: PresenceIntent = 'online';

export function setPresenceIntent(next: PresenceIntent): void {
  intent = next;
}

export function getPresenceIntent(): PresenceIntent {
  return intent;
}
