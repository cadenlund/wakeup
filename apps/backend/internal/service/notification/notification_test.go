package notification_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notification"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// stubPrefs returns whatever bool is set; tracks last call for assertions.
type stubPrefs struct {
	allow      bool
	lastUserID uuid.UUID
	lastCat    notificationpref.Category
	calls      int
}

func (s *stubPrefs) ShouldNotify(_ context.Context, userID uuid.UUID, cat notificationpref.Category) bool {
	s.calls++
	s.lastUserID = userID
	s.lastCat = cat
	return s.allow
}

// stubDevices returns the configured slice / err. Doesn't touch a DB.
type stubDevices struct {
	tokens []domain.DeviceToken
	err    error
	calls  int
}

func (s *stubDevices) ListByUser(_ context.Context, _ uuid.UUID) ([]domain.DeviceToken, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.tokens, nil
}

func newSvc(t *testing.T, prefs *stubPrefs, devs *stubDevices, pusher pushnotif.Pusher) *notification.Service {
	t.Helper()
	svc, err := notification.New(notification.Config{
		Prefs: prefs, Devices: devs, Pusher: pusher,
	})
	if err != nil {
		t.Fatalf("notification.New: %v", err)
	}
	return svc
}

// --- behaviour matrix ----------------------------------------------------

func TestSendOfflinePush_PrefOffIsNoOp(t *testing.T) {
	t.Parallel()
	prefs := &stubPrefs{allow: false}
	devs := &stubDevices{tokens: []domain.DeviceToken{{ExpoToken: "ExponentPushToken[a]"}}}
	pusher := &testutil.FakePusher{}
	svc := newSvc(t, prefs, devs, pusher)

	err := svc.SendOfflinePush(context.Background(), uuid.New(),
		notificationpref.CategoryDirectMessages,
		pushnotif.Notification{Title: "T", Body: "B"},
		nil,
	)
	if err != nil {
		t.Fatalf("SendOfflinePush: %v", err)
	}
	if devs.calls != 0 {
		t.Errorf("ListByUser should not be called when pref is off")
	}
	if len(pusher.Sent) != 0 {
		t.Errorf("Pusher.Send should not be called when pref is off; sent=%d", len(pusher.Sent))
	}
}

func TestSendOfflinePush_PrefOnAndDevicesPushes(t *testing.T) {
	t.Parallel()
	uid := uuid.New()
	prefs := &stubPrefs{allow: true}
	devs := &stubDevices{tokens: []domain.DeviceToken{
		{ExpoToken: "ExponentPushToken[a]", Platform: domain.DeviceIOS},
		{ExpoToken: "ExponentPushToken[b]", Platform: domain.DeviceAndroid},
	}}
	pusher := &testutil.FakePusher{}
	svc := newSvc(t, prefs, devs, pusher)

	payload := pushnotif.Notification{
		Title: "Hello", Body: "World", Data: map[string]any{"type": "message"},
	}
	if err := svc.SendOfflinePush(context.Background(), uid,
		notificationpref.CategoryDirectMessages, payload, nil); err != nil {
		t.Fatalf("SendOfflinePush: %v", err)
	}

	if prefs.calls != 1 || prefs.lastUserID != uid ||
		prefs.lastCat != notificationpref.CategoryDirectMessages {
		t.Errorf("ShouldNotify not called with expected args: %+v", prefs)
	}
	if len(pusher.Sent) != 1 {
		t.Fatalf("expected 1 push, got %d", len(pusher.Sent))
	}
	got := pusher.Sent[0]
	if got.Title != "Hello" || got.Body != "World" {
		t.Errorf("payload mismatch: %+v", got)
	}
	if len(got.Tokens) != 2 ||
		got.Tokens[0] != "ExponentPushToken[a]" ||
		got.Tokens[1] != "ExponentPushToken[b]" {
		t.Errorf("token fan-out wrong: %+v", got.Tokens)
	}
	if got.Data["type"] != "message" {
		t.Errorf("data not propagated: %+v", got.Data)
	}
}

func TestSendOfflinePush_NoDevicesIsNoErrorAndNoPush(t *testing.T) {
	t.Parallel()
	prefs := &stubPrefs{allow: true}
	devs := &stubDevices{tokens: nil} // brand-new user, no mobile install
	pusher := &testutil.FakePusher{}
	svc := newSvc(t, prefs, devs, pusher)

	if err := svc.SendOfflinePush(context.Background(), uuid.New(),
		notificationpref.CategoryFriendRequests,
		pushnotif.Notification{Title: "T", Body: "B"},
		nil,
	); err != nil {
		t.Fatalf("SendOfflinePush: %v", err)
	}
	if len(pusher.Sent) != 0 {
		t.Errorf("expected zero pushes for user with no devices, got %d", len(pusher.Sent))
	}
}

// --- error paths ---------------------------------------------------------

func TestSendOfflinePush_ListErrorPropagates(t *testing.T) {
	t.Parallel()
	prefs := &stubPrefs{allow: true}
	devs := &stubDevices{err: errors.New("boom")}
	pusher := &testutil.FakePusher{}
	svc := newSvc(t, prefs, devs, pusher)

	err := svc.SendOfflinePush(context.Background(), uuid.New(),
		notificationpref.CategoryCalls,
		pushnotif.Notification{Title: "T", Body: "B"},
		nil,
	)
	if err == nil {
		t.Fatal("expected error from ListByUser failure")
	}
	if len(pusher.Sent) != 0 {
		t.Errorf("Pusher.Send should not run when ListByUser failed")
	}
}

// failingPusher returns the configured error from Send.
type failingPusher struct{ err error }

func (p *failingPusher) Send(_ context.Context, _ []string, _ pushnotif.Notification) error {
	return p.err
}

func TestSendOfflinePush_PushErrorWrapsAndReturns(t *testing.T) {
	t.Parallel()
	prefs := &stubPrefs{allow: true}
	devs := &stubDevices{tokens: []domain.DeviceToken{{ExpoToken: "ExponentPushToken[a]"}}}
	pusher := &failingPusher{err: errors.New("expo down")}
	svc := newSvc(t, prefs, devs, pusher)

	err := svc.SendOfflinePush(context.Background(), uuid.New(),
		notificationpref.CategoryGroupMessages,
		pushnotif.Notification{Title: "T", Body: "B"},
		nil,
	)
	if err == nil {
		t.Fatal("expected error from Pusher.Send failure")
	}
	if !errors.Is(err, pusher.err) {
		t.Errorf("returned error should wrap underlying push error; got %v", err)
	}
}

// --- New() validation ---------------------------------------------------

func TestNew_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	cases := map[string]notification.Config{
		"missing prefs":   {Devices: &stubDevices{}, Pusher: &testutil.FakePusher{}},
		"missing devices": {Prefs: &stubPrefs{}, Pusher: &testutil.FakePusher{}},
		"missing pusher":  {Prefs: &stubPrefs{}, Devices: &stubDevices{}},
		"all empty":       {},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := notification.New(cfg); err == nil {
				t.Error("expected error")
			}
		})
	}
}
