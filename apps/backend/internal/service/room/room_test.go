package room_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/livekit/protocol/auth"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/room"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

const (
	testAPIKey    = "devkey"
	testAPISecret = "devsecretdevsecretdevsecret"
	testLKURL     = "ws://localhost:7880"
)

type stack struct {
	svc     *room.Service
	convSvc *convsvc.Service
	convs   *convrepo.Queries
	users   *userrepo.Queries
	rdb     *redis.Client
	pool    *pgxpool.Pool
	now     time.Time
}

func newStack(t *testing.T) *stack {
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

	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	svc, err := room.New(room.Config{
		Convs: convSvc, Users: users,
		APIKey: testAPIKey, APISecret: testAPISecret,
		LiveKitURL: testLKURL, Redis: rdb,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("room.New: %v", err)
	}
	return &stack{
		svc: svc, convSvc: convSvc,
		convs: convs, users: users, rdb: rdb, pool: pool, now: now,
	}
}

// makeUser inserts a user via the repo and stamps an avatar via
// Update so the §12.8.1 token_metadata test has a value to assert on.
func makeUser(ctx context.Context, t *testing.T, st *stack) domain.User {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	user, err := st.users.Create(ctx, userrepo.CreateParams{
		ID: id, Username: "u" + full, DisplayName: "User " + full[:6],
		Email: full + "@x.test", PasswordHash: "h",
	})
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	avatar := "https://wakeup.app/a/" + full + ".png"
	updated, err := st.users.Update(ctx, userrepo.UpdateParams{
		ID:        user.ID,
		AvatarURL: &avatar,
	})
	if err != nil {
		t.Fatalf("makeUser update avatar: %v", err)
	}
	return updated
}

func makeDirect(ctx context.Context, t *testing.T, st *stack, a, b domain.User) uuid.UUID {
	t.Helper()
	res, err := st.convSvc.Create(ctx, convsvc.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("makeDirect: %v", err)
	}
	return res.Conversation.ID
}

func makeGroup(ctx context.Context, t *testing.T, st *stack, creator domain.User, members []uuid.UUID) uuid.UUID {
	t.Helper()
	name := "Crew"
	res, err := st.convSvc.Create(ctx, convsvc.CreateParams{
		Type: domain.ConversationGroup, Creator: creator.ID, MemberIDs: members, Name: &name,
	})
	if err != nil {
		t.Fatalf("makeGroup: %v", err)
	}
	return res.Conversation.ID
}

// decodeGrants verifies the token signature with the dev secret and
// returns the LiveKit ClaimGrants. Using ParseAPIToken/Verify keeps
// us off a separate JWT lib + asserts the signature is valid in
// every test (catches a future code path that issues unsigned tokens).
func decodeGrants(t *testing.T, tok string) *auth.ClaimGrants {
	t.Helper()
	v, err := auth.ParseAPIToken(tok)
	if err != nil {
		t.Fatalf("ParseAPIToken: %v", err)
	}
	_, grants, err := v.Verify(testAPISecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return grants
}

// --- §12.8.1 token issuance ------------------------------------------

func TestJoin_TokenStructure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	got, err := st.svc.Join(ctx, a.ID, cid, false)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got.RoomID != "conv:"+cid.String() {
		t.Errorf("RoomID = %q", got.RoomID)
	}
	if got.LiveKitURL != testLKURL {
		t.Errorf("LiveKitURL = %q", got.LiveKitURL)
	}
	g := decodeGrants(t, got.LiveKitToken)
	if g.Video == nil {
		t.Fatal("video grant nil")
	}
	if g.Video.Room != "conv:"+cid.String() {
		t.Errorf("video.Room = %q", g.Video.Room)
	}
	if !g.Video.RoomJoin {
		t.Errorf("video.RoomJoin = false, want true")
	}
	if g.Video.CanPublish == nil || !*g.Video.CanPublish {
		t.Errorf("video.CanPublish = %v, want true", g.Video.CanPublish)
	}
	if g.Video.CanSubscribe == nil || !*g.Video.CanSubscribe {
		t.Errorf("video.CanSubscribe = %v, want true", g.Video.CanSubscribe)
	}
	if g.Video.CanPublishData == nil || !*g.Video.CanPublishData {
		t.Errorf("video.CanPublishData = %v, want true", g.Video.CanPublishData)
	}
	if len(g.Video.CanPublishSources) != 2 {
		t.Errorf("CanPublishSources = %v, want [microphone, camera]", g.Video.CanPublishSources)
	}
}

func TestJoin_TokenIdentityStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	got, err := st.svc.Join(ctx, a.ID, cid, false)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	g := decodeGrants(t, got.LiveKitToken)
	if g.Identity != "user:"+a.ID.String() {
		t.Errorf("identity = %q, want user:%v", g.Identity, a.ID)
	}
}

