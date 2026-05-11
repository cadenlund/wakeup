// Global-search entry point for the tabs / chats-list header.
// Visually distinct from the friends-tab inline search input (which
// is friend discovery — a different flow that filters + adds people
// in place). Shared so the chats list (which owns its own header now
// that it lives inside a Stack) and the friends/profile tabs render
// the same affordance.
import { useRouter } from 'expo-router';
import { Search } from 'lucide-react-native';
import { Pressable } from 'react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export function HeaderSearchButton() {
  const fg = useThemeColor('foreground');
  const router = useRouter();
  return (
    <Pressable
      onPress={() => router.push('/search')}
      accessibilityRole="button"
      accessibilityLabel="Search people, chats, messages"
      testID="header-search"
      hitSlop={8}
      className="ml-3 h-9 w-9 items-center justify-center rounded-full active:bg-muted">
      <Search size={18} color={fg} />
    </Pressable>
  );
}
