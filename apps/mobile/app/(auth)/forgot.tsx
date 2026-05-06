// Forgot-password screen (Phase 3.3). Email entry → POST
// /v1/auth/password-reset/request. The backend always responds 204
// (anti-enumeration), so the UI shows the same "Check your email"
// success state for known + unknown emails. The actual mail dispatch
// happens server-side via Resend; the user clicks the email link
// (deep-link via RESET_PASSWORD_URL_BASE) and lands on /reset.
import { useRouter } from 'expo-router';
import { ArrowLeft, Mail, Moon } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { AuthScreenLayout } from '@/components/auth-screen-layout';
import { Button } from '@/components/ui/button';
import { FieldError } from '@/components/ui/field-error';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Text } from '@/components/ui/text';
import { usePostV1AuthPasswordResetRequest } from '@/lib/api/hooks/auth/auth';
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function ForgotScreen() {
  const router = useRouter();
  const primaryColor = useThemeColor('primary');
  const [email, setEmail] = React.useState('');
  const [submitted, setSubmitted] = React.useState(false);

  const reset = usePostV1AuthPasswordResetRequest({
    mutation: {
      onSuccess: () => {
        setSubmitted(true);
      },
    },
  });
  const fieldErrors = useFieldErrors(reset.error);
  const topError = useTopLevelError(reset.error);

  const submit = () => {
    if (!email.trim()) {
      haptics.warning();
      return;
    }
    reset.mutate({ data: { email: email.trim() } });
  };

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

        {submitted ? (
          <View className="gap-6">
            <View className="items-center gap-4 py-4">
              <View className="rounded-2xl bg-primary/10 p-4">
                <Mail size={40} color={primaryColor} />
              </View>
              <View className="gap-2">
                <Text variant="h1" className="text-center text-3xl">
                  Check your email
                </Text>
                <Text variant="muted" className="text-center text-base">
                  If an account exists for {email.trim()}, we&apos;ve sent a link to reset your
                  password. The link expires in 1 hour.
                </Text>
              </View>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Back to sign in"
              testID="forgot-back"
              onPress={goBack}
              className="flex-row items-center justify-center gap-2 py-3">
              <ArrowLeft size={18} color={primaryColor} />
              <Text className="text-sm font-semibold text-primary">Back to sign in</Text>
            </Pressable>
          </View>
        ) : (
          <>
            <View className="gap-2">
              <Text variant="h1" className="text-left text-4xl">
                Reset password
              </Text>
              <Text variant="muted" className="text-base">
                Enter the email on your account. We&apos;ll send a link to set a new password.
              </Text>
            </View>

            <View className="gap-2">
              <Label nativeID="email-label">Email</Label>
              <Input
                testID="forgot-email"
                accessibilityLabel="Email"
                aria-labelledby="email-label"
                value={email}
                onChangeText={setEmail}
                keyboardType="email-address"
                autoCapitalize="none"
                autoCorrect={false}
                autoComplete="email"
                returnKeyType="go"
                onSubmitEditing={submit}
                editable={!reset.isPending}
              />
              <FieldError message={fieldErrors.email} />
            </View>

            <View className="gap-3">
              <Button
                size="lg"
                testID="forgot-submit"
                accessibilityRole="button"
                accessibilityLabel="Send reset link"
                onPress={submit}
                disabled={reset.isPending || !email.trim()}>
                <Text>{reset.isPending ? 'Sending…' : 'Send reset link'}</Text>
              </Button>

              {topError ? (
                <Text testID="forgot-top-error" className="text-center text-sm text-destructive">
                  {topError}
                </Text>
              ) : null}

              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Back to sign in"
                testID="forgot-back"
                onPress={goBack}
                className="flex-row items-center justify-center gap-2 py-2">
                <ArrowLeft size={18} color={primaryColor} />
                <Text className="text-sm font-semibold text-primary">Back to sign in</Text>
              </Pressable>
            </View>
          </>
        )}
      </View>
    </AuthScreenLayout>
  );
}