func TestJoin_TokenMetadataIncludesProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	got, err := st.svc.Join(ctx, a.ID, cid, true)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	g := decodeGrants(t, got.LiveKitToken)
	if g.Metadata == "" {
		t.Fatalf("metadata empty")
	}
	var meta room.MetadataPayload
	if err := json.Unmarshal([]byte(g.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.DisplayName != a.DisplayName {
		t.Errorf("display_name = %q, want %q", meta.DisplayName, a.DisplayName)
	}
	if meta.AvatarURL == nil || *meta.AvatarURL != *a.AvatarURL {
		t.Errorf("avatar_url = %v, want %v", meta.AvatarURL, a.AvatarURL)
	}
	if !meta.Video {
		t.Errorf("video = false, want true")
	}
}

// JoinResult.ExpiresAt equals the frozen now + TokenTTL. The token's
// embedded `exp - iat` claims also match.
func TestJoin_TokenTTLIsTenMinutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	got, err := st.svc.Join(ctx, a.ID, cid, false)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if delta := got.ExpiresAt.Sub(st.now); delta != room.TokenTTL {
		t.Errorf("ExpiresAt-now = %v, want %v", delta, room.TokenTTL)
	}
}

// video=true vs video=false must produce identical token PERMISSIONS;
// only the metadata flag flips. (§12.8.1 token_video_flag_propagated.)
func TestJoin_VideoFlagOnlyAffectsMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	tWithVideo, err := st.svc.Join(ctx, a.ID, cid, true)
	if err != nil {
		t.Fatalf("Join video=true: %v", err)
	}
	tWithoutVideo, err := st.svc.Join(ctx, a.ID, cid, false)
	if err != nil {
		t.Fatalf("Join video=false: %v", err)
	}
	gWith := decodeGrants(t, tWithVideo.LiveKitToken)
	gWithout := decodeGrants(t, tWithoutVideo.LiveKitToken)

	// Permissions identical: serialize the VideoGrant minus metadata
	// and compare bytes.
	withPerms, _ := json.Marshal(gWith.Video)
	withoutPerms, _ := json.Marshal(gWithout.Video)
	if string(withPerms) != string(withoutPerms) {
		t.Errorf("permissions differ between video=true/false:\n  with=%s\n  without=%s", withPerms, withoutPerms)
	}

	mWith, mWithout := mustParseMeta(t, gWith), mustParseMeta(t, gWithout)
	if !mWith.Video || mWithout.Video {
		t.Errorf("metadata.video not propagated: with=%v without=%v", mWith.Video, mWithout.Video)
	}
}

func mustParseMeta(t *testing.T, g *auth.ClaimGrants) room.MetadataPayload {
	t.Helper()
	var m room.MetadataPayload
	if err := json.Unmarshal([]byte(g.Metadata), &m); err != nil {
		t.Fatalf("metadata unmarshal: %v", err)
	}
	return m
}

// --- §12.8.2 authorization ------------------------------------------

func TestJoin_NonMemberForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	_, err := st.svc.Join(ctx, stranger.ID, cid, false)
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want *apierror.Error", err)
	}
	// Service.Get returns NotFound for non-members (§4.4 — never
	// Forbidden; no enumeration leak). Document the contract here so
	// a future change that flips it to 403 fails this test.
	if ae.Code != apierror.CodeNotFound {
		t.Errorf("code = %q, want RESOURCE_NOT_FOUND", ae.Code)
	}
}

func TestJoin_DirectMembersOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	for _, who := range []domain.User{a, b} {
		if _, err := st.svc.Join(ctx, who.ID, cid, false); err != nil {
			t.Errorf("Join(%v): %v", who.ID, err)
		}
	}
}

func TestJoin_GroupMemberOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	creator := makeUser(ctx, t, st)
	other := makeUser(ctx, t, st)
	cid := makeGroup(ctx, t, st, creator, []uuid.UUID{other.ID})
	if _, err := st.svc.Join(ctx, other.ID, cid, false); err != nil {
		t.Errorf("Join: %v", err)
	}
}

