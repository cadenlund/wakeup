// Friends tab placeholder. Phase 4.2 fills in the accepted-friends
// list + incoming/outgoing requests via `<FriendRow>` and
// `<PresenceDot>`. Phase 4.3 wires the add-friend search; 4.4 wires
// accept/decline/unfriend/block; 4.5 adds pull-to-refresh.
import { Users } from 'lucide-react-native';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function FriendsScreen() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
      <Users size={48} color={mutedFg} />
      <Text variant="h3" className="text-center">
        Friends
      </Text>
      <Text variant="muted" className="text-center">
        The friends list and search land in Phase 4.2.
      </Text>
    </View>
  );
}
