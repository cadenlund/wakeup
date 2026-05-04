package room_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/room"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// fakeAdmin records every RemoveParticipant call. The mutex makes
// it safe under -race; the sweeper itself is single-goroutine but
// the test harness shouldn't rely on that.
type fakeAdmin struct {
	mu      sync.Mutex
	calls   []fakeAdminCall
	nextErr error
}

type fakeAdminCall struct {
	Room     string
	Identity string
}

func (f *fakeAdmin) RemoveParticipant(_ context.Context, roomName, identity string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeAdminCall{Room: roomName, Identity: identity})
	return f.nextErr
}

func (f *fakeAdmin) Snapshot() []fakeAdminCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeAdminCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// loneKickStack is a minimal harness — same redis + room service as
// newStack, but with explicit LoneKickAfter + a fakeAdmin so we can
// drive PopDueLoneKicks and ExecuteLoneKick without an httptest server.
type loneKickStack struct {
	svc   *room.Service
	rdb   *redis.Client
	admin *fakeAdmin
	now   time.Time
}

func newLoneKickStack(t *testing.T, kickAfter time.Duration) *loneKickStack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	users := userrepo.New(pool)
	convs := convrepo.New(pool)
	convSvc, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convs, Users: users})
	if err != nil {
		t.Fatalf("convsvc.New: %v", err)
	}
	redisURL := testutil.StartRedis(t)
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })

	admin := &fakeAdmin{}
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	svc, err := room.New(room.Config{
		Convs: convSvc, Users: users,
		APIKey: "k", APISecret: "ssssssssssssssssssssssssssssssss",
		LiveKitURL: "ws://localhost:7880", Redis: rdb,
		Now:           func() time.Time { return now },
		LiveKitAdmin:  admin,
		LoneKickAfter: kickAfter,
	})
	if err != nil {
		t.Fatalf("room.New: %v", err)
	}
	return &loneKickStack{svc: svc, rdb: rdb, admin: admin, now: now}
}

// ScheduleLoneKick records a ZSET entry with the right deadline + a
// companion user_id key.
func TestScheduleLoneKick_RecordsEntry(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	ctx := context.Background()
	convID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	if err := st.svc.ScheduleLoneKick(ctx, convID, userID); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}

	score, err := st.rdb.ZScore(ctx, "room:lone_kicks_due", convID.String()).Result()
	if err != nil {
		t.Fatalf("ZScore: %v", err)
	}
	want := float64(st.now.Add(5 * time.Minute).Unix())
	if score != want {
		t.Errorf("score = %v, want %v", score, want)
	}
	got, err := st.rdb.Get(ctx, "room:"+convID.String()+":lone_user").Result()
	if err != nil {
		t.Fatalf("GET lone_user: %v", err)
	}
	if got != userID.String() {
		t.Errorf("lone_user = %q, want %q", got, userID)
	}
}

// Negative LoneKickAfter disables scheduling — assert this test's
// specific convID has no entry, regardless of other tests' state.
func TestScheduleLoneKick_DisabledWhenNegative(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, -time.Second)
	ctx := context.Background()
	convID := uuid.Must(uuid.NewV7())
	if err := st.svc.ScheduleLoneKick(ctx, convID, uuid.New()); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}
	if _, err := st.rdb.ZScore(ctx, "room:lone_kicks_due", convID.String()).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("expected no entry for convID %s, got err=%v", convID, err)
	}
}

// CancelLoneKick clears both the ZSET entry and the user-id key.
// Asserts on this test's specific convID rather than the queue size,
// since the test Redis is shared across the package's parallel
// tests and unrelated entries may be present.
func TestCancelLoneKick_RemovesEntry(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	ctx := context.Background()
	convID := uuid.Must(uuid.NewV7())

	if err := st.svc.ScheduleLoneKick(ctx, convID, uuid.New()); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}
	if err := st.svc.CancelLoneKick(ctx, convID); err != nil {
		t.Fatalf("CancelLoneKick: %v", err)
	}
	if _, err := st.rdb.ZScore(ctx, "room:lone_kicks_due", convID.String()).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("ZScore after cancel: err=%v (want redis.Nil)", err)
	}
	if _, err := st.rdb.Get(ctx, "room:"+convID.String()+":lone_user").Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("lone_user should be deleted, got err=%v", err)
	}
}

// CancelLoneKick on an unknown conv is a no-op.
func TestCancelLoneKick_Idempotent(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	if err := st.svc.CancelLoneKick(context.Background(), uuid.New()); err != nil {
		t.Errorf("CancelLoneKick on unknown conv: %v", err)
	}
}

