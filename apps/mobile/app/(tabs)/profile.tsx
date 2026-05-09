// Profile tab placeholder. Phase 11.6 fills in the "me" card
// (avatar, display name, status emoji) + entry to settings; Phase
// 11.6c adds the long-press <PresencePicker> bottom-sheet. Until
// then the screen just shows the placeholder so the tab navigator
// has somewhere to route.
import { User } from 'lucide-react-native';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function ProfileScreen() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
      <User size={48} color={mutedFg} />
      <Text variant="h3" className="text-center">
        Profile
      </Text>
      <Text variant="muted" className="text-center">
        Settings, avatar, and presence land in Phase 11.6.
      </Text>
    </View>
  );
}
