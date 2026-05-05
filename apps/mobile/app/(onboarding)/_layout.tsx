// `(onboarding)` is the first-launch route group — single carousel
// screen, no headers, no tab bar. Once the user finishes (or skips)
// the carousel, AsyncStorage `onboarding:complete = true` is set
// and the AuthGate routes them to /login from cold starts going
// forward.
import { Stack } from 'expo-router';

export default function OnboardingLayout() {
  return <Stack screenOptions={{ headerShown: false, animation: 'fade' }} />;
}
