// Forgot-password placeholder (Phase 3.3 stub). Real form +
// `POST /v1/auth/password-reset/request` lands in the next group.
// Routed today so login's "Forgot?" link doesn't dead-end.
import { useRouter } from 'expo-router';
import { ArrowLeft, Moon } from 'lucide-react-native';
import { Pressable, View } from 'react-native';

import { AuthScreenLayout } from '@/components/auth-screen-layout';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function ForgotScreen() {
  const router = useRouter();
  const primaryColor = useThemeColor('primary');

  // `router.back()` plays the stack's reverse animation (slide-from-
  // left when forward is slide-from-right) — that's the directional
  // swipe the user expects when going back to login. If the user
  // deep-linked here without a back stack, fall through to a plain
  // replace so the link never dead-ends.
  const goBack = () => {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/login');
    }
  };

  return (
    <AuthScreenLayout>
      <View className="gap-8">
        <View className="flex-row items-center gap-2 lg:hidden">
          <Moon size={22} color={primaryColor} />
          <Text className="text-lg font-semibold tracking-tight">Wakeup</Text>
        </View>

        <View className="gap-2">
          <Text variant="h1" className="text-left text-4xl">
            Reset password
          </Text>
          <Text variant="muted" className="text-base">
            Email reset is coming next. For now, ping the operator.
          </Text>
        </View>

        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Back to sign in"
          testID="forgot-back"
          onPress={goBack}
          className="flex-row items-center gap-2 self-start py-2">
          <ArrowLeft size={18} color={primaryColor} />
          <Text className="text-sm font-semibold text-primary">Back to sign in</Text>
        </Pressable>
      </View>
    </AuthScreenLayout>
  );
}
