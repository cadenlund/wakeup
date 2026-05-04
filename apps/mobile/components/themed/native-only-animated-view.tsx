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
// React 19 + Reanimated 4's stricter ref typing rejects the original
// `React.RefAttributes<typeof Animated.View>` (Animated.View is a
// ComponentClass; refs want Component). The component callers don't
// thread refs through this wrapper anyway, so dropping the explicit
// ref attributes is the cleanest fix.
function NativeOnlyAnimatedView(props: React.ComponentProps<typeof Animated.View>) {
  if (Platform.OS === "web") {
    return <>{props.children as React.ReactNode}</>;
  }
  return <Animated.View {...props} />;
}

export { NativeOnlyAnimatedView };
