// Singleton wrapper around `@react-native-community/netinfo` per
// spec §4.10. Exposes a Zustand store + `useNetworkState()` hook so
// any component can read `{ online, type }` and re-render only when
// those values change.
//
// The subscription is process-global — we attach exactly one NetInfo
// listener at module load — so we don't pay the addEventListener
// cost per component mount. Detach is unnecessary: NetInfo's listener
// lives for the app's lifetime anyway.
//
// `online` collapses NetInfo's `isConnected && isInternetReachable`
// into a single boolean. `isInternetReachable === null` (which NetInfo
// returns while it hasn't confirmed yet) is treated as online so the
// banner doesn't false-positive on cold start.
import NetInfo, { type NetInfoState } from '@react-native-community/netinfo';
import { useSyncExternalStore } from 'react';

type NetType = 'wifi' | 'cellular' | 'unknown';

type State = {
  online: boolean;
  type: NetType;
};

let current: State = { online: true, type: 'unknown' };
const listeners = new Set<() => void>();

function netInfoTypeToOurs(t: NetInfoState['type']): NetType {
  if (t === 'wifi') return 'wifi';
  if (t === 'cellular') return 'cellular';
  return 'unknown';
}

function fromNetInfo(s: NetInfoState): State {
  const reachable = s.isInternetReachable ?? true;
  return {
    online: Boolean(s.isConnected) && reachable,
    type: netInfoTypeToOurs(s.type),
  };
}

NetInfo.addEventListener((s) => {
  const next = fromNetInfo(s);
  if (next.online === current.online && next.type === current.type) return;
  current = next;
  listeners.forEach((l) => l());
});

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

function getSnapshot(): State {
  return current;
}

export function useNetworkState(): State {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function getNetworkState(): State {
  return current;
}
