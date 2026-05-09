// Web-only sidebar layout. Metro picks `_layout.web.tsx` over the
// bare `_layout.tsx` on the web bundle automatically; native still
// gets the bottom-tab navigator from `_layout.tsx`. Bottom tabs
// feel right on a phone, a collapsible sidebar feels right in a
// browser window with horizontal real estate.
//
// Animation: width + label opacity ride a reanimated shared value
// so the toggle is one smooth 220ms tween instead of a jump cut.
// Toggle handle is a floating chevron pinned to the right edge of
// the sidebar — it stays in the same screen position as the
// sidebar grows/shrinks, so users don't have to chase it.
//
// Routing: `<Slot>` renders whichever sibling tab file matches the
// current pathname (index → /, friends → /friends, profile →
// /profile). The sidebar is just the chrome around it.
import { Link, Slot, usePathname, useRouter } from 'expo-router';
import {
  ChevronLeft,
  ChevronRight,
  LogOut,
  MessageCircle,
  Moon,
  User,
  Users,
  type LucideIcon,
} from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View, type ViewStyle } from 'react-native';
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withTiming,
  type AnimatedStyle,
} from 'react-native-reanimated';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const COLLAPSED_KEY = 'wakeup:sidebar:collapsed';
const COLLAPSED_WIDTH = 68;
const EXPANDED_WIDTH = 232;
const ANIM_DURATION = 220;

type NavItem = { href: '/' | '/friends' | '/profile'; label: string; icon: LucideIcon };

const NAV_ITEMS: NavItem[] = [
  { href: '/', label: 'Chats', icon: MessageCircle },
  { href: '/friends', label: 'Friends', icon: Users },
  { href: '/profile', label: 'Profile', icon: User },
];

// Some browser modes (Safari private, embedded iframes with strict
// referrer policy) throw on the bare `window.localStorage` property
// access — not just on getItem/setItem. The whole read+write needs
// to be in a try/catch, including the property access itself.
function safeLocalGet(key: string): string | null {
  try {
    if (typeof window === 'undefined') return null;
    return window.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}
function safeLocalSet(key: string, value: string): void {
  try {
    if (typeof window === 'undefined') return;
    window.localStorage?.setItem(key, value);
  } catch {
    // Quota / privacy mode — non-critical, just lose the preference.
  }
}

export default function TabsWebLayout() {
  const pathname = usePathname();
  const primary = useThemeColor('primary');
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');

  // Lazy initial read keeps the localStorage hit on mount only.
  const [collapsed, setCollapsed] = React.useState<boolean>(
    () => safeLocalGet(COLLAPSED_KEY) === 'true'
  );

  // Reanimated shared values drive the smooth width / label-opacity
  // transitions. Updating them inside the toggle handler queues the
  // tween directly without a re-render-then-animate round-trip.
  const widthSv = useSharedValue(collapsed ? COLLAPSED_WIDTH : EXPANDED_WIDTH);
  const labelOpacitySv = useSharedValue(collapsed ? 0 : 1);

  const sidebarStyle = useAnimatedStyle(() => ({ width: widthSv.value }));
  const labelStyle = useAnimatedStyle(() => ({
    opacity: labelOpacitySv.value,
    // pointerEvents handled separately via collapsed state — opacity
    // alone leaves the labels in the layout tree, which is fine but
    // they shouldn't be tab-stops while invisible.
  }));

  const toggle = React.useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      widthSv.value = withTiming(next ? COLLAPSED_WIDTH : EXPANDED_WIDTH, {
        duration: ANIM_DURATION,
      });
      labelOpacitySv.value = withTiming(next ? 0 : 1, {
        duration: next ? 140 : 200,
      });
      safeLocalSet(COLLAPSED_KEY, String(next));
      return next;
    });
  }, [widthSv, labelOpacitySv]);

  // Same logout logic as the native tab bar header. Real settings/
  // logout UX lands in Phase 11.6; this is the temporary surface.
  const qc = useQueryClient();
  const router = useRouter();
  const logout = usePostV1AuthLogout({
    mutation: {
      onSuccess: () => signedOut(qc, router),
      onError: (err) => {
        if (err instanceof APIError && err.status === 401) {
          void signedOut(qc, router);
        }
      },
    },
  });

  return (
    <View className="flex-1 flex-row bg-background">
      <Animated.View style={sidebarStyle} className="relative border-r border-border bg-card py-4">
        {/* Brand mark — small Moon + wordmark when expanded, just
            the Moon when collapsed. Mirrors the auth-screen-layout
            so the visual identity carries between unauth and
            authed shells. */}
        <View className="mb-4 flex-row items-center gap-2 px-4">
          <Moon size={20} color={primary} />
          <Animated.View style={labelStyle} pointerEvents={collapsed ? 'none' : 'auto'}>
            <Text className="text-base font-semibold tracking-tight">Wakeup</Text>
          </Animated.View>
        </View>

        <View className="gap-1 px-3">
          {NAV_ITEMS.map((item) => (
            <SidebarLink
              key={item.href}
              item={item}
              active={isActive(pathname, item.href)}
              labelStyle={labelStyle}
              labelInteractive={!collapsed}
              activeColor={primary}
              inactiveColor={mutedFg}
            />
          ))}
        </View>

        <View className="flex-1" />

        <View className="border-t border-border px-3 pt-3">
          <SidebarButton
            icon={LogOut}
            label={logout.isPending ? 'Logging out…' : 'Log out'}
            onPress={() => logout.mutate()}
            disabled={logout.isPending}
            color={fg}
            labelStyle={labelStyle}
            labelInteractive={!collapsed}
            testID="header-logout"
            accessibilityLabel="Log out"
          />
        </View>

        {/* Floating handle — a circular button straddling the right
            edge of the sidebar so the user has a clear affordance to
            grab. Stays in roughly the same screen position whether
            the bar is open or closed. */}
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          testID="sidebar-toggle"
          onPress={toggle}
          hitSlop={6}
          className="absolute right-[-12px] top-6 h-7 w-7 items-center justify-center rounded-full border border-border bg-card shadow-sm active:bg-muted">
          {collapsed ? (
            <ChevronRight size={14} color={mutedFg} />
          ) : (
            <ChevronLeft size={14} color={mutedFg} />
          )}
        </Pressable>
      </Animated.View>

      <View className="flex-1">
        <Slot />
      </View>
    </View>
  );
}

