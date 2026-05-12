// Constructor validation tests for the handler types — every NewXxxHandler
// rejects nil deps with a distinct sentinel error. Production wiring in
// cmd/server is always all-non-nil, but the §13.8 audit picks up the
// uncovered guard branches without these tests, and a future refactor
// that drops one of the nil-checks gets caught by name here.
package httpapi_test

import (
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/validator/v10"

	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	adminsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/admin"
	attachsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	authsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	devicesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/device"
	friendsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	msgsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/message"
	notifprefsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	presencesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/presence"
	roomsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/room"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// We never actually construct working services here — every test
// expects an error before the handler reaches a real method. Using
// (*Type)(nil)-typed pointers tripped on the typed-nil-as-interface
// gotcha we hit in main.go, but each handler ctor's nil-check is a
// straight ==-comparison against a typed pointer, so we use empty
// non-nil structs as the "valid" placeholders and explicit nil for
// the dep being asserted as missing.
var (
	stubAdmin     = &adminsvc.Service{}
	stubAttach    = &attachsvc.Service{}
	stubAuth      = &authsvc.Service{}
	stubConvs     = &convsvc.Service{}
	stubDevice    = &devicesvc.Service{}
	stubFriends   = &friendsvc.Service{}
	stubMessages  = &msgsvc.Service{}
	stubNotifPref = &notifprefsvc.Service{}
	stubPresence  = &presencesvc.Service{}
	stubRoom      = &roomsvc.Service{}
	stubUsers     = &usersvc.Service{}
)

func newSession() *scs.SessionManager { return scs.New() }
func newValidator() *validator.Validate {
	return validator.New(validator.WithRequiredStructEnabled())
}

func TestNewAuthHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewAuthHandler(nil, nil, v, nil); err == nil {
		t.Error("nil svc: expected error")
	}
	if _, err := httpapi.NewAuthHandler(stubAuth, nil, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewAdminHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	sess := newSession()
	v := newValidator()
	if _, err := httpapi.NewAdminHandler(nil, stubAuth, sess, v, nil); err == nil {
		t.Error("nil admin: expected error")
	}
	if _, err := httpapi.NewAdminHandler(stubAdmin, nil, sess, v, nil); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewAdminHandler(stubAdmin, stubAuth, nil, v, nil); err == nil {
		t.Error("nil sessions: expected error")
	}
	if _, err := httpapi.NewAdminHandler(stubAdmin, stubAuth, sess, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewAttachmentHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := httpapi.NewAttachmentHandler(nil, stubAuth); err == nil {
		t.Error("nil attachments: expected error")
	}
	if _, err := httpapi.NewAttachmentHandler(stubAttach, nil); err == nil {
		t.Error("nil auth: expected error")
	}
}

func TestNewConversationHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	// The unread counter is optional — a nil one must be accepted
	// (graceful degradation: unread_count just stays 0).
	if _, err := httpapi.NewConversationHandler(stubConvs, stubUsers, stubAuth, nil, v, nil); err != nil {
		t.Fatalf("nil unread counter should be allowed: %v", err)
	}
	if _, err := httpapi.NewConversationHandler(nil, stubUsers, stubAuth, nil, v, nil); err == nil {
		t.Error("nil convs: expected error")
	}
	if _, err := httpapi.NewConversationHandler(stubConvs, nil, stubAuth, nil, v, nil); err == nil {
		t.Error("nil users: expected error")
	}
	if _, err := httpapi.NewConversationHandler(stubConvs, stubUsers, nil, nil, v, nil); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewConversationHandler(stubConvs, stubUsers, stubAuth, nil, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewDeviceHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewDeviceHandler(nil, stubAuth, v); err == nil {
		t.Error("nil devices: expected error")
	}
	if _, err := httpapi.NewDeviceHandler(stubDevice, nil, v); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewDeviceHandler(stubDevice, stubAuth, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewFriendHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewFriendHandler(nil, stubUsers, stubAuth, v, nil); err == nil {
		t.Error("nil friends: expected error")
	}
	if _, err := httpapi.NewFriendHandler(stubFriends, nil, stubAuth, v, nil); err == nil {
		t.Error("nil users: expected error")
	}
	if _, err := httpapi.NewFriendHandler(stubFriends, stubUsers, nil, v, nil); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewFriendHandler(stubFriends, stubUsers, stubAuth, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewMessageHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewMessageHandler(nil, stubAuth, v); err == nil {
		t.Error("nil messages: expected error")
	}
	if _, err := httpapi.NewMessageHandler(stubMessages, nil, v); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewMessageHandler(stubMessages, stubAuth, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewPresenceHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewPresenceHandler(nil, stubUsers, stubAuth, v, nil); err == nil {
		t.Error("nil presence: expected error")
	}
	if _, err := httpapi.NewPresenceHandler(stubPresence, nil, stubAuth, v, nil); err == nil {
		t.Error("nil users: expected error")
	}
	if _, err := httpapi.NewPresenceHandler(stubPresence, stubUsers, nil, v, nil); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewPresenceHandler(stubPresence, stubUsers, stubAuth, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewRoomHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewRoomHandler(nil, stubAuth, v); err == nil {
		t.Error("nil rooms: expected error")
	}
	if _, err := httpapi.NewRoomHandler(stubRoom, nil, v); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewRoomHandler(stubRoom, stubAuth, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}

func TestNewUserHandler_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	v := newValidator()
	if _, err := httpapi.NewUserHandler(nil, stubAuth, stubNotifPref, v, nil); err == nil {
		t.Error("nil users: expected error")
	}
	if _, err := httpapi.NewUserHandler(stubUsers, nil, stubNotifPref, v, nil); err == nil {
		t.Error("nil auth: expected error")
	}
	if _, err := httpapi.NewUserHandler(stubUsers, stubAuth, nil, v, nil); err == nil {
		t.Error("nil prefs: expected error")
	}
	if _, err := httpapi.NewUserHandler(stubUsers, stubAuth, stubNotifPref, nil, nil); err == nil {
		t.Error("nil validator: expected error")
	}
}
