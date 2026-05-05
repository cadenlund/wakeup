// Single empty-state primitive (per spec §5.2). Every "no items yet"
// surface — empty conversations list, empty friend list, no search
// results — composes this rather than rolling its own Text + Button
// pair, so the spacing, typography, and CTA shape stay uniform.
//
// `icon` is a ReactNode so callers wire up their own Lucide icon with
// the size/className they want (the wrapper just centers it). `cta`
// is optional: if absent, the empty state is purely informational.
import * as React from 'react';
import { View } from 'react-native';

import { Button } from '@/components/ui/button';
import { Text } from '@/components/ui/text';
import { cn } from '@/lib/utils';

type EmptyStateProps = {
  icon?: React.ReactNode;
  title: string;
  subtitle?: string;
  cta?: {
    label: string;
    onPress: () => void;
  };
  className?: string;
};

function EmptyState({ icon, title, subtitle, cta, className }: EmptyStateProps) {
  return (
    <View className={cn('flex-1 items-center justify-center gap-3 px-8 py-12', className)}>
      {icon ? (
        <View className="mb-2 items-center justify-center text-muted-foreground">{icon}</View>
      ) : null}
      <Text variant="h4" className="text-center">
        {title}
      </Text>
      {subtitle ? (
        <Text variant="muted" className="text-center">
          {subtitle}
        </Text>
      ) : null}
      {cta ? (
        <Button onPress={cta.onPress} className="mt-2">
          <Text>{cta.label}</Text>
        </Button>
      ) : null}
    </View>
  );
}

export { EmptyState };
export type { EmptyStateProps };
