// Phase 7.4 — "Reconnecting…" banner for the conversation screen.
//
// Per WAKEUPEXPO §6.3: when the WS connection state is not
// `connected` for more than 2s, the open thread shows a thin
// "Reconnecting…" strip; when it recovers, a one-time "Reconnected"
// toast fires. A blip shorter than 2s shows nothing and fires no
// toast — short reconnects are noise, not news.
//
// When the device is offline the root <NetworkBanner> already says
// so ("You're offline — …"); a redundant "Reconnecting…" strip under
// it is noise, so this one stays hidden until the device is back
// online (the WS state machine keeps running underneath, so the
// "Reconnected" toast still fires on recovery).
//
// Mounted inside the conversation thread (not at root) — it's the
// only screen the spec calls for this surface, and a global strip
// would shove every other screen down on every Wi-Fi hiccup.
import * as React from 'react';
import { ActivityIndicator, View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useNetworkState } from '@/lib/network/state';
import { toast } from '@/lib/toast';
import { useWSConnectionState } from '@/lib/ws/use-ws-connection-state';

// How long the connection must stay down before the strip appears.
const BANNER_DELAY_MS = 2_000;

export function WSReconnectBanner(): React.ReactElement | null {
  const state = useWSConnectionState();
  const { online } = useNetworkState();
  const [visible, setVisible] = React.useState(false);
  // Read the live `visible` from the state-change effect without
  // re-arming it on every toggle.
  const visibleRef = React.useRef(false);
  visibleRef.current = visible;

  React.useEffect(() => {
    if (state === 'connected') {
      // Recovered. If the strip was actually showing (a >2s
      // outage), tell the user it's back; otherwise stay quiet.
      if (visibleRef.current) {
        setVisible(false);
        toast.success('Reconnected');
      }
      return;
    }
    // Not connected — arm the 2s timer. Reconnecting before it
    // fires clears it (effect cleanup) so a brief blip is silent.
    const timer = setTimeout(() => setVisible(true), BANNER_DELAY_MS);
    return () => clearTimeout(timer);
  }, [state]);

  // Offline → the root <NetworkBanner> owns the message; stay quiet.
  if (!visible || !online) return null;

  return (
    <View className="flex-row items-center justify-center gap-2 bg-muted px-4 py-1.5">
      <ActivityIndicator size="small" />
      <Text className="text-xs font-medium text-muted-foreground">Reconnecting…</Text>
    </View>
  );
}
