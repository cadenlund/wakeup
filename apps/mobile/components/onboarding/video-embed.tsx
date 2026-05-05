// Cross-platform iframe-style video embed used by the onboarding
// "See it in action" slide. On web Expo Router gives us a real DOM,
// so we render an actual <iframe>; on iOS/Android we drop into
// react-native-webview which renders the same URL inside a native
// WKWebView / WebView. The container picks a 16:9 aspect ratio so
// the embed looks intentional on phones, tablets, and the web preview.
//
// `play` gates the actual mount so the carousel doesn't start
// downloading the video on slide 1 — when the user lands on the
// slide we set `play={true}`, the WebView mounts, and the URL's
// `autoplay=true&muted=true` params + `mediaPlaybackRequiresUserAction
// false` start playback automatically. Browsers (web + WebView)
// require muted for unprompted autoplay; the user can unmute from
// the player chrome.
import { Play } from 'lucide-react-native';
import * as React from 'react';
import {
  ActivityIndicator,
  Platform,
  Pressable,
  View,
  type StyleProp,
  type ViewStyle,
} from 'react-native';
import { WebView } from 'react-native-webview';

type Props = {
  uri: string;
  /** Mount + autoplay only when true. Defaults to true so callers
   *  that don't care about lifecycle still get a working embed. */
  play?: boolean;
  style?: StyleProp<ViewStyle>;
};

const CONTAINER_BASE: ViewStyle = {
  width: '100%',
  aspectRatio: 16 / 9,
  borderRadius: 12,
  overflow: 'hidden',
  backgroundColor: '#000',
};

function withAutoplayParams(uri: string): string {
  // Idempotent: if the caller already set autoplay/muted in the URL
  // we leave them alone. Otherwise append them with the right
  // separator depending on whether the URL already has a query.
  const hasQuery = uri.includes('?');
  const sep = hasQuery ? '&' : '?';
  const additions: string[] = [];
  if (!/[?&]autoplay=/.test(uri)) additions.push('autoplay=true');
  if (!/[?&]muted=/.test(uri)) additions.push('muted=true');
  return additions.length === 0 ? uri : `${uri}${sep}${additions.join('&')}`;
}

export function VideoEmbed({ uri, play = true, style }: Props) {
  const finalUri = React.useMemo(() => withAutoplayParams(uri), [uri]);

  if (!play) {
    // Lightweight placeholder while we're not on this slide — same
    // dimensions so the surrounding layout doesn't reflow when we
    // swap it for the real WebView.
    return (
      <View style={[CONTAINER_BASE, style]}>
        <View
          style={{
            ...StyleSheetAbsoluteFill,
            alignItems: 'center',
            justifyContent: 'center',
          }}>
          <View
            style={{
              width: 56,
              height: 56,
              borderRadius: 28,
              backgroundColor: 'rgba(255, 255, 255, 0.18)',
              alignItems: 'center',
              justifyContent: 'center',
            }}>
            <Play size={26} color="#ffffff" fill="#ffffff" />
          </View>
        </View>
      </View>
    );
  }

  if (Platform.OS === 'web') {
    const Iframe = 'iframe' as unknown as React.ElementType;
    return (
      <View style={[CONTAINER_BASE, style]}>
        <Iframe
          src={finalUri}
          width="100%"
          height="100%"
          frameBorder={0}
          referrerPolicy="unsafe-url"
          allowFullScreen
          allow="autoplay; clipboard-write; encrypted-media; picture-in-picture"
          style={{ border: 0 }}
        />
      </View>
    );
  }

  return (
    <View style={[CONTAINER_BASE, style]}>
      <WebView
        source={{ uri: finalUri }}
        allowsFullscreenVideo
        // Required for unprompted playback on iOS/Android. The
        // muted=true URL param gets the player past browser
        // autoplay-with-sound restrictions; the user can unmute via
        // the embedded chrome.
        mediaPlaybackRequiresUserAction={false}
        allowsInlineMediaPlayback
        javaScriptEnabled
        domStorageEnabled
        // The autoplay URL param is advisory at best — Guidde's
        // player is gated by the browser's autoplay policy. We hook
        // the page TWICE: `injectedJavaScriptBeforeContentLoaded`
        // sets up a MutationObserver that catches the <video>
        // element the moment it lands in the DOM (works even if
        // Guidde lazy-injects it). `injectedJavaScript` is a final
        // belt-and-suspenders attempt after onload.
        injectedJavaScriptBeforeContentLoaded={INJECTED_AUTOPLAY_JS_EARLY}
        injectedJavaScript={INJECTED_AUTOPLAY_JS}
        injectedJavaScriptForMainFrameOnly={false}
        injectedJavaScriptBeforeContentLoadedForMainFrameOnly={false}
        onShouldStartLoadWithRequest={() => true}
        startInLoadingState
        renderLoading={() => (
          <View
            style={{
              ...StyleSheetAbsoluteFill,
              alignItems: 'center',
              justifyContent: 'center',
              backgroundColor: '#000',
            }}>
            <ActivityIndicator color="#ffffff" />
          </View>
        )}
        style={{ flex: 1, backgroundColor: '#000' }}
      />
    </View>
  );
}

