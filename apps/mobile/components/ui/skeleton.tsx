// Pulsing placeholder for loading content. Used wherever data is in
// flight — conversations list rows, friend rows, message bubbles —
// so cold-start screens don't show layout shift when results arrive.
//
// Uses React Native's core `Animated` API rather than Reanimated.
// The pulse is an opacity loop — no gesture-driven worklet need —
// and Reanimated's v4 strict mode flags `useAnimatedStyle` reads
// of `.value` / `.get()` on web because RNW runs worklets on the
// JS thread. Core Animated runs on the native driver where
// available and falls back to JS otherwise, with no warning noise.
import * as React from 'react';
import { Animated, Easing, Platform } from 'react-native';
import { cssInterop } from 'nativewind';

import { cn } from '@/lib/utils';

// NW v4 doesn't auto-tag Animated.View, so register the interop once
// at module load. This lets `<Animated.View className="…" />` flow
// Tailwind classes into the underlying `style` prop just like a
// regular `<View>`.
cssInterop(Animated.View, { className: 'style' });

function Skeleton({ className, style, ...props }: React.ComponentProps<typeof Animated.View>) {
  const opacity = React.useRef(new Animated.Value(0.5)).current;

  React.useEffect(() => {
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(opacity, {
          toValue: 1,
          duration: 800,
          easing: Easing.inOut(Easing.ease),
          // Native driver lets the animation skip the JS bridge each
          // tick on iOS/Android. Web doesn't have a native driver
          // so RN falls back automatically.
          useNativeDriver: Platform.OS !== 'web',
        }),
        Animated.timing(opacity, {
          toValue: 0.5,
          duration: 800,
          easing: Easing.inOut(Easing.ease),
          useNativeDriver: Platform.OS !== 'web',
        }),
      ])
    );
    loop.start();
    return () => loop.stop();
  }, [opacity]);

  return (
    <Animated.View
      className={cn('rounded-md bg-muted', className)}
      style={[{ opacity }, style]}
      {...props}
    />
  );
}

export { Skeleton };
