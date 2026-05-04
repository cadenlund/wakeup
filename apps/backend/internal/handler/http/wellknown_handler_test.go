package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
)

// .well-known endpoints are unauthenticated + non-versioned, so we
// can hit them with a plain httptest server and skip the harness.

func TestWellKnown_AppleAppSiteAssociation_Configured(t *testing.T) {
	t.Parallel()
	h := httpapi.NewWellKnownHandler("ABCD12EFGH.app.wakeup.client", "", "")
	srv := httptest.NewServer(http.HandlerFunc(h.AppleAppSiteAssociation))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	applinks, _ := got["applinks"].(map[string]any)
	if applinks == nil {
		t.Fatal("applinks key missing")
	}
	details, _ := applinks["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	first := details[0].(map[string]any)
	appIDs, _ := first["appIDs"].([]any)
	if len(appIDs) != 1 || appIDs[0] != "ABCD12EFGH.app.wakeup.client" {
		t.Errorf("appIDs = %v, want [ABCD12EFGH.app.wakeup.client]", appIDs)
	}
}

func TestWellKnown_AppleAppSiteAssociation_NotConfigured_404(t *testing.T) {
	t.Parallel()
	h := httpapi.NewWellKnownHandler("", "", "")
	srv := httptest.NewServer(http.HandlerFunc(h.AppleAppSiteAssociation))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when IOS_APP_ID is empty", resp.StatusCode)
	}
}

func TestWellKnown_AssetLinks_Configured(t *testing.T) {
	t.Parallel()
	fingerprints := strings.Join([]string{
		"AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99",
	}, ",")
	h := httpapi.NewWellKnownHandler("", "app.wakeup.client", fingerprints)
	srv := httptest.NewServer(http.HandlerFunc(h.AssetLinks))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	target, _ := got[0]["target"].(map[string]any)
	if target["package_name"] != "app.wakeup.client" {
		t.Errorf("package_name = %v, want app.wakeup.client", target["package_name"])
	}
	prints, _ := target["sha256_cert_fingerprints"].([]any)
	if len(prints) != 1 {
		t.Errorf("fingerprints len = %d, want 1", len(prints))
	}
}

func TestWellKnown_AssetLinks_NotConfigured_404(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pkg  string
		fps  string
	}{
		{"missing package", "", "AA:BB"},
		{"missing fingerprints", "app.wakeup.client", ""},
		{"both missing", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := httpapi.NewWellKnownHandler("", c.pkg, c.fps)
			srv := httptest.NewServer(http.HandlerFunc(h.AssetLinks))
			t.Cleanup(srv.Close)
			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404 when %s", resp.StatusCode, c.name)
			}
		})
	}
}
