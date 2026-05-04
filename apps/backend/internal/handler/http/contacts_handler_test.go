package httpapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// hashEmail mirrors the client-side hashing the mobile spec describes:
// SHA-256 over the lowercased + trimmed email, hex-encoded.
func hashEmail(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func TestContactsMatch_FindsByEmailHash(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// Seed a peer whose email we can hash on the client side.
	_, peer := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
		"email_hashes": []string{hashEmail(peer.Email)},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	matched, _ := got["matched"].([]any)
	if len(matched) != 1 {
		t.Fatalf("matched len = %d, want 1", len(matched))
	}
	row := matched[0].(map[string]any)
	if row["id"] != peer.ID.String() {
		t.Errorf("matched id = %v, want %v", row["id"], peer.ID)
	}
}

func TestContactsMatch_UnmatchedHashesNotEchoed(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	bogus := strings.Repeat("a", 64)
	resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
		"email_hashes": []string{bogus},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	matched, _ := got["matched"].([]any)
	if len(matched) != 0 {
		t.Errorf("matched len = %d, want 0 — unmatched hashes shouldn't be echoed", len(matched))
	}
}

func TestContactsMatch_RejectsMalformedHash_422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// The handler validator catches len != 64; the service catches the
	// non-hex case after that. Either way → 422.
	cases := []string{
		strings.Repeat("a", 63),                  // too short
		strings.Repeat("a", 65),                  // too long
		strings.Repeat("z", 64),                  // wrong charset
		strings.ToUpper(strings.Repeat("a", 64)), // uppercase
	}
	for _, bad := range cases {
		resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
			"email_hashes": []string{bad},
		})
		t.Cleanup(func() { _ = resp.Body.Close() })
		assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
	}
}

func TestContactsMatch_EmptyArrayRejected_422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
		"email_hashes": []string{},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestContactsMatch_OversizeBatchRejected_422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	huge := make([]string, 1001)
	for i := range huge {
		huge[i] = strings.Repeat("a", 64)
	}
	resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
		"email_hashes": huge,
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestContactsMatch_Unauthenticated_401(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/contacts/match", map[string]any{
		"email_hashes": []string{strings.Repeat("a", 64)},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestContactsMatch_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	req, err := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/contacts/match", strings.NewReader("{not-json"))
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
