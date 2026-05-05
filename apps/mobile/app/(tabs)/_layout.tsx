import { Tabs } from 'expo-router';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export default function TabLayout() {
  const primary = useThemeColor('primary');

  return (
    <Tabs
      screenOptions={{
        tabBarActiveTintColor: primary,
      }}>
      <Tabs.Screen
        name="index"
        options={{
          title: 'Gallery',
        }}
      />
      <Tabs.Screen
        name="two"
        options={{
          title: 'Tab Two',
        }}
      />
    </Tabs>
  );
}
