// Side-effect import that quiets dev-only third-party log noise so
// the real warnings (our own code's bugs) stay visible.
//
// 1. Reanimated v4 strict mode fires "Reading from `value` during
//    component render" inside library worklets we don't own
//    (react-native-screens, flash-list, expo-router, etc.). Our own
//    code doesn't access `.value` / `.get()` during render.
// 2. `props.pointerEvents is deprecated` fires from react-native-
//    screens' ScreenStackHeaderConfig. Their migration, not ours.
import { LogBox } from 'react-native';
import { configureReanimatedLogger, ReanimatedLogLevel } from 'react-native-reanimated';

configureReanimatedLogger({
  level: ReanimatedLogLevel.warn,
  strict: false,
});

LogBox.ignoreLogs([
  // react-native-screens still uses the deprecated prop API on its
  // ScreenStackHeaderConfig. Filed upstream by their team; nothing
  // for us to fix.
  'props.pointerEvents is deprecated',
]);
