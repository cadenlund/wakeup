package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil/fixtures"
)

// makeAuthedClient registers a fixture user via the harness's AuthClient
// helper. Returns the client + the persisted user record.
func makeAuthedClient(t *testing.T, h *testutil.Harness) (*http.Client, domain.User) {
	t.Helper()
	c, u := h.AuthClient(t)
	return c, u
}

// otherClient registers a SECOND user via a fresh client; used when a
// test needs two authenticated identities (e.g. accept request flow).
func otherClient(t *testing.T, h *testutil.Harness) (*http.Client, domain.User) {
	t.Helper()
	c, u := h.AuthClient(t)
	return c, u
}

// --- POST /v1/friends/requests -----------------------------------------

func TestSendFriendRequest_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, b := otherClient(t, h)

	resp := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": b.Username})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "pending" {
		t.Errorf("status = %v, want pending", got["status"])
	}
	other, _ := got["user"].(map[string]any)
	if other["username"] != b.Username {
		t.Errorf("user.username = %v, want %s", other["username"], b.Username)
	}
}

func TestSendFriendRequest_Self(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, ua := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ua.Username})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSendFriendRequest_TargetNotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": "ghostuser123"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestSendFriendRequest_Conflict(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, b := otherClient(t, h)
	r1 := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": b.Username})
	_ = r1.Body.Close()
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("first request status=%d", r1.StatusCode)
	}
	r2 := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": b.Username})
	t.Cleanup(func() { _ = r2.Body.Close() })
	assertCode(t, r2, http.StatusConflict, apierror.CodeConflict)
}

func TestSendFriendRequest_Validation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ""})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSendFriendRequest_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/friends/requests", map[string]any{"username": "anyone"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- POST /v1/friends/requests/{id}/accept -----------------------------

func TestAcceptFriendRequest_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, ua := makeAuthedClient(t, h)
	bClient, ub := otherClient(t, h)

	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username})
	defer func() { _ = r.Body.Close() }()
	body, _ := io.ReadAll(r.Body)
	var pending map[string]any
	_ = json.Unmarshal(body, &pending)
	id, _ := pending["id"].(string)

	resp := post(t, bClient, h.Server.URL+"/v1/friends/requests/"+id+"/accept", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	rb, _ := io.ReadAll(resp.Body)
	var got map[string]any
	_ = json.Unmarshal(rb, &got)
	if got["status"] != "accepted" {
		t.Errorf("status = %v, want accepted", got["status"])
	}
	// From b's POV the counterparty is a.
	other, _ := got["user"].(map[string]any)
	if other["username"] != ua.Username {
		t.Errorf("user.username = %v, want %s", other["username"], ua.Username)
	}
}

func TestAcceptFriendRequest_RequesterCannotAccept(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, b := otherClient(t, h)

	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": b.Username})
	defer func() { _ = r.Body.Close() }()
	body, _ := io.ReadAll(r.Body)
	var pending map[string]any
	_ = json.Unmarshal(body, &pending)
	id, _ := pending["id"].(string)

	// Requester (a) tries to accept their own outgoing request.
	resp := post(t, a, h.Server.URL+"/v1/friends/requests/"+id+"/accept", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestAcceptFriendRequest_NotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/requests/"+uuid.New().String()+"/accept", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestAcceptFriendRequest_BadID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/requests/not-a-uuid/accept", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- POST /v1/friends/requests/{id}/decline ----------------------------

func TestDeclineFriendRequest_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	bClient, ub := otherClient(t, h)

	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username})
	defer func() { _ = r.Body.Close() }()
	body, _ := io.ReadAll(r.Body)
	var pending map[string]any
	_ = json.Unmarshal(body, &pending)
	id, _ := pending["id"].(string)

	req, _ := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/friends/requests/"+id+"/decline", nil)
	resp, err := bClient.Do(req)
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
}

// --- GET /v1/friends ----------------------------------------------------

func TestListFriends_AfterAccept(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	bClient, ub := otherClient(t, h)

	// Create + accept.
	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username})
	defer func() { _ = r.Body.Close() }()
	rb, _ := io.ReadAll(r.Body)
	var pending map[string]any
	_ = json.Unmarshal(rb, &pending)
	id, _ := pending["id"].(string)
	_ = post(t, bClient, h.Server.URL+"/v1/friends/requests/"+id+"/accept", nil).Body.Close()

	resp, err := a.Get(h.Server.URL + "/v1/friends")
	if err != nil {
		t.Fatalf("GET /v1/friends: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	data, _ := got["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data len = %d, want 1; body=%s", len(data), body)
	}
	row := data[0].(map[string]any)
	if row["status"] != "accepted" {
		t.Errorf("status = %v, want accepted", row["status"])
	}
	other := row["user"].(map[string]any)
	if other["username"] != ub.Username {
		t.Errorf("counterparty username = %v, want %s", other["username"], ub.Username)
	}
}

