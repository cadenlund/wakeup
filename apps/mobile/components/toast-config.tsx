// Shared `react-native-toast-message` config + a thin `<Toast>`
// re-export so any screen that renders inside a native iOS modal
// can mount its own Toast instance and have toasts paint ABOVE
// the modal chrome instead of behind it.
//
// react-native-toast-message keeps a stack of mounted Toast refs
// and uses the last-mounted one as the active singleton. Mounting
// inside a screen that's pushed as `presentation: 'modal'` means
// that screen's Toast wins while it's mounted; when it unmounts
// the root one in `<ToastRoot>` takes back over.
//
// Variants: `error` / `success` / `info` are passive notices;
// `event` is a heads-up about something elsewhere — the whole pill
// is tappable (the lib passes the `onPress` from `Toast.show`) and
// shows a chevron so it reads as actionable. All four share the
// same card chrome so a passive toast and an event toast occupy the
// same slot identically.
import { ChevronRight } from 'lucide-react-native';
import Toast, { type BaseToastProps } from 'react-native-toast-message';
import { Pressable, View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type Variant = 'error' | 'success' | 'info' | 'event';

const VARIANT_BORDER: Record<Variant, string> = {
  error: 'border-l-destructive',
  success: 'border-l-primary',
  info: 'border-l-border',
  event: 'border-l-primary',
};

function ThemedToast({
  variant,
  text1,
  text2,
  onPress,
}: {
  variant: Variant;
  text1?: string;
  text2?: string;
  onPress?: () => void;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  const actionable = variant === 'event' && !!onPress;
  const card = (
    <View
      className={`mx-4 w-[92%] max-w-md flex-row items-center gap-3 rounded-xl border border-l-4 border-border bg-card px-4 py-3 shadow-lg shadow-black/20 ${VARIANT_BORDER[variant]}`}>
      <View className="flex-1 gap-1">
        {text1 ? (
          <Text numberOfLines={1} className="text-sm font-semibold text-foreground">
            {text1}
          </Text>
        ) : null}
        {text2 ? (
          <Text numberOfLines={1} className="text-sm text-muted-foreground">
            {text2}
          </Text>
        ) : null}
      </View>
      {actionable ? <ChevronRight size={18} color={mutedFg} /> : null}
    </View>
  );
  if (!actionable) return card;
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={text2 ? `${text1}. ${text2}` : text1}
      testID="event-toast"
      className="active:opacity-80">
      {card}
    </Pressable>
  );
}

export const toastConfig = {
  error: (props: BaseToastProps) => (
    <ThemedToast variant="error" text1={props.text1} text2={props.text2} />
  ),
  success: (props: BaseToastProps) => (
    <ThemedToast variant="success" text1={props.text1} text2={props.text2} />
  ),
  info: (props: BaseToastProps) => (
    <ThemedToast variant="info" text1={props.text1} text2={props.text2} />
  ),
  event: (props: BaseToastProps) => (
    <ThemedToast variant="event" text1={props.text1} text2={props.text2} onPress={props.onPress} />
  ),
};

export const TOAST_TOP_OFFSET = 60;

export { Toast };
