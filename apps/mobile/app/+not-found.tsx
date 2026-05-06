// 404 fallback. Themed so it doesn't visually break against the rest
// of the app — the rn-reusables template default uses a hardcoded
// link colour (#2e78b7) and the bare RN <Text>, both of which read
// like a different app on dark schemes.
import { Link, Stack } from 'expo-router';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';

export default function NotFoundScreen() {
  return (
    <>
      <Stack.Screen options={{ title: 'Oops!' }} />
      <View className="flex-1 items-center justify-center gap-3 bg-background p-6">
        <Text variant="h2" className="text-center">
          This screen doesn&apos;t exist.
        </Text>
        <Link href="/" className="pt-2">
          <Text className="font-medium text-primary">Go to home screen</Text>
        </Link>
      </View>
    </>
  );
}
