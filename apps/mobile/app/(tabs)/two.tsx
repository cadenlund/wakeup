// Phase 1.4 gallery — temporary screen used to QR-verify that the
// 15-token shadcn palette + 10 schemes × 2 modes pipeline lands the
// right colors on a real device. Replaces the create-expo-app
// boilerplate at this route.
//
// This file is meant to be replaced by the real conversations tab
// (§5.1) when Phase 5 lands. Keeping it here as the reviewable
// surface for the operator's per-milestone gate (§12.5).
import { Stack } from "expo-router";
import * as React from "react";

import { ScrollView, View } from "@/lib/tw";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Text } from "@/components/ui/text";
import { SignInForm } from "@/components/sign-in-form";
import { SCHEMES, type SchemeOrSystem } from "@/lib/theme/schemes";
import { useThemeStore } from "@/lib/theme/store";

export default function GalleryScreen() {
  const selected = useThemeStore((s) => s.selected);
  const setScheme = useThemeStore((s) => s.setScheme);
  const mode = useThemeStore((s) => s.mode);

  const schemes: SchemeOrSystem[] = ["system", ...SCHEMES.map((s) => s.id)];
  const [switchOn, setSwitchOn] = React.useState(false);

  return (
    <ScrollView className="flex-1 bg-background" contentContainerClassName="px-4 py-6 gap-6">
      <Stack.Screen options={{ title: "Gallery" }} />

      <View className="gap-2">
        <Text variant="h2">Theme tokens</Text>
        <Text variant="muted">
          Active scheme: <Text className="text-foreground font-semibold">{selected}</Text> · mode:{" "}
          <Text className="text-foreground font-semibold">{mode}</Text>
        </Text>
        <View className="flex-row flex-wrap gap-2 pt-2">
          {schemes.map((s) => (
            <Button
              key={s}
              size="sm"
              variant={s === selected ? "default" : "outline"}
              onPress={() => {
                void setScheme(s);
              }}
            >
              <Text>{s}</Text>
            </Button>
          ))}
        </View>
        <Text variant="small" className="pt-1 text-muted-foreground">
          Light/dark mode follows OS Appearance — toggle Settings → Display & Brightness to flip.
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
              Card content uses the card-foreground token; the wrapping{" "}
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
          <Text>{switchOn ? "Notifications on" : "Notifications off"}</Text>
        </View>
      </View>

      <Separator />

      <View className="gap-3">
        <Text variant="h2">Auth block</Text>
        <SignInForm />
      </View>

      <View className="h-12" />
    </ScrollView>
  );
}