// Manual <Pressable> escape hatch for the placeholder so taps still
// register as a "play" intent if a parent wants to drive the
// transition manually instead of via slide visibility.
export function VideoEmbedPlaceholder({
  onPress,
  style,
}: {
  onPress?: () => void;
  style?: StyleProp<ViewStyle>;
}) {
  return (
    <Pressable
      onPress={onPress}
      style={[CONTAINER_BASE, { alignItems: 'center', justifyContent: 'center' }, style]}>
      <View
        style={{
          width: 56,
          height: 56,
          borderRadius: 28,
          backgroundColor: 'rgba(255, 255, 255, 0.18)',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
        <Play size={26} color="#ffffff" fill="#ffffff" />
      </View>
    </Pressable>
  );
}

const StyleSheetAbsoluteFill = {
  position: 'absolute' as const,
  top: 0,
  left: 0,
  right: 0,
  bottom: 0,
};

// Playback rate applied to <video> elements once they appear.
// Guidde's player typically respects HTMLMediaElement.playbackRate;
// if a future Guidde release intercepts it, the user can still set
// rate via the player chrome.
const PLAYBACK_RATE = 1.5;

// Hook the page BEFORE Guidde's bundle loads: install a
// MutationObserver that fires the moment a <video> element lands
// in the DOM, so we don't miss it between polls. Also install a
// "fake user gesture" affordance (a single bubbling click on body)
// in case the player gates on UA gesture.
const INJECTED_AUTOPLAY_JS_EARLY = `
  (function() {
    if (window.__wakeupAutoplayHooked) return;
    window.__wakeupAutoplayHooked = true;
    var rate = ${PLAYBACK_RATE};

    function bind(v) {
      try {
        v.muted = true;
        v.defaultMuted = true;
        v.setAttribute('muted', '');
        v.setAttribute('autoplay', '');
        v.setAttribute('playsinline', '');
        v.setAttribute('webkit-playsinline', '');
        var setRate = function() { try { v.playbackRate = rate; } catch (e) {} };
        setRate();
        v.addEventListener('loadedmetadata', setRate);
        v.addEventListener('playing', setRate);
        v.addEventListener('canplay', function() { v.play().catch(function(){}); });
        v.play().catch(function(){});
      } catch (e) {}
    }

    function scan(root) {
      try {
        if (!root || !root.querySelectorAll) return;
        var vids = root.querySelectorAll('video');
        for (var i = 0; i < vids.length; i++) bind(vids[i]);
      } catch (e) {}
    }

    var mo = new MutationObserver(function(mutations) {
      for (var i = 0; i < mutations.length; i++) {
        var m = mutations[i];
        for (var j = 0; j < m.addedNodes.length; j++) {
          var n = m.addedNodes[j];
          if (n && n.nodeType === 1) {
            if (n.tagName === 'VIDEO') bind(n);
            else scan(n);
          }
        }
      }
    });
    if (document.documentElement) {
      mo.observe(document.documentElement, { childList: true, subtree: true });
    }
    document.addEventListener('DOMContentLoaded', function() { scan(document); });
  })();
  true;
`;

// Final-attempt poller after the page has loaded — handles cases
// where Guidde's bundle injects the player after our observer is
// installed but before the bind() side-effects took hold.
const INJECTED_AUTOPLAY_JS = `
  (function() {
    var rate = ${PLAYBACK_RATE};
    var attempts = 0;
    function tryPlay() {
      var played = false;
      var videos = document.querySelectorAll('video');
      videos.forEach(function(v) {
        try {
          v.muted = true;
          v.defaultMuted = true;
          v.playbackRate = rate;
          v.setAttribute('playsinline', '');
          v.setAttribute('webkit-playsinline', '');
          var p = v.play();
          if (p && typeof p.then === 'function') {
            p.then(function() { played = true; }).catch(function() {});
          } else {
            played = true;
          }
        } catch (e) {}
      });
      if (!played) {
        var selectors = [
          '[aria-label="Play"]',
          '[aria-label="play"]',
          'button[title="Play"]',
          '.vjs-big-play-button',
          '.plyr__control--overlaid',
          '[data-testid="play-button"]',
          '.guidde-play-button',
          'button[class*="play" i]'
        ];
        for (var i = 0; i < selectors.length; i++) {
          var btn = document.querySelector(selectors[i]);
          if (btn) {
            try { btn.click(); played = true; break; } catch (e) {}
          }
        }
      }
      attempts++;
      if (attempts < 60) setTimeout(tryPlay, 250);
    }
    if (document.readyState === 'complete') tryPlay();
    else window.addEventListener('load', tryPlay);
    setTimeout(tryPlay, 500);
  })();
  true;
`;
