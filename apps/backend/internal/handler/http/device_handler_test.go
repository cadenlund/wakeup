package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// --- POST /v1/devices ----------------------------------------------------

func TestRegisterDevice_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/devices", map[string]any{
		"expo_token": "ExponentPushToken[abc]",
		"platform":   "ios",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["id"] == nil || got["id"] == "" {
		t.Errorf("missing id: %+v", got)
	}
	if got["expo_token"] != "ExponentPushToken[abc]" {
		t.Errorf("expo_token mismatch: %+v", got)
	}
	if got["platform"] != "ios" {
		t.Errorf("platform mismatch: %+v", got)
	}
}

// Re-registering the same (user, expo_token) pair should return 201
// with a stable id (the server upserts via ON CONFLICT). The mobile
// client calls this on every cold start, so churn is the failure mode.
func TestRegisterDevice_IdempotentSamePair(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, u := h.AuthClient(t)

	first := post(t, c, h.Server.URL+"/v1/devices", map[string]any{
		"expo_token": "ExponentPushToken[same]", "platform": "ios",
	})
	t.Cleanup(func() { _ = first.Body.Close() })
	if first.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first status=%d body=%s", first.StatusCode, body)
	}
	firstBody := mustDecode(t, first.Body)
	firstID, _ := firstBody["id"].(string)

	second := post(t, c, h.Server.URL+"/v1/devices", map[string]any{
		"expo_token": "ExponentPushToken[same]", "platform": "android",
	})
	t.Cleanup(func() { _ = second.Body.Close() })
	if second.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second status=%d body=%s", second.StatusCode, body)
	}
	secondBody := mustDecode(t, second.Body)
	if secondBody["id"] != firstID {
		t.Errorf("re-register should return same id: first=%v second=%v",
			firstID, secondBody["id"])
	}
	if secondBody["platform"] != "android" {
		t.Errorf("platform should refresh to android, got %v", secondBody["platform"])
	}

	// Single row in the table for this user — no duplicates.
	tokens, err := h.DeviceRepo.ListByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected 1 row, got %d", len(tokens))
	}
}

func TestRegisterDevice_RejectsBogusPlatform(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/devices", map[string]any{
		"expo_token": "ExponentPushToken[a]", "platform": "blackberry",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestRegisterDevice_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// Empty body — both fields are required by the DTO validator.
	resp := post(t, c, h.Server.URL+"/v1/devices", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestRegisterDevice_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/devices", map[string]any{
		"expo_token": "ExponentPushToken[a]", "platform": "ios",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- DELETE /v1/devices/{id} --------------------------------------------

func TestDeleteDevice_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, u := h.AuthClient(t)

	tok, err := h.DeviceRepo.Register(context.Background(), u.ID, "ExponentPushToken[del]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("seed Register: %v", err)
	}

	resp := deleteReq(t, c, h.Server.URL+"/v1/devices/"+tok.ID.String())
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	tokens, err := h.DeviceRepo.ListByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("token should be gone, got %d rows", len(tokens))
	}
}

func TestDeleteDevice_MissingReturns404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := deleteReq(t, c, h.Server.URL+"/v1/devices/"+uuid.New().String())
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// Owner scoping: caller A cannot delete caller B's token. Surfaces as
// 404 (no enumeration leak), and B's token must remain in the table.
func TestDeleteDevice_OtherUsersTokenReturns404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	thiefClient, _ := h.AuthClient(t)
	_, owner := h.AuthClient(t)

	tok, err := h.DeviceRepo.Register(context.Background(), owner.ID, "ExponentPushToken[scoped]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("seed Register: %v", err)
	}

	resp := deleteReq(t, thiefClient, h.Server.URL+"/v1/devices/"+tok.ID.String())
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)

	// Owner's token still present.
	tokens, err := h.DeviceRepo.ListByUser(context.Background(), owner.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(tokens) != 1 {
		t.Errorf("owner's token should survive thief's delete attempt, got %d rows", len(tokens))
	}
}

func TestDeleteDevice_BadIDReturns400(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := deleteReq(t, c, h.Server.URL+"/v1/devices/not-a-uuid")
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestDeleteDevice_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := deleteReq(t, c, h.Server.URL+"/v1/devices/"+uuid.New().String())
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}