// PopDueLoneKicks returns only entries whose deadline has passed, and
// atomically removes them so a second pop returns nothing. NOT
// t.Parallel(): the function drains the package's shared
// `room:lone_kicks_due` ZSET, so a parallel test's sweeper would
// race and steal the due entry before this test's assertion.
func TestPopDueLoneKicks_ReturnsOnlyDueEntriesAndIsAtomic(t *testing.T) {
	st := newLoneKickStack(t, 5*time.Minute)
	ctx := context.Background()

	dueConv := uuid.Must(uuid.NewV7())
	dueUser := uuid.Must(uuid.NewV7())
	pendingConv := uuid.Must(uuid.NewV7())
	pendingUser := uuid.Must(uuid.NewV7())

	if err := st.svc.ScheduleLoneKick(ctx, dueConv, dueUser); err != nil {
		t.Fatalf("schedule due: %v", err)
	}
	if err := st.svc.ScheduleLoneKick(ctx, pendingConv, pendingUser); err != nil {
		t.Fatalf("schedule pending: %v", err)
	}
	// Manually backdate the due entry — score = (st.now - 1s) so it
	// sorts before the cutoff.
	if _, err := st.rdb.ZAdd(ctx, "room:lone_kicks_due", redis.Z{
		Score: float64(st.now.Add(-time.Second).Unix()), Member: dueConv.String(),
	}).Result(); err != nil {
		t.Fatalf("ZADD backdate: %v", err)
	}

	got, err := st.svc.PopDueLoneKicks(ctx)
	if err != nil {
		t.Fatalf("PopDueLoneKicks: %v", err)
	}
	var foundDue bool
	var foundPending bool
	for _, k := range got {
		switch k.ConversationID {
		case dueConv:
			foundDue = true
			if k.UserID != dueUser {
				t.Errorf("due kick userID = %s, want %s", k.UserID, dueUser)
			}
		case pendingConv:
			foundPending = true
		}
	}
	if !foundDue {
		t.Errorf("due kick not in result: %+v", got)
	}
	if foundPending {
		t.Errorf("pending kick (deadline in future) appeared: %+v", got)
	}
	// Pending entry still there.
	if _, err := st.rdb.ZScore(ctx, "room:lone_kicks_due", pendingConv.String()).Result(); err != nil {
		t.Errorf("pending entry vanished: %v", err)
	}
	// Second pop doesn't re-fire our due kick (the ZSET entry is
	// gone). Other tests' entries may appear, so filter.
	again, err := st.svc.PopDueLoneKicks(ctx)
	if err != nil {
		t.Fatalf("PopDueLoneKicks second call: %v", err)
	}
	for _, k := range again {
		if k.ConversationID == dueConv {
			t.Errorf("due kick reappeared on second pop: %+v", k)
		}
	}
}

// ExecuteLoneKick calls the LiveKit admin RPC with the right room +
// identity shape.
func TestExecuteLoneKick_CallsAdminRPC(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	convID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	if err := st.svc.ExecuteLoneKick(context.Background(), room.LoneKick{
		ConversationID: convID, UserID: userID,
	}); err != nil {
		t.Fatalf("ExecuteLoneKick: %v", err)
	}
	calls := st.admin.Snapshot()
	if len(calls) != 1 {
		t.Fatalf("admin calls = %d, want 1", len(calls))
	}
	if calls[0].Room != "conv:"+convID.String() {
		t.Errorf("Room = %q, want conv:%s", calls[0].Room, convID)
	}
	if calls[0].Identity != "user:"+userID.String() {
		t.Errorf("Identity = %q, want user:%s", calls[0].Identity, userID)
	}
}

