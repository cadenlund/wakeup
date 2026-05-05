// `Input` + eye/eye-off toggle. Matches the existing input chrome so
// the toggle button doesn't visually break the field — the icon is
// absolutely positioned over a right padding wedge inside the input.
import { Eye, EyeOff } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { Input } from '@/components/ui/input';
import { useThemeColor } from '@/lib/theme/use-theme-color';

type Props = Omit<React.ComponentProps<typeof Input>, 'secureTextEntry'>;

function PasswordInput({ className, ...props }: Props) {
  const [visible, setVisible] = React.useState(false);
  const iconColor = useThemeColor('muted-foreground');
  // Mirror the input's editable state on the toggle so a pending
  // submission (editable={false}) doesn't let the user reveal the
  // entry mid-flight. (CR on PR #116.)
  const toggleDisabled = props.editable === false;

  return (
    <View className="relative">
      <Input className={`pr-12 ${className ?? ''}`} secureTextEntry={!visible} {...props} />
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={visible ? 'Hide password' : 'Show password'}
        accessibilityState={{ disabled: toggleDisabled }}
        disabled={toggleDisabled}
        onPress={() => setVisible((v) => !v)}
        className="absolute bottom-0 right-0 top-0 items-center justify-center px-3">
        {visible ? <EyeOff size={20} color={iconColor} /> : <Eye size={20} color={iconColor} />}
      </Pressable>
    </View>
  );
}

export { PasswordInput };
