// Phase 7.5 — bridges the dispatcher's heads-up event queue
// (`useBannerStore`, WAKEUPEXPO §4.13) onto the toast surface.
//
// The dispatcher (`lib/ws/dispatcher.ts`) can't call `toast.*`
// directly — that module pulls in `react-native` / `sonner`, and the
// dispatcher is deliberately RN-free so its `bun test` suite runs.
// So the dispatcher enqueues plain `BannerEvent` records and this
// root-mounted component, which CAN touch RN, drains them: each new
// head fires `toast.event(title, body, route)` (a tappable pill with
// a "View" action that navigates to the event) and is immediately
// dropped from the queue — the toast lib owns the display + 5s timer
// from there.
//
// Renders nothing. Replaces the old standalone `<EventBanner>` card
// so there's a single notification slot.
import * as React from 'react';

import { useBannerStore } from '@/lib/banner/store';
import { haptics } from '@/lib/haptics';
import { toast } from '@/lib/toast';

export function EventToastBridge(): null {
  const head = useBannerStore((s) => s.queue[0]);
  const dismissHead = useBannerStore((s) => s.dismissHead);

  React.useEffect(() => {
    if (!head) return;
    haptics.tap();
    toast.event(head.title, head.body, head.route);
    // Hand-off complete — the toast lib owns the lifecycle now.
    // Dropping the head re-runs this effect for the next queued event.
    dismissHead();
  }, [head, dismissHead]);

  return null;
}
