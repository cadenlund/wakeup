package presence_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	presrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/presence"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/presence"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// fakeFriends is the FriendLister stub. Per-userID lists let each
// test set up arbitrary friend graphs without the real friend repo.
type fakeFriends struct {
	byUser map[uuid.UUID][]uuid.UUID
	err    error
}

func (f *fakeFriends) ListAcceptedFriendIDs(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byUser[userID], nil
}

type stack struct {
	svc     *presence.Service
	repo    *presrepo.Queries
	broker  pubsub.Broker
	friends *fakeFriends
	pool    *pgxpool.Pool
}

func newStack(t *testing.T, opts ...func(*presence.Config)) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	repo := presrepo.New(pool)
	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })
	friends := &fakeFriends{byUser: map[uuid.UUID][]uuid.UUID{}}

	cfg := presence.Config{
		Repo: repo, Broker: broker, Friends: friends,
		// Tighten cutoffs for tests so we don't have to wait minutes.
		OnlineCutoff:  100 * time.Millisecond,
		AwayCutoff:    300 * time.Millisecond,
		SweepInterval: time.Hour, // we call Run directly
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	svc, err := presence.New(cfg)
	if err != nil {
		t.Fatalf("presence.New: %v", err)
	}
	return &stack{svc: svc, repo: repo, broker: broker, friends: friends, pool: pool}
}

func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

// subscribePresence subscribes to friendID's user channel and returns
// a drain function that pulls every presence.update arriving within d
// after the call. Subscribe MUST happen before publishing — InProc
// (and Redis) drops messages with no live subscriber.
func subscribePresence(t *testing.T, broker pubsub.Broker, friendID uuid.UUID) func(d time.Duration) []wsproto.PresenceUpdatePayload {
	t.Helper()
	ch, err := broker.Subscribe(context.Background(), "user:"+friendID.String()+":events")
	if err != nil {
		t.Fatalf("broker.Subscribe: %v", err)
	}
	return func(d time.Duration) []wsproto.PresenceUpdatePayload {
		t.Helper()
		deadline := time.After(d)
		var got []wsproto.PresenceUpdatePayload
		for {
			select {
			case <-deadline:
				return got
			case msg, ok := <-ch:
				if !ok {
					return got
				}
				env, err := wsproto.Decode(msg.Payload)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if env.Type != wsproto.EventPresenceUpdate {
					continue
				}
				var p wsproto.PresenceUpdatePayload
				if err := wsproto.UnmarshalData(env, &p); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				got = append(got, p)
			}
		}
	}
}

// --- Heartbeat -------------------------------------------------------

func TestHeartbeat_FreshUserPublishesToFriends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	if err := st.svc.Heartbeat(ctx, uid); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	got := drain(100 * time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	if got[0].UserID != uid {
		t.Errorf("UserID = %v, want %v", got[0].UserID, uid)
	}
	if got[0].Status != "online" {
		t.Errorf("Status = %q, want online", got[0].Status)
	}
}

// Heartbeat that doesn't change status (online → online) must NOT
// publish. Avoids spam during normal liveness pings.
func TestHeartbeat_NoStatusChangeDoesNotPublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	if err := st.svc.Heartbeat(ctx, uid); err != nil {
		t.Fatalf("first: %v", err)
	}
	// First call publishes (offline→online). Second is noop status-wise.
	if err := st.svc.Heartbeat(ctx, uid); err != nil {
		t.Fatalf("second: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 1 {
		t.Errorf("got %d updates, want 1 (only the first should publish)", len(got))
	}
}

// --- SetStatus -------------------------------------------------------

func TestSetStatus_ValidatesValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	err := st.svc.SetStatus(ctx, uid, domain.PresenceStatus("bogus").Ptr())
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T, want *apierror.Error", err)
	}
	if ae.Code != apierror.CodeValidation {
		t.Errorf("code = %q, want VALIDATION_FAILED", ae.Code)
	}
}

func TestSetStatus_PublishesOnChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 1 || got[0].Status != "sleeping" {
		t.Errorf("got %+v, want one sleeping update", got)
	}
}

func TestSetStatus_DNDPersistsIntentAndStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	if err := st.svc.SetStatus(ctx, uid, domain.PresenceDND.Ptr()); err != nil {
		t.Fatalf("SetStatus dnd: %v", err)
	}
	row, err := st.repo.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != domain.PresenceDND {
		t.Errorf("status = %q, want dnd", row.Status)
	}
	if row.Intent == nil || *row.Intent != domain.PresenceDND {
		t.Errorf("intent = %v, want sticky dnd", row.Intent)
	}
}

func TestSetStatus_NilClearsIntentAndDefaultsToOnline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	if err := st.svc.SetStatus(ctx, uid, domain.PresenceDND.Ptr()); err != nil {
		t.Fatalf("seed dnd: %v", err)
	}
	if err := st.svc.SetStatus(ctx, uid, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	row, err := st.repo.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Intent != nil {
		t.Errorf("intent = %v, want nil after clear", row.Intent)
	}
	if row.Status != domain.PresenceOnline {
		t.Errorf("status = %q, want online (default after clear)", row.Status)
	}
}

func TestSetStatus_RejectsOfflineIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	err := st.svc.SetStatus(ctx, uid, domain.PresenceOffline.Ptr())
	if err == nil {
		t.Fatal("expected error — offline isn't a valid intent")
	}
	var ae *apierror.Error
	if !errors.As(err, &ae) || ae.Code != apierror.CodeValidation {
		t.Errorf("err = %v, want VALIDATION_FAILED", err)
	}
}

