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
// Variants:
//   - `error` / `success` / `info` — passive notices: a card with a
//     coloured left accent bar, no avatar, not tappable.
//   - `event` — a heads-up about something elsewhere (a new message,
//     a friend request): the whole pill is tappable (the lib passes
//     the `onPress` from `Toast.show`), shows the sender's avatar on
//     the left + a chevron on the right so it reads like a
//     notification. No accent bar — the avatar carries the identity.
// All variants share the same card width / position so the slot is
// consistent.
import { ChevronRight } from 'lucide-react-native';
import Toast, { type BaseToastProps, type ToastConfigParams } from 'react-native-toast-message';
import { Pressable, View } from 'react-native';

import { Avatar } from '@/components/ui/avatar';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const ACCENT: Record<'error' | 'success' | 'info', string> = {
  error: 'border-l-destructive',
  success: 'border-l-primary',
  info: 'border-l-border',
};

const CARD =
  'mx-4 w-[92%] max-w-md flex-row items-center gap-3 rounded-xl bg-card px-4 py-3 shadow-lg shadow-black/20';

function PassiveToast({
  variant,
  text1,
  text2,
}: {
  variant: 'error' | 'success' | 'info';
  text1?: string;
  text2?: string;
}) {
  return (
    <View className={`${CARD} border border-l-4 border-border ${ACCENT[variant]}`}>
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
    </View>
  );
}

type EventToastProps = { avatarUrl?: string | null; fallbackName?: string };

function EventToast({
  text1,
  text2,
  onPress,
  extra,
}: {
  text1?: string;
  text2?: string;
  onPress?: () => void;
  extra?: EventToastProps;
}) {
  const mutedFg = useThemeColor('muted-foreground');
  const card = (
    <View className={`${CARD} border border-border`}>
      <Avatar source={extra?.avatarUrl} fallbackName={extra?.fallbackName} size={36} />
      <View className="flex-1 gap-0.5">
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
      <ChevronRight size={18} color={mutedFg} />
    </View>
  );
  if (!onPress) return card;
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
    <PassiveToast variant="error" text1={props.text1} text2={props.text2} />
  ),
  success: (props: BaseToastProps) => (
    <PassiveToast variant="success" text1={props.text1} text2={props.text2} />
  ),
  info: (props: BaseToastProps) => (
    <PassiveToast variant="info" text1={props.text1} text2={props.text2} />
  ),
  event: (props: ToastConfigParams<EventToastProps>) => (
    <EventToast
      text1={props.text1}
      text2={props.text2}
      onPress={props.onPress}
      extra={props.props}
    />
  ),
};

export const TOAST_TOP_OFFSET = 60;

export { Toast };
