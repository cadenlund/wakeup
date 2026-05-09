// Web-only sidebar layout. Metro picks `_layout.web.tsx` over the
// bare `_layout.tsx` on the web bundle automatically; native still
// gets the bottom-tab navigator from `_layout.tsx`. Ergonomic split
// — bottom tabs feel right on a phone, a collapsible sidebar feels
// right in a browser window with horizontal real estate.
//
// Two widths: 64px (icons only) and 240px (icons + labels). Toggle
// state persists to localStorage so the user's preference survives
// a reload. Default is expanded.
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
  User,
  Users,
  type LucideIcon,
} from 'lucide-react-native';
import * as React from 'react';
import { Pressable, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { APIError } from '@/lib/api/client';
import { usePostV1AuthLogout } from '@/lib/api/hooks/auth/auth';
import { signedOut } from '@/lib/auth/post-auth-nav';
import { useThemeColor } from '@/lib/theme/use-theme-color';

const COLLAPSED_KEY = 'wakeup:sidebar:collapsed';

type NavItem = { href: '/' | '/friends' | '/profile'; label: string; icon: LucideIcon };

const NAV_ITEMS: NavItem[] = [
  { href: '/', label: 'Chats', icon: MessageCircle },
  { href: '/friends', label: 'Friends', icon: Users },
  { href: '/profile', label: 'Profile', icon: User },
];

export default function TabsWebLayout() {
  const pathname = usePathname();
  const primary = useThemeColor('primary');
  const mutedFg = useThemeColor('muted-foreground');
  const fg = useThemeColor('foreground');

  // Initial value comes from localStorage so the user's preference
  // survives a reload. Lazy initializer so the read only fires on
  // mount, not every render.
  const [collapsed, setCollapsed] = React.useState<boolean>(() => {
    if (typeof window === 'undefined' || !window.localStorage) return false;
    try {
      return window.localStorage.getItem(COLLAPSED_KEY) === 'true';
    } catch {
      return false;
    }
  });
  const toggle = React.useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        window.localStorage?.setItem(COLLAPSED_KEY, String(next));
      } catch {
        // Quota / privacy mode — non-critical, just lose the preference.
      }
      return next;
    });
  }, []);

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
      <View
        accessibilityRole="none"
        style={{ width: collapsed ? 64 : 240 }}
        className="border-r border-border bg-card py-3">
        <SidebarToggle collapsed={collapsed} onPress={toggle} fg={fg} />
        <View className="mt-2 gap-1 px-2">
          {NAV_ITEMS.map((item) => (
            <SidebarLink
              key={item.href}
              item={item}
              active={isActive(pathname, item.href)}
              collapsed={collapsed}
              activeColor={primary}
              inactiveColor={mutedFg}
            />
          ))}
        </View>
        <View className="flex-1" />
        <View className="border-t border-border px-2 pt-2">
          <SidebarButton
            icon={LogOut}
            label={logout.isPending ? 'Logging out…' : 'Log out'}
            onPress={() => logout.mutate()}
            disabled={logout.isPending}
            collapsed={collapsed}
            color={fg}
            testID="header-logout"
            accessibilityLabel="Log out"
          />
        </View>
      </View>
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

function SidebarToggle({
  collapsed,
  onPress,
  fg,
}: {
  collapsed: boolean;
  onPress: () => void;
  fg: string;
}) {
  const Icon = collapsed ? ChevronRight : ChevronLeft;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
      testID="sidebar-toggle"
      onPress={onPress}
      hitSlop={8}
      className="mx-2 flex-row items-center justify-center rounded-md py-2 active:bg-muted">
      <Icon size={18} color={fg} />
    </Pressable>
  );
}

function SidebarLink({
  item,
  active,
  collapsed,
  activeColor,
  inactiveColor,
}: {
  item: NavItem;
  active: boolean;
  collapsed: boolean;
  activeColor: string;
  inactiveColor: string;
}) {
  const Icon = item.icon;
  const tint = active ? activeColor : inactiveColor;
  return (
    <Link
      href={item.href}
      accessibilityRole="link"
      accessibilityLabel={item.label}
      className={`flex-row items-center gap-3 rounded-md px-3 py-2 ${active ? 'bg-muted' : ''}`}>
      <Icon size={18} color={tint} />
      {collapsed ? null : (
        <Text
          className={`text-sm ${active ? 'font-semibold text-primary' : 'text-muted-foreground'}`}>
          {item.label}
        </Text>
      )}
    </Link>
  );
}

function SidebarButton({
  icon: Icon,
  label,
  onPress,
  disabled,
  collapsed,
  color,
  testID,
  accessibilityLabel,
}: {
  icon: LucideIcon;
  label: string;
  onPress: () => void;
  disabled?: boolean;
  collapsed: boolean;
  color: string;
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
      className={`flex-row items-center gap-3 rounded-md px-3 py-2 active:bg-muted ${
        disabled ? 'opacity-50' : ''
      }`}>
      <Icon size={18} color={color} />
      {collapsed ? null : <Text className="text-sm font-medium">{label}</Text>}
    </Pressable>
  );
}
