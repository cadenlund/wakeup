// Reset-password screen (Phase 3.3). Reads the reset token from
// the deep-link URL params (`?token=…`) and accepts a new password
// + confirmation. Submit posts to /v1/auth/password-reset/confirm.
//
// On success: toast "Password reset" + replace to /login. The
// backend invalidates the token after use; if the user retries
// with the same token they'll get a generic 401.
import { useLocalSearchParams, useRouter } from 'expo-router';
import { CheckCircle2, Moon } from 'lucide-react-native';
import * as React from 'react';
import { Platform, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { AuthScreenLayout } from '@/components/auth-screen-layout';
import { Button } from '@/components/ui/button';
import { FieldError } from '@/components/ui/field-error';
import { Label } from '@/components/ui/label';
import { PasswordInput } from '@/components/ui/password-input';
import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import {
  usePostV1AuthPasswordResetConfirm,
  usePostV1AuthPasswordResetValidate,
} from '@/lib/api/hooks/auth/auth';
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

export default function ResetScreen() {
  const router = useRouter();
  const qc = useQueryClient();
  const primaryColor = useThemeColor('primary');
  const params = useLocalSearchParams<{ token?: string }>();
  const token = typeof params.token === 'string' ? params.token : '';

  const [password, setPassword] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [mismatchError, setMismatchError] = React.useState<string | undefined>();

  // Preflight: hit the validate endpoint on mount so a bad / used /
  // expired token bounces the user back to /login before they bother
  // typing a new password. Server-side this is idempotent (no DB
  // writes) — it's modelled as a POST only because it carries the
  // token in the body rather than the URL. The mutation framing
  // here is therefore a thin wrapper, NOT a typical write.
  //
  // Manual retry config because react-query's mutation defaults
  // (retry: 0 in lib/api/query-client.ts) would otherwise drop the
  // user off a valid token on a single transient 5xx / network
  // blip. We only redirect on a definitive auth error (401 codes);
  // anything else — including unmapped 5xx — falls through here so
  // the global mutationCache toast gets the chance to retry.
  const validate = usePostV1AuthPasswordResetValidate({
    mutation: {
      onError: (err) => {
        if (
          err instanceof APIError &&
          (err.body?.code === 'UNAUTHORIZED' || err.body?.code === 'RESET_TOKEN_EXPIRED')
        ) {
          haptics.warning();
          router.replace('/login');
        }
      },
    },
  });
  const validateMutate = validate.mutate;
  const tokenValid = validate.isSuccess;
  React.useEffect(() => {
    if (!token) return;
    // Mutate reference is stable per TanStack — re-firing on token
    // change only happens via fresh deep-link mount, which is exactly
    // when we want the preflight to re-run.
    validateMutate({ data: { token } });
  }, [token, validateMutate]);

  const confirmReset = usePostV1AuthPasswordResetConfirm({
    mutation: {
      onSuccess: async () => {
        haptics.success();
        // On web we hard-navigate to /login. Reason: the React Query
        // cache, the protected-stack guards, and the auth-gate
        // observer all carry stale-from-before-reset state, and the
        // sequence of (cancel → setQueryData(null) → router.replace
        // → user signs in → setQueryData(user) → invalidate) hits
        // enough subtle React Query / Stack.Protected races that the
        // *next* sign-in's redirect to (tabs) would silently drop.
        // A full navigation wipes the in-memory state cleanly so the
        // post-reset sign-in starts from a known-good baseline.
        //
        // The success toast is queued via sessionStorage so it
        // survives the page reload — ToastRoot drains the queue on
        // mount on the destination /login page.
        //
        // Native still uses signedOut (cache clear + router.replace)
        // because there's no full-reload equivalent and the cold-
        // start race that motivates this on web doesn't apply.
        if (Platform.OS === 'web') {
          toast.queueForNextMount('success', 'Password reset', 'Sign in with your new password.');
          window.location.assign('/login');
          return;
        }
        toast.success('Password reset', 'Sign in with your new password.');
        await signedOut(qc, router);
      },
      onError: (err) => {
        // Expired tokens are dead — no useful retry on this screen.
        // Route the user back to /login. The global mutation toast
        // (lib/api/query-client.ts) shows the backend's "Reset link
        // has expired…" message; we add a haptic + reroute on top.
        // Branches off the stable error code so the UX doesn't break
        // the next time someone tweaks the message copy. Other 4xx
        // (bad token, used) and network errors fall through to the
        // inline `topError` text below.
        if (err instanceof APIError && err.body?.code === 'RESET_TOKEN_EXPIRED') {
          haptics.warning();
          router.replace('/login');
        }
      },
    },
  });
  const fieldErrors = useFieldErrors(confirmReset.error);
  const topError = useTopLevelError(confirmReset.error);

  const submit = () => {
    if (!token || password.length < 8) {
      haptics.warning();
      return;
    }
    if (password !== confirm) {
      haptics.warning();
      setMismatchError('Passwords do not match.');
      return;
    }
    setMismatchError(undefined);
    confirmReset.mutate({ data: { token, new_password: password } });
  };

  // Missing/empty token = the user opened /reset without coming
  // from the email link. Surface a clear error and route them to
  // login since they have no actionable form to fill.
  if (!token) {
    return (
      <AuthScreenLayout>
        <View className="gap-8">
          <View className="flex-row items-center gap-2 lg:hidden">
            <Moon size={22} color={primaryColor} />
            <Text className="text-lg font-semibold tracking-tight">Wakeup</Text>
          </View>
          <View className="gap-2">
            <Text variant="h1" className="text-left text-4xl">
              Link not valid
            </Text>
            <Text variant="muted" className="text-base">
              The reset link is missing a token. Request a new one from the sign-in screen.
            </Text>
          </View>
          <Button
            size="lg"
            testID="reset-go-login"
            accessibilityRole="button"
            accessibilityLabel="Back to sign in"
            onPress={() => router.replace('/login')}>
            <Text>Back to sign in</Text>
          </Button>
        </View>
      </AuthScreenLayout>
    );
  }

  return (
    <AuthScreenLayout>
      <View className="gap-8">
        <View className="flex-row items-center gap-2 lg:hidden">
          <Moon size={22} color={primaryColor} />
          <Text className="text-lg font-semibold tracking-tight">Wakeup</Text>
        </View>

        <View className="items-center gap-3 lg:items-start">
          <View className="rounded-2xl bg-primary/10 p-3">
            <CheckCircle2 size={28} color={primaryColor} />
          </View>
          <View className="gap-2">
            <Text variant="h1" className="text-left text-4xl">
              Set a new password
            </Text>
            <Text variant="muted" className="text-base">
              Pick something at least 8 characters long.
            </Text>
          </View>
        </View>

        <View className="gap-5">
          <View className="gap-2">
            <Label nativeID="new-password-label">New password</Label>
            <PasswordInput
              testID="reset-new-password"
              accessibilityLabel="New password"
              aria-labelledby="new-password-label"
              value={password}
              onChangeText={setPassword}
              autoComplete="new-password"
              returnKeyType="next"
              editable={!confirmReset.isPending}
            />
            {fieldErrors.new_password ? (
              <FieldError message={fieldErrors.new_password} />
            ) : (
              <Text variant="muted" className="text-xs">
                At least 8 characters.
              </Text>
            )}
          </View>

          <View className="gap-2">
            <Label nativeID="confirm-password-label">Confirm password</Label>
            <PasswordInput
              testID="reset-confirm-password"
              accessibilityLabel="Confirm password"
              aria-labelledby="confirm-password-label"
              value={confirm}
              onChangeText={(v) => {
                setConfirm(v);
                if (mismatchError) setMismatchError(undefined);
              }}
              autoComplete="new-password"
              returnKeyType="go"
              onSubmitEditing={submit}
              editable={!confirmReset.isPending}
            />
            <FieldError message={mismatchError} />
          </View>
        </View>

        <View className="gap-3">
          <Button
            size="lg"
            testID="reset-submit"
            accessibilityRole="button"
            accessibilityLabel="Set new password"
            onPress={submit}
            disabled={
              !tokenValid ||
              validate.isPending ||
              confirmReset.isPending ||
              password.length < 8 ||
              !confirm
            }>
            <Text>
              {validate.isPending
                ? 'Checking link…'
                : confirmReset.isPending
                  ? 'Saving…'
                  : 'Set new password'}
            </Text>
          </Button>

          {topError ? (
            <Text testID="reset-top-error" className="text-center text-sm text-destructive">
              {topError}
            </Text>
          ) : null}
        </View>
      </View>
    </AuthScreenLayout>
  );
}
