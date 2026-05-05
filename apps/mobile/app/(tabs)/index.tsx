// Phase 1.4 gallery — temporary screen used to verify that the
// shadcn-aligned 15-token palette + 10 sleep-cycle schemes ×
// (light, dark) lands the right colors on a real device.
//
// This file is meant to be replaced by the real conversations tab
// (§5.1) when Phase 5 lands. Keeping it here as the reviewable
// surface for the operator's per-milestone gate (§12.5).
import { Stack } from 'expo-router';
import { Inbox } from 'lucide-react-native';
import * as React from 'react';
import { ScrollView, View } from 'react-native';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { EmptyState } from '@/components/ui/empty-state';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Text } from '@/components/ui/text';
import { SCHEMES, type SchemeOrSystem } from '@/lib/theme/schemes';
import { useThemeStore, type ModePreference } from '@/lib/theme/store';
import { haptics } from '@/lib/haptics';
import { toast } from '@/lib/toast';

const MODE_PREFERENCES: ModePreference[] = ['light', 'dark', 'system'];

export default function GalleryScreen() {
  const selected = useThemeStore((s) => s.selected);
  const setScheme = useThemeStore((s) => s.setScheme);
  const mode = useThemeStore((s) => s.mode);
  const modePreference = useThemeStore((s) => s.modePreference);
  const setModePreference = useThemeStore((s) => s.setModePreference);

  const schemes: SchemeOrSystem[] = ['system', ...SCHEMES.map((s) => s.id)];
  const [switchOn, setSwitchOn] = React.useState(false);

  return (
    <ScrollView className="flex-1 bg-background" contentContainerClassName="px-4 py-6 gap-6">
      <Stack.Screen options={{ title: 'Gallery' }} />

      <View className="gap-2">
        <Text variant="h2">Theme tokens</Text>
        <Text variant="muted">
          Active scheme: <Text className="font-semibold text-foreground">{selected}</Text> · mode:{' '}
          <Text className="font-semibold text-foreground">{mode}</Text> · pref:{' '}
          <Text className="font-semibold text-foreground">{modePreference}</Text>
        </Text>
        <View className="flex-row flex-wrap gap-2 pt-2">
          {schemes.map((s) => (
            <Button
              key={s}
              size="sm"
              variant={s === selected ? 'default' : 'outline'}
              onPress={() => {
                void setScheme(s);
              }}>
              <Text>{s}</Text>
            </Button>
          ))}
        </View>
        <View className="flex-row gap-2 pt-2">
          {MODE_PREFERENCES.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={p === modePreference ? 'default' : 'outline'}
              onPress={() => {
                void setModePreference(p);
              }}>
              <Text>{p}</Text>
            </Button>
          ))}
        </View>
        <Text variant="small" className="pt-1 text-muted-foreground">
          Mode pref &quot;system&quot; follows OS Appearance; &quot;light&quot; / &quot;dark&quot;
          override.
        </Text>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Buttons</Text>
        <View className="flex-row flex-wrap gap-2">
          <Button>
            <Text>Default</Text>
          </Button>
          <Button variant="secondary">
            <Text>Secondary</Text>
          </Button>
          <Button variant="destructive">
            <Text>Destructive</Text>
          </Button>
          <Button variant="outline">
            <Text>Outline</Text>
          </Button>
          <Button variant="ghost">
            <Text>Ghost</Text>
          </Button>
          <Button variant="link">
            <Text>Link</Text>
          </Button>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Badges</Text>
        <View className="flex-row flex-wrap gap-2">
          <Badge>
            <Text>Default</Text>
          </Badge>
          <Badge variant="secondary">
            <Text>Secondary</Text>
          </Badge>
          <Badge variant="destructive">
            <Text>Destructive</Text>
          </Badge>
          <Badge variant="outline">
            <Text>Outline</Text>
          </Badge>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Card</Text>
        <Card>
          <CardHeader>
            <CardTitle>Card title</CardTitle>
            <CardDescription>
              Card description rendering against the active scheme&apos;s muted foreground token.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Text>
              Card content uses the card-foreground token; the wrapping{' '}
              <Text className="font-semibold">Card</Text> uses card + border-border.
            </Text>
          </CardContent>
          <CardFooter className="gap-2">
            <Button size="sm" variant="outline">
              <Text>Cancel</Text>
            </Button>
            <Button size="sm">
              <Text>Save</Text>
            </Button>
          </CardFooter>
        </Card>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Inputs &amp; switches</Text>
        <View className="gap-2">
          <Label nativeID="email">Email</Label>
          <Input
            aria-labelledby="email"
            placeholder="caden@example.com"
            keyboardType="email-address"
            autoCapitalize="none"
          />
        </View>
        <View className="flex-row items-center gap-3 pt-2">
          <Switch checked={switchOn} onCheckedChange={setSwitchOn} />
          <Text>{switchOn ? 'Notifications on' : 'Notifications off'}</Text>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Toasts</Text>
        <View className="flex-row flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            onPress={() => toast.success('Saved', 'Your changes are live.')}>
            <Text>Success</Text>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onPress={() => toast.error('Failed to save', 'Try again in a moment.')}>
            <Text>Error</Text>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onPress={() => toast.info('Reconnecting…', 'Lost connection to server.')}>
            <Text>Info</Text>
          </Button>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Haptics</Text>
        <View className="flex-row flex-wrap gap-2">
          <Button size="sm" variant="outline" onPress={() => haptics.tap()}>
            <Text>tap</Text>
          </Button>
          <Button size="sm" variant="outline" onPress={() => haptics.success()}>
            <Text>success</Text>
          </Button>
          <Button size="sm" variant="outline" onPress={() => haptics.warning()}>
            <Text>warning</Text>
          </Button>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Skeleton</Text>
        <View className="gap-2">
          <Skeleton className="h-4 w-3/4" />
          <Skeleton className="h-4 w-1/2" />
          <View className="flex-row items-center gap-3 pt-2">
            <Skeleton className="h-12 w-12 rounded-full" />
            <View className="flex-1 gap-2">
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-3 w-1/3" />
            </View>
          </View>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Empty state</Text>
        <View className="overflow-hidden rounded-xl border border-border bg-card">
          <EmptyState
            icon={<Inbox size={40} color="hsl(var(--muted-foreground))" />}
            title="No conversations yet"
            subtitle="Start a chat with a friend to see it here."
            cta={{
              label: 'Find friends',
              onPress: () => {},
            }}
          />
        </View>
      </View>

      <View className="h-12" />
    </ScrollView>
  );
}
