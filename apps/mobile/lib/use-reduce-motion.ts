// `useReduceMotion()` — true when the OS "reduce motion" accessibility
// setting is on, kept in sync via the `reduceMotionChanged` event.
//
// Used to skip / shorten non-essential animations (bubble pop-ins,
// typing-indicator collapse, layout transitions) per WAKEUPEXPO §10.4.
import * as React from 'react';
import { AccessibilityInfo } from 'react-native';

export function useReduceMotion(): boolean {
  const [reduce, setReduce] = React.useState(false);
  React.useEffect(() => {
    let mounted = true;
    void AccessibilityInfo.isReduceMotionEnabled().then((v) => {
      if (mounted) setReduce(v);
    });
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduce);
    return () => {
      mounted = false;
      sub.remove();
    };
  }, []);
  return reduce;
}