function isActive(pathname: string, href: string): boolean {
  if (href === '/') return pathname === '/' || pathname === '';
  return pathname === href;
}

type LabelStyle = AnimatedStyle<ViewStyle>;

function SidebarLink({
  item,
  active,
  labelStyle,
  labelInteractive,
  activeColor,
  inactiveColor,
}: {
  item: NavItem;
  active: boolean;
  labelStyle: LabelStyle;
  labelInteractive: boolean;
  activeColor: string;
  inactiveColor: string;
}) {
  const Icon = item.icon;
  const tint = active ? activeColor : inactiveColor;
  return (
    <Link href={item.href} accessibilityRole="link" accessibilityLabel={item.label} asChild>
      <Pressable
        className={`relative flex-row items-center gap-3 rounded-lg px-3 py-2.5 transition-colors active:bg-muted ${
          active ? 'bg-muted' : ''
        }`}>
        {active ? (
          <View
            pointerEvents="none"
            className="absolute left-0 top-2 h-[calc(100%-16px)] w-[3px] rounded-r-full bg-primary"
          />
        ) : null}
        <Icon size={18} color={tint} />
        <Animated.View style={labelStyle} pointerEvents={labelInteractive ? 'auto' : 'none'}>
          <Text
            numberOfLines={1}
            className={`text-sm ${active ? 'font-semibold text-primary' : 'text-muted-foreground'}`}>
            {item.label}
          </Text>
        </Animated.View>
      </Pressable>
    </Link>
  );
}

function SidebarButton({
  icon: Icon,
  label,
  onPress,
  disabled,
  color,
  labelStyle,
  labelInteractive,
  testID,
  accessibilityLabel,
}: {
  icon: LucideIcon;
  label: string;
  onPress: () => void;
  disabled?: boolean;
  color: string;
  labelStyle: LabelStyle;
  labelInteractive: boolean;
  testID?: string;
  accessibilityLabel: string;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={accessibilityLabel}
      testID={testID}
      onPress={onPress}
      disabled={disabled}
      className={`flex-row items-center gap-3 rounded-lg px-3 py-2.5 transition-colors active:bg-muted ${
        disabled ? 'opacity-50' : ''
      }`}>
      <Icon size={18} color={color} />
      <Animated.View style={labelStyle} pointerEvents={labelInteractive ? 'auto' : 'none'}>
        <Text numberOfLines={1} className="text-sm font-medium">
          {label}
        </Text>
      </Animated.View>
    </Pressable>
  );
}
