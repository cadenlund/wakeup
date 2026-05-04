import { cn } from "@/lib/utils";
import { Platform, TextInput } from "react-native";

// `placeholderClassName` is a NativeWind-injected prop (the
// className-aware polyfill in metro.config.js handles it at the
// transformer layer) so it isn't on RN's stock TextInputProps. Local
// prop type extension keeps tsc happy without a global declaration.
type TextareaProps = React.ComponentProps<typeof TextInput> & {
  placeholderClassName?: string;
};

function Textarea({
  className,
  multiline = true,
  numberOfLines = Platform.select({ web: 2, native: 8 }), // On web, numberOfLines also determines initial height. On native, it determines the maximum height.
  placeholderClassName,
  ...props
}: TextareaProps) {
  // The polyfill consumes `placeholderClassName` at runtime, but TS
  // doesn't see it on TextInputProps. Cast at the JSX boundary so the
  // unknown prop slips past type-checking — same shape RNR ships.
  const extraProps = {
    placeholderClassName: cn("text-muted-foreground", placeholderClassName),
  } as Partial<React.ComponentProps<typeof TextInput>>;

  return (
    <TextInput
      className={cn(
        "text-foreground border-input dark:bg-input/30 flex min-h-16 w-full flex-row rounded-md border bg-transparent px-3 py-2 text-base shadow-sm shadow-black/5 md:text-sm",
        Platform.select({
          web: "placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive field-sizing-content resize-y outline-none transition-[color,box-shadow] focus-visible:ring-[3px] disabled:cursor-not-allowed",
        }),
        props.editable === false && "opacity-50",
        className,
      )}
      multiline={multiline}
      numberOfLines={numberOfLines}
      textAlignVertical="top"
      {...extraProps}
      {...props}
    />
  );
}

export { Textarea };
