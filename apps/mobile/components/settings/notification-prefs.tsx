// Notification-preferences section body (roadmap 8.3) — the four
// per-category push toggles backed by GET/PATCH /v1/users/me/notifications.
// Server-side these gate the offline-push fan-out (WS events still
// fire). Toggling is optimistic: flip the cached row immediately, PATCH
// the full set, and re-sync on settle so a failed write rolls back to
// the truth (the mutationCache toast surfaces the failure).
import * as React from 'react';
import { ActivityIndicator, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Switch } from '@/components/ui/switch';
import { Text } from '@/components/ui/text';
import {
  getGetV1UsersMeNotificationsQueryKey,
  useGetV1UsersMeNotifications,
  usePatchV1UsersMeNotifications,
} from '@/lib/api/hooks/users/users';
import type { InternalHandlerHttpNotificationPreferencesResponse } from '@/lib/api/model';

type Prefs = InternalHandlerHttpNotificationPreferencesResponse;
type ToggleKey = 'direct_messages' | 'group_messages' | 'friend_requests' | 'calls';

const ROWS: { key: ToggleKey; label: string; hint: string }[] = [
  { key: 'direct_messages', label: 'Direct messages', hint: 'New messages in 1:1 chats' },
  { key: 'group_messages', label: 'Group messages', hint: 'New messages in group chats' },
  { key: 'friend_requests', label: 'Friend requests', hint: 'When someone adds you' },
  { key: 'calls', label: 'Calls', hint: 'Incoming voice / video calls' },
];

export function NotificationPrefsSection() {
  const qc = useQueryClient();
  const queryKey = getGetV1UsersMeNotificationsQueryKey();
  const { data, isLoading, isError } = useGetV1UsersMeNotifications({
    query: { staleTime: 60_000 },
  });
  // apiFetch returns the bare body; orval types it as the {data,status} wrapper.
  const prefs = data as Prefs | undefined;

  const patch = usePatchV1UsersMeNotifications({
    mutation: {
      onSettled: () => {
        void qc.invalidateQueries({ queryKey });
      },
    },
  });

  const toggle = (key: ToggleKey, next: boolean) => {
    const current = (qc.getQueryData<Prefs>(queryKey) ?? prefs) as Prefs | undefined;
    if (!current) return;
    qc.setQueryData<Prefs>(queryKey, { ...current, [key]: next });
    patch.mutate({
      data: {
        direct_messages: current.direct_messages ?? true,
        group_messages: current.group_messages ?? true,
        friend_requests: current.friend_requests ?? true,
        calls: current.calls ?? true,
        [key]: next,
      },
    });
  };

  if (isLoading) {
    return (
      <View className="py-2">
        <ActivityIndicator />
      </View>
    );
  }
  if (isError || !prefs) {
    return (
      <Text variant="muted" className="text-sm">
        Couldn&apos;t load notification settings. Pull to refresh.
      </Text>
    );
  }

  return (
    <View className="gap-1">
      {ROWS.map((row, i) => {
        const value = prefs[row.key] ?? true;
        return (
          <View
            key={row.key}
            className={`flex-row items-center gap-3 py-3 ${i > 0 ? 'border-t border-border' : ''}`}>
            <View className="min-w-0 flex-1">
              <Text className="text-base">{row.label}</Text>
              <Text variant="muted" className="text-xs">
                {row.hint}
              </Text>
            </View>
            <Switch
              accessibilityLabel={row.label}
              checked={value}
              onCheckedChange={(next) => toggle(row.key, next)}
              disabled={patch.isPending}
            />
          </View>
        );
      })}
      <Text variant="muted" className="pt-2 text-xs">
        These only gate push notifications — in-app banners and unread counts always update.
      </Text>
    </View>
  );
}