func TestSetStatus_DoesNotPublishOnNoOpChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("second: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 1 {
		t.Errorf("got %d updates, want 1 (no-op should not republish)", len(got))
	}
}

// --- §7.2 friends-only fan-out --------------------------------------

func TestPublish_NonFriendDoesNotReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	stranger := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, stranger)
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 0 {
		t.Errorf("stranger received %d updates, want 0", len(got))
	}
}

// --- Get + ListForUsers ----------------------------------------------

func TestGet_MissingUserRendersOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := uuid.Must(uuid.NewV7())
	got, err := st.svc.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.PresenceOffline {
		t.Errorf("Status = %q, want offline", got.Status)
	}
	if got.UserID != uid {
		t.Errorf("UserID = %v, want %v", got.UserID, uid)
	}
}

func TestListForUsers_FillsMissingAsOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool)
	b := makeUser(ctx, t, st.pool)
	missing := uuid.Must(uuid.NewV7())

	if err := st.svc.SetStatus(ctx, a, domain.PresenceOnline.Ptr()); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := st.svc.SetStatus(ctx, b, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	got, err := st.svc.ListForUsers(ctx, []uuid.UUID{a, b, missing})
	if err != nil {
		t.Fatalf("ListForUsers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	statuses := map[uuid.UUID]domain.PresenceStatus{}
	for _, p := range got {
		statuses[p.UserID] = p.Status
	}
	if statuses[a] != domain.PresenceOnline {
		t.Errorf("a status = %q", statuses[a])
	}
	if statuses[b] != domain.PresenceSleeping {
		t.Errorf("b status = %q", statuses[b])
	}
	if statuses[missing] != domain.PresenceOffline {
		t.Errorf("missing status = %q, want offline", statuses[missing])
	}
}

// --- Decay sweeper (job.Job.Run) -------------------------------------

func TestRun_DemotesAndPublishes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	// Seed via the repo with intent=nil so the decay sweeper is allowed
	// to demote — service.SetStatus would set intent='online' (sticky)
	// which by design is exempt from decay.
	if _, err := st.repo.SetStatus(ctx, uid, domain.PresenceOnline, nil); err != nil {
		t.Fatalf("seed online: %v", err)
	}

	// Wait past the 100ms onlineCutoff so the sweeper demotes.
	time.Sleep(150 * time.Millisecond)
	if err := st.svc.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 1 || got[0].Status != "away" {
		t.Errorf("got %+v, want one away update", got)
	}
}

// Counterpart: when intent is set, the sweeper does NOT touch the row.
// This is what makes DND survive backgrounding without resetting.
func TestRun_SkipsRowsWithStickyIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)
	friend := uuid.Must(uuid.NewV7())
	st.friends.byUser[uid] = []uuid.UUID{friend}

	drain := subscribePresence(t, st.broker, friend)
	// Service.SetStatus sets intent='online' too. Sticky.
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceOnline.Ptr()); err != nil {
		t.Fatalf("seed online with intent: %v", err)
	}
	_ = drain(50 * time.Millisecond) // drain the offline→online publish

	time.Sleep(150 * time.Millisecond)
	if err := st.svc.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := drain(100 * time.Millisecond)
	if len(got) != 0 {
		t.Errorf("got %+v, want no demotion (intent is sticky)", got)
	}
	// Confirm the row still says online.
	row, err := st.repo.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != domain.PresenceOnline {
		t.Errorf("status = %q, want online (sticky)", row.Status)
	}
}

func TestRun_NoStaleNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	if err := st.svc.Run(ctx); err != nil {
		t.Fatalf("Run on empty table: %v", err)
	}
}

// --- Job interface conformance ---------------------------------------

func TestJob_Identity(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	if st.svc.Name() != "presence-decay-sweeper" {
		t.Errorf("Name = %q", st.svc.Name())
	}
	if st.svc.Interval() <= 0 {
		t.Errorf("Interval = %v, want > 0", st.svc.Interval())
	}
}

// --- Config validation -----------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := presence.New(presence.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}

func TestNew_RejectsNegativeDurations(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	repo := presrepo.New(pool)
	friends := &fakeFriends{byUser: map[uuid.UUID][]uuid.UUID{}}
	for _, tc := range []struct {
		name string
		cfg  presence.Config
	}{
		{"OnlineCutoff", presence.Config{Repo: repo, Friends: friends, OnlineCutoff: -1 * time.Second}},
		{"AwayCutoff", presence.Config{Repo: repo, Friends: friends, AwayCutoff: -1 * time.Second}},
		{"SweepInterval", presence.Config{Repo: repo, Friends: friends, SweepInterval: -1 * time.Second}},
	} {
		if _, err := presence.New(tc.cfg); err == nil {
			t.Errorf("negative %s should error (CodeRabbit PR #52 fail-fast)", tc.name)
		}
	}
}

// --- Friend lookup error path on publish -----------------------------

// Friend lookup failure on publish should not block the state change
// (we already persisted). It logs and moves on.
func TestPublish_FriendLookupFailureIsNotFatal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	st.friends.err = errors.New("boom")
	uid := makeUser(ctx, t, st.pool)
	if err := st.svc.SetStatus(ctx, uid, domain.PresenceSleeping.Ptr()); err != nil {
		t.Fatalf("SetStatus should still succeed even when fan-out fails: %v", err)
	}
	got, err := st.svc.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.PresenceSleeping {
		t.Errorf("Status = %q, want sleeping", got.Status)
	}
}
