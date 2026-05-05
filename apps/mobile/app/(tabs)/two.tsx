// Tab Two — placeholder. Phase 5.1 will replace this with the
// conversations route.
import { Stack } from 'expo-router';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';

export default function TabTwo() {
  return (
    <>
      <Stack.Screen options={{ title: 'Tab Two' }} />
      <View className="flex-1 items-center justify-center bg-background">
        <Text variant="h2">Tab Two</Text>
        <Text variant="muted">Replaced by conversations in Phase 5.1.</Text>
      </View>
    </>
  );
}
