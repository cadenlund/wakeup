// Register screen (Phase 3.2). Display name + username + email +
// password → POST /v1/auth/register. The backend sets a session
// cookie on success (auto sign-in), so the post-success path is the
// same as login: invalidate `me`, replace route to (tabs).
import { useRouter } from 'expo-router';
import { Moon } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { AuthScreenLayout } from '@/components/auth-screen-layout';
import { Button } from '@/components/ui/button';
import { FieldError } from '@/components/ui/field-error';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { PasswordInput } from '@/components/ui/password-input';
import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey, usePostV1AuthRegister } from '@/lib/api/hooks/auth/auth';
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function RegisterScreen() {
  const router = useRouter();
  const qc = useQueryClient();
  const primaryColor = useThemeColor('primary');
  const [displayName, setDisplayName] = React.useState('');
  const [username, setUsername] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');

  const register = usePostV1AuthRegister({
    mutation: {
      onSuccess: async (response) => {
        haptics.success();
        // Register envelope is `{ user: MeResponse }`. Push the
        // embedded user into the me-query cache up front so
        // AuthGate routes from `onboarded_at: null` directly to
        // /(onboarding) without first flashing /(tabs) while the
        // me query refetches.
        const body = response as unknown as { user?: { id?: string; onboarded_at?: string } };
        if (body?.user?.id) {
          qc.setQueryData(getGetV1AuthMeQueryKey(), body.user);
        }
        await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
        router.replace('/(onboarding)');
      },
    },
  });
  const fieldErrors = useFieldErrors(register.error);
  const topError = useTopLevelError(register.error);

  const valid =
    displayName.trim().length > 0 &&
    username.trim().length >= 3 &&
    email.trim().length > 0 &&
    password.length >= 8;

  const submit = () => {
    if (!valid) return;
    register.mutate({
      data: {
        display_name: displayName.trim(),
        username: username.trim(),
        email: email.trim(),
        password,
      },
    });
  };

  // Same back-stack-aware swap as login → register: pop if we can,
  // push as a fallback when the user deep-linked into /register.
  const goToLogin = () => {
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
            Create account
          </Text>
          <Text variant="muted" className="text-base">
            Pick a username your friends will recognise.
          </Text>
        </View>

        <View className="gap-5">
          <View className="gap-2">
            <Label nativeID="display-name-label">Display name</Label>
            <Input
              testID="register-display-name"
              accessibilityLabel="Display name"
              aria-labelledby="display-name-label"
              value={displayName}
              onChangeText={setDisplayName}
              autoCapitalize="words"
              returnKeyType="next"
              editable={!register.isPending}
            />
            <FieldError message={fieldErrors.display_name} />
          </View>

          <View className="gap-2">
            <Label nativeID="username-label">Username</Label>
            <Input
              testID="register-username"
              accessibilityLabel="Username"
              aria-labelledby="username-label"
              value={username}
              onChangeText={setUsername}
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="username-new"
              returnKeyType="next"
              editable={!register.isPending}
            />
            {fieldErrors.username ? (
              <FieldError message={fieldErrors.username} />
            ) : (
              <Text variant="muted" className="text-xs">
                3–32 characters. Letters, numbers, underscores.
              </Text>
            )}
          </View>

          <View className="gap-2">
            <Label nativeID="email-label">Email</Label>
            <Input
              testID="register-email"
              accessibilityLabel="Email"
              aria-labelledby="email-label"
              value={email}
              onChangeText={setEmail}
              keyboardType="email-address"
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="email"
              returnKeyType="next"
              editable={!register.isPending}
            />
            <FieldError message={fieldErrors.email} />
          </View>

          <View className="gap-2">
            <Label nativeID="password-label">Password</Label>
            <PasswordInput
              testID="register-password"
              accessibilityLabel="Password"
              aria-labelledby="password-label"
              value={password}
              onChangeText={setPassword}
              autoComplete="new-password"
              returnKeyType="go"
              onSubmitEditing={submit}
              editable={!register.isPending}
            />
            {fieldErrors.password ? (
              <FieldError message={fieldErrors.password} />
            ) : (
              <Text variant="muted" className="text-xs">
                At least 8 characters.
              </Text>
            )}
          </View>
        </View>

        <View className="gap-3">
          <Button
            size="lg"
            testID="register-submit"
            accessibilityRole="button"
            accessibilityLabel="Create account"
            onPress={submit}
            disabled={register.isPending || !valid}>
            <Text>{register.isPending ? 'Creating account…' : 'Create account'}</Text>
          </Button>

          {topError ? (
            <Text testID="register-top-error" className="text-center text-sm text-destructive">
              {topError}
            </Text>
          ) : null}

          <View className="flex-row items-center justify-center gap-1.5 pt-1">
            <Text variant="muted" className="text-sm">
              Already have an account?
            </Text>
            <Pressable
              testID="register-go-login"
              accessibilityRole="button"
              accessibilityLabel="Sign in"
              onPress={goToLogin}
              className="py-1">
              <Text className="text-sm font-semibold text-primary">Sign in</Text>
            </Pressable>
          </View>
        </View>
      </View>
    </AuthScreenLayout>
  );
}