// Sweeper end-to-end: schedule a kick, backdate it, run the sweeper,
// admin RPC fires for that kick. NOT t.Parallel(): the sweeper
// drains the shared ZSET; a parallel test would steal the entry.
func TestLoneKickSweeper_FiresDueKicks(t *testing.T) {
	st := newLoneKickStack(t, 5*time.Minute)
	ctx := context.Background()
	convID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	if err := st.svc.ScheduleLoneKick(ctx, convID, userID); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}
	// Backdate to make it due.
	if _, err := st.rdb.ZAdd(ctx, "room:lone_kicks_due", redis.Z{
		Score: float64(st.now.Add(-time.Second).Unix()), Member: convID.String(),
	}).Result(); err != nil {
		t.Fatalf("ZADD backdate: %v", err)
	}

	sweeper, err := room.NewLoneKickSweeper(st.svc, nil, 0)
	if err != nil {
		t.Fatalf("NewLoneKickSweeper: %v", err)
	}
	if err := sweeper.Run(ctx); err != nil {
		t.Fatalf("sweeper.Run: %v", err)
	}
	wantIdentity := "user:" + userID.String()
	matches := 0
	for _, c := range st.admin.Snapshot() {
		if c.Identity == wantIdentity && c.Room == "conv:"+convID.String() {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("admin calls matching {%s, %s} = %d, want 1; full=%+v",
			"conv:"+convID.String(), wantIdentity, matches, st.admin.Snapshot())
	}
}

// Sweeper logs and continues when the admin RPC errors. The kick
// still fires (admin sees the call) but Run returns nil so the
// runner doesn't treat it as a tick failure. NOT t.Parallel() —
// same shared-ZSET reason as TestPopDueLoneKicks above.
func TestLoneKickSweeper_LogsAndContinuesOnAdminError(t *testing.T) {
	st := newLoneKickStack(t, 5*time.Minute)
	st.admin.nextErr = errors.New("livekit transient error")
	ctx := context.Background()
	convID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	if err := st.svc.ScheduleLoneKick(ctx, convID, userID); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}
	if _, err := st.rdb.ZAdd(ctx, "room:lone_kicks_due", redis.Z{
		Score: float64(st.now.Add(-time.Second).Unix()), Member: convID.String(),
	}).Result(); err != nil {
		t.Fatalf("ZADD backdate: %v", err)
	}

	sweeper, err := room.NewLoneKickSweeper(st.svc, nil, 0)
	if err != nil {
		t.Fatalf("NewLoneKickSweeper: %v", err)
	}
	if err := sweeper.Run(ctx); err != nil {
		t.Errorf("sweeper.Run: %v (should swallow per-kick errors)", err)
	}
	wantIdentity := "user:" + userID.String()
	saw := false
	for _, c := range st.admin.Snapshot() {
		if c.Identity == wantIdentity {
			saw = true
		}
	}
	if !saw {
		t.Errorf("admin RPC was not called for our kick despite the queued entry")
	}
}

// Sweeper sanity checks
func TestNewLoneKickSweeper_RejectsNilSvc(t *testing.T) {
	t.Parallel()
	if _, err := room.NewLoneKickSweeper(nil, nil, 0); err == nil {
		t.Error("expected error for nil Svc")
	}
}

func TestLoneKickSweeper_NameAndIntervalDefaults(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	s, err := room.NewLoneKickSweeper(st.svc, nil, 0)
	if err != nil {
		t.Fatalf("NewLoneKickSweeper: %v", err)
	}
	if s.Name() != "lone-kick-sweeper" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.Interval() != room.LoneKickSweeperInterval {
		t.Errorf("Interval() = %v, want %v (default)", s.Interval(), room.LoneKickSweeperInterval)
	}
}

func TestLoneKickSweeper_HonorsCustomInterval(t *testing.T) {
	t.Parallel()
	st := newLoneKickStack(t, 5*time.Minute)
	s, err := room.NewLoneKickSweeper(st.svc, nil, 90*time.Second)
	if err != nil {
		t.Fatalf("NewLoneKickSweeper: %v", err)
	}
	if s.Interval() != 90*time.Second {
		t.Errorf("Interval() = %v, want 90s", s.Interval())
	}
}

// LiveKitAdmin constructor rejects empty inputs.
func TestNewLiveKitAdmin_RejectsEmptyInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                   string
		url, apiKey, apiSecret string
	}{
		{"empty url", "", "k", "s"},
		{"empty key", "ws://x", "", "s"},
		{"empty secret", "ws://x", "k", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := room.NewLiveKitAdmin(tc.url, tc.apiKey, tc.apiSecret); err == nil {
				t.Error("expected error")
			}
		})
	}
}

// LiveKitAdmin happy construction succeeds.
func TestNewLiveKitAdmin_HappyPath(t *testing.T) {
	t.Parallel()
	c, err := room.NewLiveKitAdmin("ws://localhost:7880", "k", "ssssssssssssssssssssssssssssssss")
	if err != nil {
		t.Fatalf("NewLiveKitAdmin: %v", err)
	}
	if c == nil {
		t.Fatal("client is nil")
	}
}
