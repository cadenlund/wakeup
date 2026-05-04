package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// helper: create a group conversation between alice + bob with the given name.
func seedGroup(ctx context.Context, t *testing.T, h *testutil.Harness, name string, members ...domain.User) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := h.ConvRepo.CreateConversation(ctx, conversation.CreateParams{
		ID: id, Type: domain.ConversationGroup, Name: &name, CreatedBy: members[0].ID,
	}); err != nil {
		t.Fatalf("create conv: %v", err)
	}
	for _, m := range members {
		role := domain.MemberRoleMember
		if m.ID == members[0].ID {
			role = domain.MemberRoleAdmin
		}
		if _, err := h.ConvRepo.AddMember(ctx, id, m.ID, role); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	return id
}

func TestSearch_FindsUserByDisplayName(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	// Use a fixed, distinctive display name so the query exercises
	// display-name matching specifically (not username) — fails if
	// the trigram `OR` clause ever drops display_name.
	_, peer := h.AuthClient(t, testutil.WithDisplayName("ZephyrTarget"))

	resp, err := c.Get(h.Server.URL + "/v1/search?q=zephyr&types=users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	users, _ := got["users"].([]any)
	if len(users) == 0 {
		t.Errorf("expected display-name match for ZephyrTarget; peer.DisplayName=%q", peer.DisplayName)
	}
}

func TestSearch_FindsConversationByName(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	seedGroup(context.Background(), t, h, "Wakeup Crew", alice, bob)

	resp, err := c.Get(h.Server.URL + "/v1/search?q=wake&types=conversations")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	convs, _ := got["conversations"].([]any)
	if len(convs) != 1 {
		t.Fatalf("expected 1 conv hit, got %d", len(convs))
	}
	row := convs[0].(map[string]any)
	if row["name"] != "Wakeup Crew" {
		t.Errorf("name = %v, want Wakeup Crew", row["name"])
	}
}

func TestSearch_FindsMessageByBody(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	convID := seedGroup(context.Background(), t, h, "team", alice, bob)

	// Insert a message in the conv directly.
	msgID := uuid.Must(uuid.NewV7())
	if _, err := h.MsgRepo.Create(context.Background(), message.CreateParams{
		ID: msgID, ConversationID: convID, SenderID: bob.ID,
		Body: "where are we meeting tomorrow morning",
	}); err != nil {
		t.Fatalf("seed msg: %v", err)
	}

	resp, err := c.Get(h.Server.URL + "/v1/search?q=meeting&types=messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message hit, got %d", len(msgs))
	}
	row := msgs[0].(map[string]any)
	if !strings.Contains(row["body"].(string), "meeting") {
		t.Errorf("body = %v, want substring 'meeting'", row["body"])
	}
}

func TestSearch_NonMemberDoesNotSeeMessages(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	cAlice, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	_, carol := h.AuthClient(t)

	// Bob + Carol have a private group; Alice isn't a member.
	convID := seedGroup(context.Background(), t, h, "secret", bob, carol)
	if _, err := h.MsgRepo.Create(context.Background(), message.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: convID, SenderID: bob.ID,
		Body: "rendezvous behind the gym",
	}); err != nil {
		t.Fatalf("seed msg: %v", err)
	}

	// Alice searches — must not see the message (or the conv).
	resp, err := cAlice.Get(h.Server.URL + "/v1/search?q=rendezvous")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 0 {
		t.Errorf("non-member found %d messages — must be 0", len(msgs))
	}
	convs, _ := got["conversations"].([]any)
	if len(convs) != 0 {
		t.Errorf("non-member found %d conversations — must be 0", len(convs))
	}
	// Use alice to silence the unused-variable warning.
	_ = alice
}

func TestSearch_RejectsShortQuery_422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/search?q=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSearch_RejectsUnknownType_422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/search?q=hello&types=videos")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSearch_Unauthenticated_401(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/search?q=hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}
