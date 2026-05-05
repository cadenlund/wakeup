// `(auth)` is the unauthenticated route group — login, register,
// forgot, reset. No tab bar (the user isn't signed in yet so there's
// nothing to navigate between besides the auth screens themselves).
//
// The root layout's <AuthGate> redirects any 401 user here; once
// they sign in, the gate re-resolves and pushes them back to (tabs).
import { Stack } from 'expo-router';

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
