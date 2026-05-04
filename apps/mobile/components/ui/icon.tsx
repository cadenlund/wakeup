import { TextClassContext } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import type { LucideIcon, LucideProps } from "lucide-react-native";
import { styled } from "nativewind";
import * as React from "react";

type IconProps = LucideProps & {
  as: LucideIcon;
};

function IconImpl({ as: IconComponent, ...props }: IconProps) {
  return <IconComponent {...props} />;
}

// NativeWind v5's `styled` replaces v4's `cssInterop`. Same semantics:
// className is consumed and the resolved style fields height/width get
// piped to Lucide's `size` prop so utilities like `size-4` size the icon.
//
// Type-erased at the boundary because `styled` returns `any` and the
// nativeStyleToProp generic doesn't infer cleanly through the
// IconImpl wrapper — the runtime behavior is the contract that
// matters here.
const StyledIconImpl: React.ComponentType<IconProps & { className?: string }> = (styled as any)(
  IconImpl,
  {
    className: {
      target: "style",
      nativeStyleToProp: {
        height: "size",
        width: "size",
      },
    },
  },
);

/**
 * A wrapper component for Lucide icons with Nativewind `className` support via `cssInterop`.
 *
 * This component allows you to render any Lucide icon while applying utility classes
 * using `nativewind`. It avoids the need to wrap or configure each icon individually.
 *
 * @component
 * @example
 * ```tsx
 * import { ArrowRight } from 'lucide-react-native';
 * import { Icon } from '@/registry/components/ui/icon';
 *
 * <Icon as={ArrowRight} className="text-red-500" size={16} />
 * ```
 *
 * @param {LucideIcon} as - The Lucide icon component to render.
 * @param {string} className - Utility classes to style the icon using Nativewind.
 * @param {number} size - Icon size (defaults to 14).
 * @param {...LucideProps} ...props - Additional Lucide icon props passed to the "as" icon.
 */
function Icon({
  as: IconComponent,
  className,
  size = 14,
  ...props
}: IconProps & { className?: string }) {
  const textClass = React.useContext(TextClassContext);
  return (
    <StyledIconImpl
      as={IconComponent}
      className={cn("text-foreground", textClass, className)}
      size={size}
      {...props}
    />
  );
}

export { Icon };
