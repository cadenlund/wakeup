// `(auth)` is the unauthenticated route group — login, register,
// forgot, reset. No tab bar (the user isn't signed in yet so there's
// nothing to navigate between besides the auth screens themselves).
//
// The root layout's <Stack.Protected> blocks redirect any unauthed
// user here; once they sign in, the (auth) guard flips and the user
// is redirected to (tabs) or (onboarding) automatically.
import { Stack } from 'expo-router';

// `login` is the canonical landing screen of the (auth) group. Without
// an explicit anchor, expo-router would pick the alphabetically-first
// route (forgot.tsx) when redirecting into the group, which is the
// wrong UX. Nested `unstable_settings` is safe — only the ROOT
// _layout.tsx version was problematic for deep links.
export const unstable_settings = {
  initialRouteName: 'login',
};

export default function AuthLayout() {
  return (
    <Stack
      screenOptions={{
        headerShown: false,
        animation: 'slide_from_right',
      }}
    />
  );
}
