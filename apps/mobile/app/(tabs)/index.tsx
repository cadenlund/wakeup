// Chats tab placeholder. Phase 5.1 fills in the conversations
// list (sorted by last_message_at, pinned-first) and the new-
// conversation "+" button; 5.4 adds pull-to-refresh; 5.6 adds
// pin/mute long-press menus. Until then the tab routes here so
// the navigator has somewhere to land at `/`.
import { MessageCircle } from 'lucide-react-native';
import { View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function ChatsScreen() {
  const mutedFg = useThemeColor('muted-foreground');
  return (
    <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
      <MessageCircle size={48} color={mutedFg} />
      <Text variant="h3" className="text-center">
        Chats
      </Text>
      <Text variant="muted" className="text-center">
        The conversations list lands in Phase 5.1.
      </Text>
    </View>
  );
}
