package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// VoIP token registration is the new POST /v1/devices/voip endpoint.

func TestRegisterVoIP_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/devices/voip", map[string]any{
		"voip_token": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["voip_token"] != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Errorf("voip_token = %v, want round-trip", got["voip_token"])
	}
	if got["id"] == "" || got["id"] == nil {
		t.Errorf("id missing in response")
	}
}

func TestRegisterVoIP_Idempotent(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	body := map[string]any{
		"voip_token": "deadbeef" + strings.Repeat("0", 56),
	}
	first := post(t, c, h.Server.URL+"/v1/devices/voip", body)
	t.Cleanup(func() { _ = first.Body.Close() })
	second := post(t, c, h.Server.URL+"/v1/devices/voip", body)
	t.Cleanup(func() { _ = second.Body.Close() })

	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 on both, got %d / %d", first.StatusCode, second.StatusCode)
	}
	a := mustDecode(t, first.Body)
	b := mustDecode(t, second.Body)
	if a["id"] != b["id"] {
		t.Errorf("idempotent re-register should reuse id: %v vs %v", a["id"], b["id"])
	}
}

func TestRegisterVoIP_RejectsEmptyToken(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/devices/voip", map[string]any{
		"voip_token": "",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestRegisterVoIP_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/devices/voip", map[string]any{
		"voip_token": "abc",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestRegisterVoIP_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	req, err := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/devices/voip", strings.NewReader("{not-json"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}