func TestJoin_RemovedMemberForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	creator := makeUser(ctx, t, st)
	other := makeUser(ctx, t, st)
	third := makeUser(ctx, t, st)
	cid := makeGroup(ctx, t, st, creator, []uuid.UUID{other.ID, third.ID})
	if err := st.convSvc.RemoveMember(ctx, creator.ID, cid, other.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	_, err := st.svc.Join(ctx, other.ID, cid, false)
	if err == nil {
		t.Fatal("expected error after removal")
	}
}

// --- Leave ------------------------------------------------------------

func TestLeave_MemberOK_NonMemberError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	if err := st.svc.Leave(ctx, a.ID, cid); err != nil {
		t.Errorf("member Leave: %v", err)
	}
	if err := st.svc.Leave(ctx, stranger.ID, cid); err == nil {
		t.Errorf("stranger Leave should error")
	}
}

// --- GetParticipants + helpers --------------------------------------

func TestGetParticipants_EmptyRoomMembershipGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	got, err := st.svc.GetParticipants(ctx, a.ID, cid)
	if err != nil {
		t.Fatalf("member GetParticipants: %v", err)
	}
	if len(got.Participants) != 0 {
		t.Errorf("expected empty participants: %+v", got.Participants)
	}
	if _, err := st.svc.GetParticipants(ctx, stranger.ID, cid); err == nil {
		t.Errorf("stranger GetParticipants should error")
	}
}

func TestRoomLifecycle_AddMarkSetVideoRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	added, err := st.svc.AddParticipant(ctx, cid, a.ID)
	if err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	if !added {
		t.Errorf("first AddParticipant returned added=false")
	}
	// Idempotent: re-add returns false (LiveKit's at-least-once
	// delivery means we may see the same event twice).
	again, err := st.svc.AddParticipant(ctx, cid, a.ID)
	if err != nil {
		t.Fatalf("AddParticipant again: %v", err)
	}
	if again {
		t.Errorf("re-AddParticipant returned added=true")
	}

	wasFirst, err := st.svc.MarkStarted(ctx, cid)
	if err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	if !wasFirst {
		t.Errorf("MarkStarted: wasFirst=false on initial call")
	}
	wasSecond, _ := st.svc.MarkStarted(ctx, cid)
	if wasSecond {
		t.Errorf("MarkStarted on already-started room returned wasFirst=true")
	}

	if err := st.svc.SetParticipantVideo(ctx, cid, a.ID, true); err != nil {
		t.Fatalf("SetParticipantVideo: %v", err)
	}

	got, err := st.svc.GetParticipants(ctx, a.ID, cid)
	if err != nil {
		t.Fatalf("GetParticipants: %v", err)
	}
	if len(got.Participants) != 1 {
		t.Fatalf("len = %d, want 1", len(got.Participants))
	}
	if got.Participants[0].UserID != a.ID || !got.Participants[0].Video {
		t.Errorf("participant = %+v, want a w/ video=true", got.Participants[0])
	}
	if !got.Participants[0].JoinedAt.Equal(st.now) {
		t.Errorf("JoinedAt = %v, want %v (frozen now)", got.Participants[0].JoinedAt, st.now)
	}
	if got.StartedAt == nil {
		t.Errorf("StartedAt nil after MarkStarted")
	}

	size, err := st.svc.RemoveParticipant(ctx, cid, a.ID)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}
	if size != 0 {
		t.Errorf("size after remove = %d, want 0", size)
	}
	emptyGot, _ := st.svc.GetParticipants(ctx, a.ID, cid)
	if len(emptyGot.Participants) != 0 || emptyGot.StartedAt != nil {
		t.Errorf("post-clean state = %+v", emptyGot)
	}
}

// --- Config validation ------------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	base := room.Config{
		Convs:      st.convSvc,
		Users:      st.users,
		APIKey:     "key",
		APISecret:  "secret",
		LiveKitURL: "ws://localhost:7880",
		Redis:      st.rdb,
	}
	cases := []struct {
		name string
		mod  func(*room.Config)
	}{
		{"missing convs", func(c *room.Config) { c.Convs = nil }},
		{"missing users", func(c *room.Config) { c.Users = nil }},
		{"missing api key", func(c *room.Config) { c.APIKey = "" }},
		{"missing api secret", func(c *room.Config) { c.APISecret = "" }},
		{"missing livekit url", func(c *room.Config) { c.LiveKitURL = "" }},
		{"missing redis", func(c *room.Config) { c.Redis = nil }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mod(&cfg)
			if _, err := room.New(cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}
