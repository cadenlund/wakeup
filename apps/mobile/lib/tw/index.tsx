// CSS-aware wrappers around the React Native primitives.
//
// As of the RNR foundation (§3.1), the global classname polyfill is on
// in metro.config.js, so a bare `<View className="..." />` already
// resolves correctly. These wrappers stay for two reasons:
//   1. TouchableHighlight needs `underlayColor` extracted from a
//      flattened style object — not something the polyfill handles.
//   2. AnimatedScrollView accepts both `contentClassName` and
//      `contentContainerClassName`, mapped to `contentContainerStyle`.
// New code can import primitives directly from `react-native`; reach
// for these wrappers only when the special-case behaviour matters.
import { Link as RouterLink } from "expo-router";
import * as React from "react";
import {
  Pressable as RNPressable,
  ScrollView as RNScrollView,
  Text as RNText,
  TextInput as RNTextInput,
  TouchableHighlight as RNTouchableHighlight,
  View as RNView,
  StyleSheet,
} from "react-native";
import Animated from "react-native-reanimated";
import { useCssElement, useNativeVariable as useFunctionalVariable } from "react-native-css";

export type ViewProps = React.ComponentProps<typeof RNView> & {
  className?: string;
};

export const View = (props: ViewProps) => {
  return useCssElement(RNView, props, { className: "style" });
};
View.displayName = "CSS(View)";

export type TextProps = React.ComponentProps<typeof RNText> & {
  className?: string;
};

export const Text = (props: TextProps) => {
  return useCssElement(RNText, props, { className: "style" });
};
Text.displayName = "CSS(Text)";

export type ScrollViewProps = React.ComponentProps<typeof RNScrollView> & {
  className?: string;
  contentContainerClassName?: string;
};

export const ScrollView = (props: ScrollViewProps) => {
  return useCssElement(RNScrollView, props, {
    className: "style",
    contentContainerClassName: "contentContainerStyle",
  });
};
ScrollView.displayName = "CSS(ScrollView)";

export type PressableProps = React.ComponentProps<typeof RNPressable> & {
  className?: string;
};

export const Pressable = (props: PressableProps) => {
  return useCssElement(RNPressable, props, { className: "style" });
};
Pressable.displayName = "CSS(Pressable)";

export type TextInputProps = React.ComponentProps<typeof RNTextInput> & {
  className?: string;
};

export const TextInput = (props: TextInputProps) => {
  return useCssElement(RNTextInput, props, { className: "style" });
};
TextInput.displayName = "CSS(TextInput)";

// TouchableHighlight is special: RN reads `underlayColor` as a top-level
// prop, not a style. Tailwind users would naturally want to write
// `underlay-color: ...` as a CSS variable, so we extract it from the
// flattened style and re-route to the prop.
function HighlightWithUnderlay(props: React.ComponentProps<typeof RNTouchableHighlight>) {
  // StyleSheet.flatten widens to a union that's painful to narrow
  // through; we know our consumers pass either a className-derived
  // object or undefined here, so the inline cast keeps the wrapper
  // honest without leaking into the public types.
  const flat = StyleSheet.flatten(props.style) as
    | (Record<string, unknown> & { underlayColor?: string })
    | undefined;
  const underlayColor = flat?.underlayColor ?? props.underlayColor;
  let style = props.style;
  if (flat) {
    const { underlayColor: _drop, ...rest } = flat;
    style = rest as typeof props.style;
  }
  return <RNTouchableHighlight {...props} underlayColor={underlayColor} style={style} />;
}

export type TouchableHighlightProps = React.ComponentProps<typeof RNTouchableHighlight> & {
  className?: string;
};

export const TouchableHighlight = (props: TouchableHighlightProps) => {
  return useCssElement(HighlightWithUnderlay, props, { className: "style" });
};
TouchableHighlight.displayName = "CSS(TouchableHighlight)";

export type LinkProps = React.ComponentProps<typeof RouterLink> & {
  className?: string;
};

export const Link = (props: LinkProps) => {
  return useCssElement(RouterLink, props, { className: "style" });
};

Link.Trigger = RouterLink.Trigger;
Link.Menu = RouterLink.Menu;
Link.MenuAction = RouterLink.MenuAction;
Link.Preview = RouterLink.Preview;

// AnimatedScrollView surfaces both styles a Reanimated ScrollView accepts.
// Reanimated's component types recurse deep enough to trip TS2589, so we
// type-erase BOTH the wrapped component and the useCssElement call —
// className routing is what matters here, not exact prop typing.
// Consumers still get autocomplete on the public wrapper signature.
export type AnimatedScrollViewProps = Record<string, unknown> & {
  className?: string;
  contentClassName?: string;
  contentContainerClassName?: string;
};

const UntypedAnimatedScrollView = Animated.ScrollView as unknown as React.ComponentType<unknown>;

const useCssElementUntyped = useCssElement as unknown as (
  Component: React.ComponentType<unknown>,
  props: Record<string, unknown>,
  classNameMap: Record<string, string>,
) => React.ReactElement;

export const AnimatedScrollView = (props: AnimatedScrollViewProps) => {
  return useCssElementUntyped(UntypedAnimatedScrollView, props, {
    className: "style",
    contentClassName: "contentContainerStyle",
    contentContainerClassName: "contentContainerStyle",
  });
};
AnimatedScrollView.displayName = "CSS(AnimatedScrollView)";

// `useCSSVariable("--my-var")` returns the resolved native value on iOS /
// Android via the runtime variable hook, and the string `var(--my-var)`
// on web so the browser can resolve it from the cascade. The platform
// branch ships at module-load time so there's no per-call overhead.
export const useCSSVariable: (variable: string) => string =
  process.env.EXPO_OS === "web"
    ? (variable: string) => `var(${variable})`
    : (useFunctionalVariable as (variable: string) => string);
