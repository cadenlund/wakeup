// Phase 1.4 smoke gallery — verifies the react-native-reusables
// foundation is wired up against the per-scheme theme tokens
// (WAKEUPEXPO.md §3.1). Renders a Button (3 variants), a Card, and
// the SignInForm auth block. The actual product screen (conversation
// list per §5.2) replaces this in a later phase.
import { ScrollView, View } from "react-native";

import { Button } from "@/components/themed/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/themed/card";
import { Text } from "@/components/themed/text";
import { SignInForm } from "@/components/sign-in-form";

export default function TabOneScreen() {
  return (
    <ScrollView
      className="bg-background flex-1"
      contentContainerClassName="px-4 py-6 gap-6"
      keyboardShouldPersistTaps="handled"
    >
      <View className="gap-2">
        <Text className="text-foreground text-2xl font-semibold">RNR foundation gallery</Text>
        <Text className="text-muted-foreground text-sm">
          Phase 1.4 smoke test. Tokens are scheme-aware — change schemes in the theme store and
          everything below should re-skin in place.
        </Text>
      </View>

      <View className="gap-3">
        <Text className="text-foreground text-sm font-medium uppercase tracking-wide">Buttons</Text>
        <View className="gap-3">
          <Button>
            <Text>Default</Text>
          </Button>
          <Button variant="outline">
            <Text>Outline</Text>
          </Button>
          <Button variant="destructive">
            <Text>Destructive</Text>
          </Button>
        </View>
      </View>

      <View className="gap-3">
        <Text className="text-foreground text-sm font-medium uppercase tracking-wide">Card</Text>
        <Card>
          <CardHeader>
            <CardTitle>Card primitive</CardTitle>
            <CardDescription>
              Background, border, and text colours come from the active scheme.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Text>
              Card content uses the foreground token via TextClassContext, so swatch changes
              propagate automatically.
            </Text>
          </CardContent>
        </Card>
      </View>

      <View className="gap-3">
        <Text className="text-foreground text-sm font-medium uppercase tracking-wide">
          Sign-in form
        </Text>
        <SignInForm />
      </View>
    </ScrollView>
  );
}
