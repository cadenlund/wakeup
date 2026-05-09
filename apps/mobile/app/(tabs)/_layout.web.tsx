// Web-only sidebar layout. Metro picks `_layout.web.tsx` over the
// bare `_layout.tsx` on the web bundle automatically; native still
// gets the bottom-tab navigator from `_layout.tsx`.
//
// Animation: width tweens via reanimated; labels fade via a shared
// opacity value. Icons stay pinned at a fixed left offset so they
// don't slide / vanish during the tween — only the labels (which
// are clipped by overflow-hidden on the sidebar) come and go.
//
// Toggle handle lives inside the header itself — no floating
// off-edge button to get clipped. When collapsed, the brand mark
// fades out and the toggle is the only thing left in the header,
// sitting roughly centered in the now-narrow column.
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
import { AccessibilityInfo, Pressable, View, type ViewStyle } from 'react-native';
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
const COLLAPSED_WIDTH = 60;
const EXPANDED_WIDTH = 240;
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

  // Honor the OS reduced-motion setting — when on, we skip the tween
  // and snap to the new width/opacity. Same pattern as the onboarding
  // carousel.
  const [reduceMotion, setReduceMotion] = React.useState(false);
  React.useEffect(() => {
    let mounted = true;
    AccessibilityInfo.isReduceMotionEnabled().then((v) => {
      if (mounted) setReduceMotion(v);
    });
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', (v) =>
      setReduceMotion(v)
    );
    return () => {
      mounted = false;
      sub.remove();
    };
  }, []);

  const widthSv = useSharedValue(collapsed ? COLLAPSED_WIDTH : EXPANDED_WIDTH);
  const labelOpacitySv = useSharedValue(collapsed ? 0 : 1);

  const sidebarStyle = useAnimatedStyle(() => ({ width: widthSv.value }));
  const labelStyle = useAnimatedStyle(() => ({ opacity: labelOpacitySv.value }));

  const toggle = React.useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      const targetWidth = next ? COLLAPSED_WIDTH : EXPANDED_WIDTH;
      const targetOpacity = next ? 0 : 1;
      if (reduceMotion) {
        widthSv.value = targetWidth;
        labelOpacitySv.value = targetOpacity;
      } else {
        widthSv.value = withTiming(targetWidth, { duration: ANIM_DURATION });
        // Collapse: fade labels out faster than the width shrinks so the
        // text isn't squashed into the icons mid-tween. Expand: fade in
        // a touch slower so the labels appear once there's space.
        labelOpacitySv.value = withTiming(targetOpacity, {
          duration: next ? 120 : 180,
        });
      }
      safeLocalSet(COLLAPSED_KEY, String(next));
      return next;
    });
  }, [widthSv, labelOpacitySv, reduceMotion]);

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
      <Animated.View
        style={sidebarStyle}
        className="overflow-hidden border-r border-border bg-card">
        {/* Header — brand on left fades with the labels; toggle is
            pinned to the right and stays opaque the whole time. When
            collapsed the brand goes to opacity 0 and only the toggle
            is visible, naturally near-centered as the column narrows. */}
        <View className="h-12 flex-row items-center border-b border-border">
          <Animated.View
            style={labelStyle}
            className="ml-[14px] flex-1 flex-row items-center gap-2"
            pointerEvents="none">
            <Moon size={18} color={primary} />
            <Text numberOfLines={1} className="text-base font-semibold tracking-tight">
              Wakeup
            </Text>
          </Animated.View>
          <Pressable
            onPress={toggle}
            accessibilityRole="button"
            accessibilityLabel={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            testID="sidebar-toggle"
            hitSlop={6}
            className="mr-[14px] h-8 w-8 items-center justify-center rounded-md active:bg-muted">
            {collapsed ? (
              <ChevronRight size={16} color={mutedFg} />
            ) : (
              <ChevronLeft size={16} color={mutedFg} />
            )}
          </Pressable>
        </View>

        <View className="gap-1 py-3">
          {NAV_ITEMS.map((item) => (
            <SidebarRow
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

        <View className="border-t border-border py-3">
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

// Icon padding-left math: each row sits inside `mx-2` (8px each
// side), so the inner row width is sidebarWidth - 16. Collapsed
// inner width = 60 - 16 = 44; icon is 18 wide, so pl-[13px] puts
// the icon center at 13+9 = 22 = 44/2 — exactly centered.
const ROW_PL = 13;

function SidebarRow({
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
        style={{ paddingLeft: ROW_PL }}
        className={`relative mx-2 h-10 flex-row items-center rounded-lg active:bg-muted ${
          active ? 'bg-muted' : ''
        }`}>
        {active ? (
          <View
            pointerEvents="none"
            className="absolute bottom-2 left-0 top-2 w-[3px] rounded-r-full bg-primary"
          />
        ) : null}
        <Icon size={18} color={tint} />
        <Animated.View
          style={labelStyle}
          className="ml-3 flex-shrink"
          pointerEvents={labelInteractive ? 'auto' : 'none'}>
          <Text
            numberOfLines={1}
            className={`text-sm ${
              active ? 'font-semibold text-primary' : 'text-muted-foreground'
            }`}>
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
      style={{ paddingLeft: ROW_PL }}
      className={`mx-2 h-10 flex-row items-center rounded-lg active:bg-muted ${
        disabled ? 'opacity-50' : ''
      }`}>
      <Icon size={18} color={color} />
      <Animated.View
        style={labelStyle}
        className="ml-3 flex-shrink"
        pointerEvents={labelInteractive ? 'auto' : 'none'}>
        <Text numberOfLines={1} className="text-sm font-medium">
          {label}
        </Text>
      </Animated.View>
    </Pressable>
  );
}
