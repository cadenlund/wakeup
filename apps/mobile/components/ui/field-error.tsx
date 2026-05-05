// Renders a per-field validation message in destructive colour. No-
// ops when message is undefined so consumers can render it
// unconditionally and let the empty state collapse to nothing:
//
//   <Input ... />
//   <FieldError message={errors.username} />
import { Text } from '@/components/ui/text';

export function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return <Text className="text-xs text-destructive">{message}</Text>;
}
