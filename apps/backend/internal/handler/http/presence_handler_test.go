package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeFriends links a and b as accepted friends by calling the
// friendship repo directly. The presence service's friend lookup
// reads from the same table the friend service writes to, so this
// shortcut is the cheapest way to set up a fan-out scenario.
func makeFriends(t *testing.T, h *testutil.Harness, a, b domain.User) {
	t.Helper()
	frow, err := h.FriendRepo.Create(context.Background(), friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a.ID, AddresseeID: b.ID,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("FriendRepo.Create: %v", err)
	}
	if _, err := h.FriendRepo.Accept(context.Background(), frow.ID); err != nil {
		t.Fatalf("FriendRepo.Accept: %v", err)
	}
}

// --- POST /v1/presence/status -----------------------------------------

func TestSetPresenceStatus_AcceptsSleeping(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "sleeping"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestSetPresenceStatus_AcceptsOnline(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "online"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

// `away` and `dnd` are user-settable as sticky intents per the new
// presence design. They round-trip through the same handler.
func TestSetPresenceStatus_AcceptsAway(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "away"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestSetPresenceStatus_AcceptsDND(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "dnd"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

// Sending `null` (or omitting the field) clears any existing sticky
// intent — the next heartbeat / decay cycle takes back over.
func TestSetPresenceStatus_AcceptsNullToClear(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": nil})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestSetPresenceStatus_RejectsOffline(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "offline"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSetPresenceStatus_RejectsBogus(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "afk"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSetPresenceStatus_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/presence/status", map[string]any{"status": "online"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/presence/friends -----------------------------------------

func TestGetPresenceFriends_EmptyForLonelyUser(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/presence/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 0 {
		t.Errorf("len = %d, want 0", len(data))
	}
}

func TestGetPresenceFriends_WithFriendsAndStatuses(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	cAlice, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	makeFriends(t, h, alice, bob)
	// Bob has no presence row yet; should render as offline.

	// Make ourselves sleeping and assert the friend list reflects it.
	if err := h.PresenceSvc.SetStatus(context.Background(), bob.ID, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	resp, err := cAlice.Get(h.Server.URL + "/v1/presence/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("len = %d, want 1", len(data))
	}
	row := data[0].(map[string]any)
	if row["status"] != "sleeping" {
		t.Errorf("status = %v, want sleeping", row["status"])
	}
	if row["user_id"] != bob.ID.String() {
		t.Errorf("user_id = %v, want %v", row["user_id"], bob.ID)
	}
}

// --- GET /v1/widget/friends -------------------------------------------

func TestWidgetFriends_EmbedsUserProfile(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	cAlice, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	makeFriends(t, h, alice, bob)

	resp, err := cAlice.Get(h.Server.URL + "/v1/widget/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("len = %d, want 1", len(data))
	}
	row := data[0].(map[string]any)
	user, _ := row["user"].(map[string]any)
	if user == nil {
		t.Fatal("user object missing")
	}
	if user["id"] != bob.ID.String() {
		t.Errorf("user.id = %v, want %v", user["id"], bob.ID)
	}
	if _, ok := user["display_name"]; !ok {
		t.Errorf("user.display_name missing")
	}
	pres, _ := row["presence"].(map[string]any)
	if pres == nil {
		t.Fatal("presence object missing")
	}
	if pres["status"] != "offline" {
		t.Errorf("presence.status = %v, want offline (no row yet)", pres["status"])
	}
}

func TestWidgetFriends_EmptyForLonelyUser(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/widget/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 0 {
		t.Errorf("len = %d, want 0", len(data))
	}
}

func TestWidgetFriends_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/widget/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}
