// Web-only sidebar layout. Metro picks `_layout.web.tsx` over the
// bare `_layout.tsx` on the web bundle automatically; native still
// gets the bottom-tab navigator from `_layout.tsx`.
//
// Layout pattern (Linear / Notion style): each row is split into a
// fixed-width icon column (width = COLLAPSED_WIDTH) and a label
// area that fills the rest. The sidebar's outer width tweens
// between COLLAPSED_WIDTH and EXPANDED_WIDTH while overflow-hidden
// clips the label area off-screen when collapsed. The icons live
// in their own column so they never shrink, slide, or vanish —
// they sit at the same screen position in both states. This
// matters specifically on web, where the CSS default of
// `flex-shrink: 1` would otherwise let the icon collapse alongside
// the label when its row overflowed; the icon column is
// `shrink-0` so yoga / flexbox can't squeeze it.
//
// Toggle handle lives inside the header itself, pinned to the
// right edge of the sidebar — no floating off-edge button. When
// collapsed, the brand fades to opacity 0 and the toggle is the
// only visible chrome, sitting near-centered as the column
// narrows.
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
import { STORAGE_KEYS } from '@/lib/storage-keys';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const COLLAPSED_WIDTH = 64;
const EXPANDED_WIDTH = 240;
const ANIM_DURATION = 220;
// Match the icon column to the collapsed sidebar width so the
// icon center sits at sidebar/2 in both states without ever
// moving horizontally.
const ICON_COL_WIDTH = COLLAPSED_WIDTH;

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
    () => safeLocalGet(STORAGE_KEYS.uiSidebarCollapsed) === 'true'
  );

  // Honor the OS reduced-motion setting — when on, snap rather
  // than tween. Same pattern as the onboarding carousel.
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
        // Collapse: fade labels out faster than the width shrinks
        // so the text doesn't get squashed mid-tween. Expand: fade
        // in slightly slower so labels appear once there's space.
        labelOpacitySv.value = withTiming(targetOpacity, {
          duration: next ? 120 : 180,
        });
      }
      safeLocalSet(STORAGE_KEYS.uiSidebarCollapsed, String(next));
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
        {/* HEADER. Brand sits in the icon column on the left;
            wordmark fills remaining space and fades. Toggle is
            pinned right with shrink-0 so it stays the same size at
            both ends of the animation. */}
        <View className="h-12 flex-row items-center border-b border-border">
          <View style={{ width: ICON_COL_WIDTH }} className="shrink-0 items-center justify-center">
            <Moon size={20} color={primary} />
          </View>
          <Animated.View
            style={labelStyle}
            className="flex-1 shrink overflow-hidden"
            pointerEvents="none">
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
            className="mr-3 h-8 w-8 shrink-0 items-center justify-center rounded-md active:bg-muted">
            {collapsed ? (
              <ChevronRight size={16} color={mutedFg} />
            ) : (
              <ChevronLeft size={16} color={mutedFg} />
            )}
          </Pressable>
        </View>

        <View className="py-3">
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
          <SidebarActionRow
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

// Shared row chrome — a fixed-width icon column followed by a
// flex-1 label. Matches the header's columnar split so icons land
// in the same column whether you're looking at the brand mark, a
// nav row, or the logout button.
function RowShell({ active, children }: { active?: boolean; children: React.ReactNode }) {
  return (
    <View className="relative">
      {active ? (
        <View
          pointerEvents="none"
          className="absolute bottom-2 left-0 top-2 z-10 w-[3px] rounded-r-full bg-primary"
        />
      ) : null}
      {children}
    </View>
  );
}

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
    <RowShell active={active}>
      <Link href={item.href} accessibilityRole="link" accessibilityLabel={item.label} asChild>
        <Pressable
          className={`h-10 flex-row items-center overflow-hidden ${
            active ? 'bg-muted' : 'active:bg-muted'
          }`}>
          <View style={{ width: ICON_COL_WIDTH }} className="shrink-0 items-center justify-center">
            <Icon size={20} color={tint} />
          </View>
          <Animated.View
            style={labelStyle}
            className="flex-1 shrink overflow-hidden pr-3"
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
    </RowShell>
  );
}

function SidebarActionRow({
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
    <RowShell>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel}
        testID={testID}
        onPress={onPress}
        disabled={disabled}
        className={`h-10 flex-row items-center overflow-hidden active:bg-muted ${
          disabled ? 'opacity-50' : ''
        }`}>
        <View style={{ width: ICON_COL_WIDTH }} className="shrink-0 items-center justify-center">
          <Icon size={20} color={color} />
        </View>
        <Animated.View
          style={labelStyle}
          className="flex-1 shrink overflow-hidden pr-3"
          pointerEvents={labelInteractive ? 'auto' : 'none'}>
          <Text numberOfLines={1} className="text-sm font-medium">
            {label}
          </Text>
        </Animated.View>
      </Pressable>
    </RowShell>
  );
}
