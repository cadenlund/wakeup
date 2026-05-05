// Persistent thin banner above the route stack when the device is
// offline (per spec §4.10). Hidden when online so it has zero pixel
// cost in the happy path. Mounted once at root.
import { View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useNetworkState } from '@/lib/network/state';

export function NetworkBanner() {
  const { online } = useNetworkState();

  if (online) return null;

  return (
    <View className="bg-destructive px-4 py-2">
      <Text className="text-center text-sm font-medium text-white">
        You&apos;re offline — messages will send when you&apos;re back.
      </Text>
    </View>
  );
}
