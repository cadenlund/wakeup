package httpapi

import (
	"net/http"
	"strings"
)

// WellKnownHandler serves the two static JSON files that Apple and
// Google require for universal / app links (WAKEUPEXPO.md §10.5):
//
//   - /.well-known/apple-app-site-association  (iOS Universal Links)
//   - /.well-known/assetlinks.json              (Android App Links)
//
// Both are unauthenticated, no rate limiting, no v1 prefix — the OS
// fetches them with a fixed user-agent and won't follow redirects.
type WellKnownHandler struct {
	iOSAppID       string
	androidPackage string
	androidCertHex []string // SHA-256 fingerprints (colon-separated upper hex)
}

// NewWellKnownHandler builds the handler. Empty fields disable the
// corresponding endpoint (returns 404) — the operator opts in by
// setting IOS_APP_ID / ANDROID_PACKAGE / ANDROID_SHA256_FINGERPRINTS
// in the environment.
func NewWellKnownHandler(iosAppID, androidPackage, androidFingerprints string) *WellKnownHandler {
	var certs []string
	for _, c := range strings.Split(androidFingerprints, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			certs = append(certs, c)
		}
	}
	return &WellKnownHandler{
		iOSAppID:       strings.TrimSpace(iosAppID),
		androidPackage: strings.TrimSpace(androidPackage),
		androidCertHex: certs,
	}
}

// AppleAppSiteAssociation serves the iOS Universal Links manifest.
//
// @Summary      Apple App Site Association
// @Description  Static JSON consumed by iOS to enable Universal Links to `app.wakeup.client`. Returns 404 when `IOS_APP_ID` isn't configured. Apple's CDN caches this; clients should not rely on dynamic content.
// @Tags         system
// @Produce      json
// @Success      200  {object} object  "Apple universal-links manifest"
// @Failure      404  {object} ErrorResponse  "Not configured"
// @Router       /.well-known/apple-app-site-association [get]
func (h *WellKnownHandler) AppleAppSiteAssociation(w http.ResponseWriter, r *http.Request) {
	if h.iOSAppID == "" {
		http.NotFound(w, r)
		return
	}
	// Shape per Apple docs:
	// https://developer.apple.com/documentation/xcode/supporting-associated-domains
	body := map[string]any{
		"applinks": map[string]any{
			"details": []map[string]any{
				{
					"appIDs": []string{h.iOSAppID},
					"components": []map[string]any{
						// Match the §10.5 universal-link routes.
						{"/": "/c/*", "comment": "conversation deep link"},
						{"/": "/u/*", "comment": "user profile deep link"},
						{"/": "/r/*", "comment": "password reset deep link"},
						{"/": "/i/*", "comment": "friend invite landing"},
					},
				},
			},
		},
	}
	WriteJSON(w, http.StatusOK, body)
}

// AssetLinks serves the Android App Links manifest.
//
// @Summary      Android Asset Links
// @Description  Static JSON consumed by Android to enable App Links to `app.wakeup.client`. Returns 404 when `ANDROID_PACKAGE` or `ANDROID_SHA256_FINGERPRINTS` aren't configured. The fingerprints array supports signing-key rotation.
// @Tags         system
// @Produce      json
// @Success      200  {array}  object  "Android app-links manifest"
// @Failure      404  {object} ErrorResponse  "Not configured"
// @Router       /.well-known/assetlinks.json [get]
func (h *WellKnownHandler) AssetLinks(w http.ResponseWriter, r *http.Request) {
	if h.androidPackage == "" || len(h.androidCertHex) == 0 {
		http.NotFound(w, r)
		return
	}
	// Shape per Android docs:
	// https://developer.android.com/training/app-links/verify-android-applinks
	body := []map[string]any{
		{
			"relation": []string{"delegate_permission/common.handle_all_urls"},
			"target": map[string]any{
				"namespace":                "android_app",
				"package_name":             h.androidPackage,
				"sha256_cert_fingerprints": h.androidCertHex,
			},
		},
	}
	WriteJSON(w, http.StatusOK, body)
}
