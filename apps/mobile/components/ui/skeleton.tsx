// Pulsing placeholder for loading content. Used wherever data is in
// flight — conversations list rows, friend rows, message bubbles —
// so cold-start screens don't show layout shift when results arrive.
//
// NW v4's native bundle doesn't ship the Tailwind keyframe utilities
// (`animate-pulse`), so we drive the opacity pulse with Reanimated.
// Web gets the same Animated.View — Reanimated v4 supports web — so
// the animation matches across platforms without a Platform.select.
import * as React from 'react';
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from 'react-native-reanimated';

import { cn } from '@/lib/utils';

function Skeleton({ className, style, ...props }: React.ComponentProps<typeof Animated.View>) {
  const opacity = useSharedValue(0.5);

  React.useEffect(() => {
    opacity.value = withRepeat(withTiming(1, { duration: 800 }), -1, true);
  }, [opacity]);

  const animatedStyle = useAnimatedStyle(() => ({ opacity: opacity.value }));

  return (
    <Animated.View
      className={cn('rounded-md bg-muted', className)}
      style={[animatedStyle, style]}
      {...props}
    />
  );
}

export { Skeleton };