func TestListFriends_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/friends")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/friends/requests ------------------------------------------

func TestListFriendRequests_PartitionsByDirection(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, ub := otherClient(t, h) // outgoing: a → b
	cClient, _ := otherClient(t, h)
	_, ua := h.AuthClient(t) // unused identity, replaced below — just wire ids

	_ = ua // silence unused warning if we don't reach the loop body

	// Outgoing request from a → b.
	if r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username}); r.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		t.Fatalf("setup outgoing status=%d body=%s", r.StatusCode, body)
	}

	// Incoming request: c → a's username. We need a's username; round-trip via /v1/auth/me.
	meResp, err := a.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	mb, _ := io.ReadAll(meResp.Body)
	_ = meResp.Body.Close()
	var me map[string]any
	_ = json.Unmarshal(mb, &me)
	aUsername, _ := me["username"].(string)
	if r := post(t, cClient, h.Server.URL+"/v1/friends/requests", map[string]any{"username": aUsername}); r.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		t.Fatalf("setup incoming status=%d body=%s", r.StatusCode, body)
	}

	resp, err := a.Get(h.Server.URL + "/v1/friends/requests")
	if err != nil {
		t.Fatalf("GET /v1/friends/requests: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	in, _ := got["incoming"].([]any)
	out, _ := got["outgoing"].([]any)
	if len(in) != 1 {
		t.Errorf("incoming len = %d, want 1", len(in))
	}
	if len(out) != 1 {
		t.Errorf("outgoing len = %d, want 1", len(out))
	}
}

// --- DELETE /v1/friends/{user_id} -------------------------------------

func TestUnfriend_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	bClient, ub := otherClient(t, h)
	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username})
	rb, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	var pending map[string]any
	_ = json.Unmarshal(rb, &pending)
	id, _ := pending["id"].(string)
	_ = post(t, bClient, h.Server.URL+"/v1/friends/requests/"+id+"/accept", nil).Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, h.Server.URL+"/v1/friends/"+ub.ID.String(), nil)
	resp, err := a.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
}

func TestUnfriend_NotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	other := fixtures.MakeUser(t, h.DB)
	req, _ := http.NewRequest(http.MethodDelete, h.Server.URL+"/v1/friends/"+other.ID.String(), nil)
	resp, err := a.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestUnfriend_BadUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	req, _ := http.NewRequest(http.MethodDelete, h.Server.URL+"/v1/friends/not-a-uuid", nil)
	resp, err := a.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- POST /v1/friends/{user_id}/block + DELETE -------------------------

func TestBlockUnblock_Roundtrip(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, ub := otherClient(t, h)

	resp := post(t, a, h.Server.URL+"/v1/friends/"+ub.ID.String()+"/block", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("block status=%d body=%s", resp.StatusCode, rb)
	}
	rb, _ := io.ReadAll(resp.Body)
	var got map[string]any
	_ = json.Unmarshal(rb, &got)
	if got["status"] != "blocked" {
		t.Errorf("status = %v, want blocked", got["status"])
	}

	// Unblock.
	req, _ := http.NewRequest(http.MethodDelete, h.Server.URL+"/v1/friends/"+ub.ID.String()+"/block", nil)
	r2, err := a.Do(req)
	if err != nil {
		t.Fatalf("unblock: %v", err)
	}
	t.Cleanup(func() { _ = r2.Body.Close() })
	if r2.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(r2.Body)
		t.Fatalf("unblock status=%d body=%s", r2.StatusCode, rb)
	}
}

func TestBlock_Self(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, ua := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/"+ua.ID.String()+"/block", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestBlock_TargetMissing(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	resp := post(t, a, h.Server.URL+"/v1/friends/"+uuid.New().String()+"/block", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// --- DTO no-leak --------------------------------------------------------

func TestFriendDTOs_NoLeak(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := makeAuthedClient(t, h)
	_, ub := otherClient(t, h)
	r := post(t, a, h.Server.URL+"/v1/friends/requests", map[string]any{"username": ub.Username})
	t.Cleanup(func() { _ = r.Body.Close() })
	body, _ := io.ReadAll(r.Body)
	for _, leak := range []string{"password_hash", "email", "PasswordHash", "deleted_at"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("response leaked %q: %s", leak, body)
		}
	}
}

// silence unused-import warnings on the helper packages above.
var _ = context.Background
var _ = user.ErrNotFound
