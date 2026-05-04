import { TextClassContext } from "@/components/themed/text";
import { cn } from "@/lib/utils";
import type { LucideIcon, LucideProps } from "lucide-react-native";
import * as React from "react";

type IconProps = LucideProps & {
  as: LucideIcon;
} & React.RefAttributes<LucideIcon>;

/**
 * A wrapper component for Lucide icons that threads className through
 * the TextClassContext so siblings of a themed Text inherit the same
 * colour. The original RNR icon used NativeWind v4's `cssInterop` to
 * map height/width style → the icon's `size` prop; v5 doesn't export
 * `cssInterop`, so we drop that path. Callers pass `size` explicitly
 * and use `color={...}` if they need a non-default colour.
 */
function Icon({ as: IconComponent, className, size = 14, ...props }: IconProps) {
  const textClass = React.useContext(TextClassContext);
  return (
    <IconComponent className={cn("text-foreground", textClass, className)} size={size} {...props} />
  );
}

export { Icon };
