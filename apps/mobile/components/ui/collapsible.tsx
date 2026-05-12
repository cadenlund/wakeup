// Settings-section accordion. A card with a tappable header (optional
// leading icon + title + chevron) that shows / hides its body. Used to
// group the profile/settings screen into expandable sections so the
// page isn't one long scroll. Uncontrolled with an optional
// `defaultOpen`; the body is conditionally rendered (no measured
// height animation — the card chrome carries the affordance).
import { ChevronDown, ChevronRight } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { cn } from '@/lib/utils';

type IconComponent = React.ComponentType<{ size?: number; color?: string }>;

type Props = {
  title: string;
  Icon?: IconComponent;
  defaultOpen?: boolean;
  children: React.ReactNode;
  testID?: string;
};

export function Collapsible({ title, Icon, defaultOpen = false, children, testID }: Props) {
  const [open, setOpen] = React.useState(defaultOpen);
  const mutedFg = useThemeColor('muted-foreground');
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <View className="overflow-hidden rounded-2xl border border-border bg-card">
      <Pressable
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
        accessibilityLabel={title}
        testID={testID}
        onPress={() => setOpen((o) => !o)}
        className={cn(
          'flex-row items-center gap-3 px-4 py-3.5 active:bg-muted',
          open && 'border-b border-border'
        )}>
        {Icon ? <Icon size={18} color={mutedFg} /> : null}
        <Text className="flex-1 text-base font-medium">{title}</Text>
        <Chevron size={18} color={mutedFg} />
      </Pressable>
      {open ? <View className="gap-4 px-4 py-4">{children}</View> : null}
    </View>
  );
}
