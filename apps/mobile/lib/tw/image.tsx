// CSS-aware Image. Wraps expo-image (the project-mandated image
// component per WAKEUPEXPO.md §3 — RN's <Image> is banned via the
// `apps/mobile/**` CodeRabbit rule).
//
// expo-image uses `contentFit` / `contentPosition` instead of CSS's
// `object-fit` / `object-position`. We re-map at the boundary so
// `className="object-cover"` works the way Tailwind users expect.
import { Image as RNImage } from "expo-image";
import * as React from "react";
import { StyleSheet } from "react-native";
import Animated from "react-native-reanimated";
import { useCssElement } from "react-native-css";

// Animated.createAnimatedComponent + expo-image's deep prop union trips
// TS2589 on the wrapper signature; type-erase at the boundary so the
// className path stays usable without a 5-page error.
const AnimatedExpoImage = Animated.createAnimatedComponent(
  RNImage,
) as unknown as React.ComponentType<
  React.ComponentProps<typeof RNImage> & {
    // Accept a few animated-view escape hatches (transforms, opacity,
    // etc.) without enumerating Reanimated's full typing surface.
    style?: unknown;
  }
>;

type CSSImageProps = React.ComponentProps<typeof RNImage> & {
  source: React.ComponentProps<typeof RNImage>["source"] | string;
  style?: Record<string, unknown> | unknown;
};

function CSSImage(props: CSSImageProps) {
  const flat = StyleSheet.flatten(props.style as Parameters<typeof StyleSheet.flatten>[0]) as
    | (Record<string, unknown> & {
        objectFit?: "contain" | "cover" | "fill" | "scale-down" | "none";
        objectPosition?: string;
      })
    | undefined;
  const objectFit = flat?.objectFit;
  const objectPosition = flat?.objectPosition;
  let style = props.style as Record<string, unknown> | undefined;
  if (flat) {
    const { objectFit: _f, objectPosition: _p, ...rest } = flat;
    style = rest;
  }

  return (
    <AnimatedExpoImage
      contentFit={objectFit}
      // expo-image's ImageContentPosition accepts a CSS-shorthand
      // string ("top right", "50% 25%", etc.) but the type is the
      // narrower struct union. Cast at the boundary.
      contentPosition={objectPosition as React.ComponentProps<typeof RNImage>["contentPosition"]}
      {...props}
      source={typeof props.source === "string" ? { uri: props.source } : props.source}
      style={style}
    />
  );
}

export type ImageProps = CSSImageProps & {
  className?: string;
};

export const Image = (props: ImageProps) => {
  return useCssElement(CSSImage, props, { className: "style" });
};
Image.displayName = "CSS(Image)";
