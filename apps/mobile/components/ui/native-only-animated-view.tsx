import { Platform } from "react-native";
import Animated from "react-native-reanimated";

/**
 * This component is used to wrap animated views that should only be animated on native.
 * @param props - The props for the animated view.
 * @returns The animated view if the platform is native, otherwise the children.
 * @example
 * <NativeOnlyAnimatedView entering={FadeIn} exiting={FadeOut}>
 *   <Text>I am only animated on native</Text>
 * </NativeOnlyAnimatedView>
 */
// Drops the React.RefAttributes<typeof Animated.View> from RNR's stock
// signature — `typeof Animated.View` is the class, not the instance, so
// the constraint widens incorrectly under React 19's stricter ref types.
// Animated.View's own props already accept a ref via the spread, so the
// explicit RefAttributes was redundant.
function NativeOnlyAnimatedView(props: React.ComponentProps<typeof Animated.View>) {
  if (Platform.OS === "web") {
    return <>{props.children as React.ReactNode}</>;
  } else {
    return <Animated.View {...props} />;
  }
}

export { NativeOnlyAnimatedView };
