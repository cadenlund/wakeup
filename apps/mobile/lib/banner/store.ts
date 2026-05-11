// Phase 7.5 — in-app event-banner queue (WAKEUPEXPO §4.13).
//
// A tiny FIFO of "heads-up" events the dispatcher decides to surface
// while the app is foregrounded: a message in a conversation you're
// not looking at, a friend request, getting added to a group. The
// `<EventBanner>` component renders the HEAD of the queue as a card
// that drops in from the top; when it's dismissed (tap-to-route,
// swipe-up, or the 4s timer) `dismissHead()` advances to the next.
//
// The dispatcher (`lib/ws/dispatcher.ts`) owns the enqueue/skip
// decision — this store never filters. Dedup is by `id` only: an
// event whose id is already queued is dropped (the dispatcher mints
// stable ids so a duplicate WS delivery doesn't double-banner).
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
