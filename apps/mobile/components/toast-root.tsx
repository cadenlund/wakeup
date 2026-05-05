// Mount point for `react-native-toast-message`. Renders our own
// themed toast component for every type so the visual stays
// consistent with the rest of the app — bg-card surface, theme-
// tinted left border per severity, foreground/muted-foreground
// type colours that follow the active scheme + light/dark.
//
// Same component works on iOS, Android, and web via the same
// React Native primitives — no platform split, no system-styled
// pill that ignores our palette.
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

const toastConfig = {
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

export function ToastRoot() {
  return <Toast config={toastConfig} topOffset={60} />;
}
