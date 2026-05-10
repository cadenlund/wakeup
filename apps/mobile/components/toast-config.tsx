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
import Toast, { type BaseToastProps } from 'react-native-toast-message';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';

type Variant = 'error' | 'success' | 'info';

const VARIANT_BORDER: Record<Variant, string> = {
  error: 'border-l-destructive',
  success: 'border-l-primary',
  info: 'border-l-border',
};

function ThemedToast({
  variant,
  text1,
  text2,
}: {
  variant: Variant;
  text1?: string;
  text2?: string;
}) {
  return (
    <View
      className={`mx-4 w-[92%] max-w-md flex-row gap-3 rounded-xl border border-l-4 border-border bg-card px-4 py-3 shadow-lg shadow-black/20 ${VARIANT_BORDER[variant]}`}>
      <View className="flex-1 gap-1">
        {text1 ? <Text className="text-sm font-semibold text-foreground">{text1}</Text> : null}
        {text2 ? <Text className="text-sm text-muted-foreground">{text2}</Text> : null}
      </View>
    </View>
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
};

export const TOAST_TOP_OFFSET = 60;

export { Toast };
