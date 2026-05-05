// Long-list primitive per spec §4.9. Every list of >~20 items routes
// through this — conversations list, friends list, message thread —
// so the recycling, viewport tracking, and (eventual) onEndReached
// pagination are uniform across the app.
//
// FlashList v2 dropped the `estimatedItemSize` prop in favour of
// runtime measurement, so the wrapper is currently a thin pass-through.
// It exists as a single import target ("@/components/ui/list") so the
// day we want to add per-app defaults — empty fallback, refresh
// control wiring, scroll-to-top on tab re-press — there's already
// one place to slot it in.
import { FlashList, type FlashListProps, type FlashListRef } from '@shopify/flash-list';
import * as React from 'react';

function ListInner<T>(props: FlashListProps<T>, ref: React.ForwardedRef<FlashListRef<T>>) {
  return <FlashList<T> ref={ref} {...props} />;
}

const List = React.forwardRef(ListInner) as <T>(
  props: FlashListProps<T> & { ref?: React.ForwardedRef<FlashListRef<T>> }
) => React.ReactElement;

export { List };
export type { FlashListProps as ListProps, FlashListRef as ListRef };
