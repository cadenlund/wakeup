import { StatusBar } from 'expo-status-bar';
import { Platform, View } from 'react-native';

import { Text } from '@/components/ui/text';

export default function Modal() {
  return (
    <View className="flex-1 items-center justify-center gap-2 bg-background p-6">
      <Text variant="h2">Modal</Text>
      <Text variant="muted">Placeholder modal route.</Text>
      <StatusBar style={Platform.OS === 'ios' ? 'light' : 'auto'} />
    </View>
  );
}
