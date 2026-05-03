// Tests for the small helper methods on domain types. The repos and
// services exercise these indirectly via their own tests, but those
// live in `*_test` packages and don't contribute to domain's
// per-package coverage. Adding the asserts here keeps the §13.8
// audit honest.
package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// --- Conversation type helpers ----------------------------------------

func TestConversation_TypeHelpers(t *testing.T) {
	t.Parallel()
	g := domain.Conversation{Type: domain.ConversationGroup}
	if !g.IsGroup() || g.IsDirect() {
		t.Errorf("group helpers wrong: %+v", g)
	}
	d := domain.Conversation{Type: domain.ConversationDirect}
	if !d.IsDirect() || d.IsGroup() {
		t.Errorf("direct helpers wrong: %+v", d)
	}
}

// --- ConversationMember.IsAdmin --------------------------------------

func TestConversationMember_IsAdmin(t *testing.T) {
	t.Parallel()
	if !(domain.ConversationMember{Role: domain.MemberRoleAdmin}).IsAdmin() {
		t.Error("admin role should report IsAdmin=true")
	}
	if (domain.ConversationMember{Role: domain.MemberRoleMember}).IsAdmin() {
		t.Error("member role should report IsAdmin=false")
	}
}

// --- DevicePlatform.IsValid ------------------------------------------

func TestDevicePlatform_IsValid(t *testing.T) {
	t.Parallel()
	for _, p := range []domain.DevicePlatform{domain.DeviceIOS, domain.DeviceAndroid} {
		if !p.IsValid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []domain.DevicePlatform{"", "windows", "blackberry"} {
		if p.IsValid() {
			t.Errorf("%q should be invalid", p)
		}
	}
}

// --- Friendship helpers ----------------------------------------------

func TestFriendship_OtherID(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	f := domain.Friendship{RequesterID: a, AddresseeID: b}
	if got := f.OtherID(a); got != b {
		t.Errorf("OtherID(a) = %s, want %s", got, b)
	}
	if got := f.OtherID(b); got != a {
		t.Errorf("OtherID(b) = %s, want %s", got, a)
	}
	// OtherID with a third party returns the requester — the impl's
	// "if requester==self return addressee else return requester"
	// shape means a non-pair caller falls through to requester. Pin
	// that shape so a refactor that changes the fall-through doesn't
	// silently regress.
	c := uuid.New()
	if got := f.OtherID(c); got != a {
		t.Errorf("OtherID(stranger) = %s, want %s (requester fallthrough)", got, a)
	}
}

func TestFriendship_StatusHelpers(t *testing.T) {
	t.Parallel()
	if !(domain.Friendship{Status: domain.FriendshipAccepted}).IsAccepted() {
		t.Error("accepted should report IsAccepted=true")
	}
	if (domain.Friendship{Status: domain.FriendshipPending}).IsAccepted() {
		t.Error("pending should not report IsAccepted=true")
	}
	if !(domain.Friendship{Status: domain.FriendshipBlocked}).IsBlocked() {
		t.Error("blocked should report IsBlocked=true")
	}
	if (domain.Friendship{Status: domain.FriendshipPending}).IsBlocked() {
		t.Error("pending should not report IsBlocked=true")
	}
}

// --- Message helpers --------------------------------------------------

func TestMessage_StatusHelpers(t *testing.T) {
	t.Parallel()
	now := nowPtr()
	if !(domain.Message{EditedAt: now}).IsEdited() {
		t.Error("EditedAt set should report IsEdited=true")
	}
	if (domain.Message{}).IsEdited() {
		t.Error("nil EditedAt should report IsEdited=false")
	}
	if !(domain.Message{DeletedAt: now}).IsDeleted() {
		t.Error("DeletedAt set should report IsDeleted=true")
	}
	if (domain.Message{}).IsDeleted() {
		t.Error("nil DeletedAt should report IsDeleted=false")
	}
}

// --- PresenceStatus.IsValid ------------------------------------------

func TestPresenceStatus_IsValid(t *testing.T) {
	t.Parallel()
	for _, s := range []domain.PresenceStatus{
		domain.PresenceOnline, domain.PresenceAway,
		domain.PresenceOffline, domain.PresenceSleeping,
	} {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []domain.PresenceStatus{"", "stale", "xx"} {
		if s.IsValid() {
			t.Errorf("%q should be invalid", s)
		}
	}
}

// --- helpers ----------------------------------------------------------

func nowPtr() *time.Time {
	t := time.Now()
	return &t
}
