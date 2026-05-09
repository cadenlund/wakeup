// Login screen (Phase 3.1). Username-or-email + password → POST
// /v1/auth/login. The backend sets an scs cookie on success.
// onSuccess primes the me-query cache from the response envelope, then
// imperatively routes to the next group based on `onboarded_at`. The
// cache prime ensures Stack.Protected's guards have flipped by the
// time the navigation lands, so the user doesn't bounce.
import { Link, useRouter } from 'expo-router';
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
import { usePostV1AuthLogin } from '@/lib/api/hooks/auth/auth';
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { signedInAs } from '@/lib/auth/post-auth-nav';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

export default function LoginScreen() {
  const router = useRouter();
  const qc = useQueryClient();
  const primaryColor = useThemeColor('primary');
  const [identifier, setIdentifier] = React.useState('');
  const [password, setPassword] = React.useState('');

  const login = usePostV1AuthLogin({
    mutation: {
      onSuccess: (response) => {
        haptics.success();
        toast.success('Welcome back');
        // Login envelope is `{ user: MeResponse }`; orval types it
        // as the wrapped `{data, status, headers}` envelope but
        // apiFetch returns the unwrapped body. signedInAs primes
        // the me-cache + navigates; see lib/auth/post-auth-nav.ts
        // for the cache + invalidate + setTimeout dance.
        const body = response as unknown as { user?: { id?: string; onboarded_at?: string } };
        if (body?.user) {
          signedInAs(qc, router, body.user);
        }
      },
    },
  });
  const fieldErrors = useFieldErrors(login.error);
  const topError = useTopLevelError(login.error);

  const submit = () => {
    if (!identifier.trim() || !password) {
      haptics.warning();
      return;
    }
    login.mutate({ data: { identifier: identifier.trim(), password } });
  };

  // If the user is already on a back-stack with /register (i.e. they
  // bounced login → register → login), tapping "Create one" should
  // pop back rather than push another /register on top — that way
  // the slide animation plays in the reverse direction and the
  // history doesn't grow on every swap.
  const goToRegister = () => {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.push('/register');
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
            Sign in
          </Text>
          <Text variant="muted" className="text-base">
            Welcome back. We saved your spot.
          </Text>
        </View>

        <View className="gap-5">
          <View className="gap-2">
            <Label nativeID="identifier-label">Username or email</Label>
            <Input
              testID="login-identifier"
              accessibilityLabel="Username or email"
              aria-labelledby="identifier-label"
              value={identifier}
              onChangeText={setIdentifier}
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType="email-address"
              autoComplete="username"
              returnKeyType="next"
              editable={!login.isPending}
            />
            <FieldError message={fieldErrors.identifier} />
          </View>

          <View className="gap-2">
            <View className="flex-row items-center justify-between">
              <Label nativeID="password-label">Password</Label>
              <Link href="/forgot" asChild>
                <Text
                  testID="login-forgot"
                  accessibilityRole="link"
                  accessibilityLabel="Forgot password"
                  className="text-sm font-medium text-primary">
                  Forgot?
                </Text>
              </Link>
            </View>
            <PasswordInput
              testID="login-password"
              accessibilityLabel="Password"
              aria-labelledby="password-label"
              value={password}
              onChangeText={setPassword}
              autoComplete="current-password"
              returnKeyType="go"
              onSubmitEditing={submit}
              editable={!login.isPending}
            />
            <FieldError message={fieldErrors.password} />
          </View>
        </View>

        <View className="gap-3">
          <Button
            size="lg"
            testID="login-submit"
            accessibilityRole="button"
            accessibilityLabel="Sign in"
            onPress={submit}
            disabled={login.isPending || !identifier.trim() || !password}>
            <Text>{login.isPending ? 'Signing in…' : 'Sign in'}</Text>
          </Button>

          {topError ? (
            <Text testID="login-top-error" className="text-center text-sm text-destructive">
              {topError}
            </Text>
          ) : null}

          <View className="flex-row items-center justify-center gap-1.5 pt-1">
            <Text variant="muted" className="text-sm">
              No account?
            </Text>
            <Pressable
              testID="login-go-register"
              accessibilityRole="button"
              accessibilityLabel="Create account"
              onPress={goToRegister}
              className="py-1">
              <Text className="text-sm font-semibold text-primary">Create one</Text>
            </Pressable>
          </View>
        </View>
      </View>
    </AuthScreenLayout>
  );
}
