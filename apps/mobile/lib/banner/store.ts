// Phase 7.5 — in-app event hand-off queue (WAKEUPEXPO §4.13).
//
// A tiny FIFO of "heads-up" events the dispatcher decides to surface
// while the app is foregrounded: a message in a conversation you're
// not looking at, a friend request, getting added to a group. The
// `<EventToastBridge>` component drains the HEAD of the queue into a
// `toast.event(...)` pill (the §4.13 banner is folded into the toast
// surface) and immediately drops it; the toast lib owns the display.
//
// This store exists purely as the RN-free seam: the dispatcher is
// kept off `react-native` so its `bun test` suite runs, so it can't
// touch `lib/toast` directly — it enqueues here and the bridge does
// the rest. The dispatcher owns the enqueue/skip decision; this
// store never filters. Dedup is by `id` only: an event whose id is
// already queued is dropped (the dispatcher mints stable ids so a
// duplicate WS delivery doesn't double-notify).
import { create } from 'zustand';

export type BannerEvent = {
  // Stable id — used for dedup and as the React key. For message
  // banners this is the message id; for friend events the request
  // id; for member-added the conversation id.
  id: string;
  title: string;
  body?: string;
  // expo-router path to push when the banner is tapped.
  route: string;
  // For the toast's avatar: the person this is about (the message
  // sender) — `senderName` is the avatar's initials fallback. Set
  // for `message.new`; absent for friend / member-added events.
  avatarUrl?: string | null;
  senderName?: string;
};

type BannerState = {
  queue: BannerEvent[];
  enqueue: (event: BannerEvent) => void;
  dismissHead: () => void;
};

export const useBannerStore = create<BannerState>((set) => ({
  queue: [],
  enqueue: (event) =>
    set((s) => (s.queue.some((q) => q.id === event.id) ? s : { queue: [...s.queue, event] })),
  dismissHead: () => set((s) => ({ queue: s.queue.slice(1) })),
}));

// Non-React entry point for the dispatcher. Mirrors `useBannerStore`'s
// `enqueue` but callable from a plain module.
export function enqueueBanner(event: BannerEvent): void {
  useBannerStore.getState().enqueue(event);
}
