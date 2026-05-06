// `(onboarding)` is the first-launch route group — single carousel
// screen, no headers, no tab bar. Once the user finishes the
// carousel, the backend sets `onboarded_at` and the next /me refetch
// flips the root layout's `Stack.Protected` guards: (onboarding)
// becomes hidden, (tabs) becomes accessible, and the user is
// redirected automatically.
import { Stack } from 'expo-router';

export default function OnboardingLayout() {
  return <Stack screenOptions={{ headerShown: false, animation: 'fade' }} />;
}
