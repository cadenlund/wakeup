// Phase 7.5 — in-app event banner (WAKEUPEXPO §4.13).
//
// Root-mounted singleton that renders the HEAD of `useBannerStore`'s
// queue as a card that slides in from the top. The dispatcher owns
// the enqueue/skip decision (`lib/ws/dispatcher.ts`) — this component
// just renders whatever's queued.
//
// Behaviour:
//   - slides down on appear; light haptic on appear
//   - tap → push the event's route, then dismiss
//   - swipe up → dismiss
//   - 4s auto-dismiss
//   - dismissing advances the queue; the next event (if any) slides in
//
// Style mirrors the toast (`components/toast-config.tsx`'s
// `ThemedToast`) — same rounded card / border / shadow / type — so
// the two read as one notification system. It sits below the toast
// slot so a banner and a toast on screen together stack rather than
// overlap (per design feedback).
import { useRouter } from 'expo-router';
import * as React from 'react';
import { Animated, PanResponder, Pressable, View } from 'react-native';

import { TOAST_TOP_OFFSET } from '@/components/toast-config';
import { Text } from '@/components/ui/text';
import { haptics } from '@/lib/haptics';
import { useBannerStore } from '@/lib/banner/store';

// Banners park below the toast slot so the two never overlap; ~56 is
// a comfortable clear of a one-line toast.
const BANNER_BELOW_TOAST = 56;
const AUTO_DISMISS_MS = 4_000;
// How far the card slides in from (negative = above the screen).
const SLIDE_FROM = -160;
// Swipe-up distance / velocity past which a release dismisses.
const SWIPE_DISMISS_DISTANCE = 28;
const SWIPE_DISMISS_VELOCITY = -0.6;

export function EventBanner(): React.ReactElement | null {
  const head = useBannerStore((s) => s.queue[0]);
  if (!head) return null;
  // Remount per event so each gets a fresh slide-in + timer.
  return <BannerCard key={head.id} />;
}

function BannerCard(): React.ReactElement | null {
  const head = useBannerStore((s) => s.queue[0]);
  const dismissHead = useBannerStore((s) => s.dismissHead);
  const router = useRouter();
  const translateY = React.useRef(new Animated.Value(SLIDE_FROM)).current;

  // Slide in + haptic on appear; auto-dismiss after 4s.
  React.useEffect(() => {
    haptics.tap();
    Animated.spring(translateY, {
      toValue: 0,
      useNativeDriver: true,
      bounciness: 6,
      speed: 14,
    }).start();
    const timer = setTimeout(() => dismissHead(), AUTO_DISMISS_MS);
    return () => clearTimeout(timer);
    // translateY is a stable ref; dismissHead is a stable store action.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const slideOutAndDismiss = React.useCallback(() => {
    Animated.timing(translateY, {
      toValue: SLIDE_FROM,
      duration: 160,
      useNativeDriver: true,
    }).start(() => dismissHead());
  }, [translateY, dismissHead]);

  const panResponder = React.useMemo(
    () =>
      PanResponder.create({
        // Let the Pressable own taps; only steal the gesture once
        // it reads as an upward drag.
        onStartShouldSetPanResponder: () => false,
        onMoveShouldSetPanResponder: (_e, g) => g.dy < -4 && Math.abs(g.dy) > Math.abs(g.dx),
        // Track upward drag only; clamp at the resting position so it
        // can't be pulled down.
        onPanResponderMove: (_e, g) => translateY.setValue(Math.min(0, g.dy)),
        onPanResponderRelease: (_e, g) => {
          if (g.dy < -SWIPE_DISMISS_DISTANCE || g.vy < SWIPE_DISMISS_VELOCITY) slideOutAndDismiss();
          else Animated.spring(translateY, { toValue: 0, useNativeDriver: true }).start();
        },
      }),
    [translateY, slideOutAndDismiss]
  );

  if (!head) return null;

  const onPress = () => {
    dismissHead();
    router.push(head.route as never);
  };

  return (
    // box-none so taps outside the card pass through to the screen
    // behind it; the card itself (and its swipe handler) still works.
    <Animated.View
      pointerEvents="box-none"
      style={{
        position: 'absolute',
        left: 0,
        right: 0,
        top: TOAST_TOP_OFFSET + BANNER_BELOW_TOAST,
        transform: [{ translateY }],
        zIndex: 50,
        elevation: 50,
      }}>
      <View {...panResponder.panHandlers} className="mx-4 max-w-md self-center">
        <Pressable
          onPress={onPress}
          accessibilityRole="button"
          accessibilityLabel={head.body ? `${head.title}. ${head.body}` : head.title}
          testID="event-banner"
          className="flex-row gap-3 rounded-xl border border-l-4 border-border bg-card px-4 py-2.5 shadow-lg shadow-black/20">
          <View className="flex-1 gap-0.5">
            <Text numberOfLines={1} className="text-sm font-semibold text-foreground">
              {head.title}
            </Text>
            {head.body ? (
              <Text numberOfLines={1} className="text-sm text-muted-foreground">
                {head.body}
              </Text>
            ) : null}
          </View>
        </Pressable>
      </View>
    </Animated.View>
  );
}
