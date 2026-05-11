// Phase 6.4 — who's typing, per conversation (WAKEUPEXPO §6.2).
//
// Not Query Cache — typing is ephemeral chatter, not a server fact
// to refetch. The WS dispatcher feeds `typing.start` / `typing.stop`
// in here (after dropping the local user's own echo); the
// `<TypingIndicator>` reads `typing[conversationId]`.
//
// Self-healing: every `markTyping` (re)arms a TTL timer that clears
// that user, so a dropped `typing.stop` (sender crashed, socket
// blip) can't leave a "typing…" stuck forever. The peer re-sends
// `typing.start` on each keystroke, so an active typist keeps the
// timer alive.
import * as React from 'react';
import { create } from 'zustand';

// How long a "typing" entry lives without a refresh. Slightly longer
// than the composer's 3s idle-stop so a still-typing peer's keystroke
// re-arms it before it lapses.
const TYPING_TTL_MS = 5_000;

// Per-(conversation,user) auto-clear timers — module-level, not part
// of the store's React state.
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const timerKey = (conversationId: string, userId: string) => `${conversationId}:${userId}`;

type TypingState = {
  // conversationId -> { userId -> true }. An empty inner object means
  // nobody's typing there; `<TypingIndicator>` derives the id list.
  typing: Record<string, Record<string, true>>;
  markTyping: (conversationId: string, userId: string) => void;
  clearTyping: (conversationId: string, userId: string) => void;
};

export const useTypingStore = create<TypingState>((set, get) => ({
  typing: {},
  markTyping: (conversationId, userId) => {
    const key = timerKey(conversationId, userId);
    const existing = timers.get(key);
    if (existing) clearTimeout(existing);
    timers.set(
      key,
      setTimeout(() => get().clearTyping(conversationId, userId), TYPING_TTL_MS)
    );
    set((s) => {
      const conv = s.typing[conversationId];
      if (conv?.[userId]) return s;
      return { typing: { ...s.typing, [conversationId]: { ...conv, [userId]: true } } };
    });
  },
  clearTyping: (conversationId, userId) => {
    const key = timerKey(conversationId, userId);
    const t = timers.get(key);
    if (t) {
      clearTimeout(t);
      timers.delete(key);
    }
    set((s) => {
      const conv = s.typing[conversationId];
      if (!conv || !(userId in conv)) return s;
      const next = { ...conv };
      delete next[userId];
      return { typing: { ...s.typing, [conversationId]: next } };
    });
  },
}));

// Non-React entry points for the WS dispatcher (a plain module).
export function markTyping(conversationId: string, userId: string): void {
  useTypingStore.getState().markTyping(conversationId, userId);
}
export function clearTyping(conversationId: string, userId: string): void {
  useTypingStore.getState().clearTyping(conversationId, userId);
}

// Test helper: clear all pending TTL timers and empty the store, so
// one test's `markTyping` can't leave a timer (or entry) alive into
// the next.
export function resetTypingStore(): void {
  for (const t of timers.values()) clearTimeout(t);
  timers.clear();
  useTypingStore.setState({ typing: {} });
}

// Selector hook: the user ids currently typing in `conversationId`.
// Reads the (ref-stable) per-conversation object from the store and
// derives the id list with `useMemo`, so it stays stable while that
// object is unchanged.
export function useTypingUserIds(conversationId: string): string[] {
  const conv = useTypingStore((s) => s.typing[conversationId]);
  return React.useMemo(() => (conv ? Object.keys(conv) : []), [conv]);
}
