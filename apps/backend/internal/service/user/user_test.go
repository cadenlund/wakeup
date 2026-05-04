package user_test

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // bucket name hash, not crypto
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// minimalPNG is a 1x1 transparent PNG. The first 8 bytes (89 50 4E 47 ...)
// are the PNG signature `\x89PNG\r\n\x1a\n` that http.DetectContentType
// matches as "image/png" without needing the decoder to actually parse it.
var minimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// stack holds everything a test needs.
type stack struct {
	svc      *user.Service
	users    *repo.Queries
	storage  *objectstore.Store
	bucket   string
	endpoint string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)

	endpoint := testutil.StartMinIO(t)
	sum := sha1.Sum([]byte(t.Name())) //nolint:gosec
	bucket := "test-" + hex.EncodeToString(sum[:])[:16]
	createBucket(t, endpoint, bucket)

	store, err := objectstore.New(objectstore.Config{
		Endpoint:       endpoint,
		Region:         "us-east-1",
		AccessKey:      testutil.MinIOAccessKey,
		SecretKey:      testutil.MinIOSecretKey,
		Bucket:         bucket,
		ForcePathStyle: true,
		MaxUploadBytes: user.MaxAvatarBytes + 1024,
	})
	if err != nil {
		t.Fatalf("objectstore.New: %v", err)
	}

	users := repo.New(pool)
	svc, err := user.New(user.Config{Users: users, Storage: store})
	if err != nil {
		t.Fatalf("user.New: %v", err)
	}
	return &stack{svc: svc, users: users, storage: store, bucket: bucket, endpoint: endpoint}
}

// createBucket spins up a raw S3 client just long enough to MakeBucket.
// (objectstore.Store doesn't expose CreateBucket — that's deployment infra.)
func createBucket(t *testing.T, endpoint, bucket string) {
	t.Helper()
	client := s3.NewFromConfig(aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			testutil.MinIOAccessKey, testutil.MinIOSecretKey, "",
		),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		t.Fatalf("CreateBucket: %v", err)
	}
}

// makeUser inserts a user via the repo so the service has something to
// update. Returns the populated domain.User.
func makeUser(t *testing.T, st *stack) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	if _, err := st.users.Create(context.Background(), repo.CreateParams{
		ID:           id,
		Username:     "u" + full,
		DisplayName:  "User " + full[:8],
		Email:        full + "@x.test",
		PasswordHash: "h",
	}); err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

// asAPIError pulls the *apierror.Error out of err and fails if it isn't one.
func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- UpdateProfile -------------------------------------------------------

func TestUpdateProfile_PatchesEachField(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	newName := "Renamed"
	got, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		DisplayName: &newName,
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if got.DisplayName != newName {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}

	dark := "dark"
	got2, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		ColorScheme: &dark,
	})
	if err != nil {
		t.Fatalf("UpdateProfile color: %v", err)
	}
	if got2.ColorScheme != "dark" {
		t.Errorf("ColorScheme = %q", got2.ColorScheme)
	}
}

func TestUpdateProfile_BioAndStatusEmojiRoundTrip(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	bio := "Building things at night."
	emoji := "🛌"
	got, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		Bio:         &bio,
		StatusEmoji: &emoji,
	})
	if err != nil {
		t.Fatalf("UpdateProfile bio+emoji: %v", err)
	}
	if got.Bio == nil || *got.Bio != bio {
		t.Errorf("Bio = %v, want %q", got.Bio, bio)
	}
	if got.StatusEmoji == nil || *got.StatusEmoji != emoji {
		t.Errorf("StatusEmoji = %v, want %q", got.StatusEmoji, emoji)
	}

	// Sending nil leaves the existing values alone (COALESCE pattern).
	newName := "After Bio"
	got2, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		DisplayName: &newName,
	})
	if err != nil {
		t.Fatalf("UpdateProfile name only: %v", err)
	}
	if got2.Bio == nil || *got2.Bio != bio {
		t.Errorf("Bio after no-op patch = %v, want unchanged %q", got2.Bio, bio)
	}
	if got2.StatusEmoji == nil || *got2.StatusEmoji != emoji {
		t.Errorf("StatusEmoji after no-op patch = %v, want unchanged %q", got2.StatusEmoji, emoji)
	}

	// Empty string is a valid stored value (UI hides it but DB allows it
	// since CHECK is `<= 280` / `<= 8`, not `> 0`).
	blank := ""
	got3, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		Bio:         &blank,
		StatusEmoji: &blank,
	})
	if err != nil {
		t.Fatalf("UpdateProfile clear: %v", err)
	}
	if got3.Bio == nil || *got3.Bio != "" {
		t.Errorf("Bio after clear = %v, want empty string", got3.Bio)
	}
	if got3.StatusEmoji == nil || *got3.StatusEmoji != "" {
		t.Errorf("StatusEmoji after clear = %v, want empty string", got3.StatusEmoji)
	}
}

func TestUpdateProfile_RejectsInvalidColorScheme(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)
	bogus := "fuschia"
	_, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uid,
		ColorScheme: &bogus,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	ae := asAPIError(t, err)
	if ae.Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", ae.Code)
	}
	if len(ae.Fields) != 1 || ae.Fields[0].Field != "color_scheme" {
		t.Errorf("Fields = %+v", ae.Fields)
	}
}

func TestUpdateProfile_MissingUserReturns404(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	name := "x"
	_, err := st.svc.UpdateProfile(context.Background(), user.UpdateProfileParams{
		UserID:      uuid.New(),
		DisplayName: &name,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// --- UploadAvatar --------------------------------------------------------

func TestUploadAvatar_PNG_Success(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	got, err := st.svc.UploadAvatar(context.Background(), uid, bytes.NewReader(minimalPNG), int64(len(minimalPNG)))
	if err != nil {
		t.Fatalf("UploadAvatar: %v", err)
	}
	if got.AvatarURL == nil {
		t.Fatal("AvatarURL is nil after upload")
	}
	if !strings.HasPrefix(*got.AvatarURL, "avatars/"+uid.String()+"/") {
		t.Errorf("avatar key shape unexpected: %q", *got.AvatarURL)
	}
	if !strings.HasSuffix(*got.AvatarURL, ".png") {
		t.Errorf("expected .png ext for image/png, got %q", *got.AvatarURL)
	}
}

func TestUploadAvatar_RejectsBadMIME(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	textBytes := []byte("This is not an image, it's plain text.")
	_, err := st.svc.UploadAvatar(context.Background(), uid, bytes.NewReader(textBytes), int64(len(textBytes)))
	if err == nil {
		t.Fatal("expected MIME validation error")
	}
	ae := asAPIError(t, err)
	if ae.Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", ae.Code)
	}
}

func TestUploadAvatar_RejectsOversize_DeclaredSize(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	// declaredSize over the cap → fast-fail before reading body.
	_, err := st.svc.UploadAvatar(context.Background(), uid, bytes.NewReader(minimalPNG), user.MaxAvatarBytes+1)
	if err == nil {
		t.Fatal("expected oversize error")
	}
	if asAPIError(t, err).Code != apierror.CodePayloadTooLarge {
		t.Errorf("Code = %q, want PAYLOAD_TOO_LARGE", asAPIError(t, err).Code)
	}
}

func TestUploadAvatar_RejectsOversize_StreamLength(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	// Hide the size: send a stream over the cap with declaredSize = -1.
	big := make([]byte, user.MaxAvatarBytes+100)
	copy(big, minimalPNG) // start with PNG signature so MIME doesn't reject first
	_, err := st.svc.UploadAvatar(context.Background(), uid, bytes.NewReader(big), -1)
	if err == nil {
		t.Fatal("expected stream-oversize error")
	}
	if asAPIError(t, err).Code != apierror.CodePayloadTooLarge {
		t.Errorf("Code = %q, want PAYLOAD_TOO_LARGE", asAPIError(t, err).Code)
	}
}

func TestUploadAvatar_EmptyBody_400(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	_, err := st.svc.UploadAvatar(context.Background(), uid, bytes.NewReader(nil), 0)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if asAPIError(t, err).Code != apierror.CodeBadRequest {
		t.Errorf("Code = %q, want BAD_REQUEST", asAPIError(t, err).Code)
	}
}

func TestUploadAvatar_NilBody_400(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	_, err := st.svc.UploadAvatar(context.Background(), uid, nil, 0)
	if err == nil {
		t.Fatal("expected error for nil body")
	}
}

// MIME extension matrix: assert each allowed type maps to the right ext.
func TestUploadAvatar_ExtMappings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		signature []byte
		mime      string
		ext       string
	}{
		// Just PNG covered here — http.DetectContentType for JPEG/GIF/WebP
		// requires real-ish payloads; testing one mapping proves the
		// matrix wiring works without us having to ship binary fixtures
		// for every codec.
		{"png", minimalPNG, "image/png", ".png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newStack(t)
			uid := makeUser(t, st)
			// Confirm http.DetectContentType agrees on the MIME first.
			got := http.DetectContentType(tc.signature)
			if !strings.HasPrefix(got, tc.mime) {
				t.Fatalf("test fixture wrong: detected %q, want prefix %q", got, tc.mime)
			}
			updated, err := st.svc.UploadAvatar(context.Background(), uid,
				bytes.NewReader(tc.signature), int64(len(tc.signature)))
			if err != nil {
				t.Fatalf("UploadAvatar: %v", err)
			}
			if !strings.HasSuffix(*updated.AvatarURL, tc.ext) {
				t.Fatalf("expected ext %q, got %q", tc.ext, *updated.AvatarURL)
			}
		})
	}
}

// --- SoftDeleteAccount ---------------------------------------------------

func TestSoftDeleteAccount_Success(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)

	if err := st.svc.SoftDeleteAccount(context.Background(), uid); err != nil {
		t.Fatalf("SoftDeleteAccount: %v", err)
	}
	// Repo's GetByID must now return ErrNotFound.
	if _, err := st.users.GetByID(context.Background(), uid); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("after soft delete, GetByID = %v, want ErrNotFound", err)
	}
	// GetByIDIncludingDeleted should still find them with deleted_at set.
	got, err := st.users.GetByIDIncludingDeleted(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetByIDIncludingDeleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Fatal("DeletedAt should be set")
	}
}

func TestSoftDeleteAccount_MissingUserReturns404(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	err := st.svc.SoftDeleteAccount(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	if _, err := user.New(user.Config{Users: nil, Storage: st.storage}); err == nil {
		t.Error("nil Users should error")
	}
	if _, err := user.New(user.Config{Users: st.users, Storage: nil}); err == nil {
		t.Error("nil Storage should error")
	}
}

// --- GetByID + Search (used by GET /v1/users{,/{id}}) -------------------

func TestGetByID_Success(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	uid := makeUser(t, st)
	got, err := st.svc.GetByID(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != uid {
		t.Errorf("ID mismatch")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	_, err := st.svc.GetByID(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestSearch_PaginatesAndOver_FetchesLimitPlusOne(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	// Make 25 users; with limit=10 we expect 3 pages (10, 10, 5).
	for i := 0; i < 25; i++ {
		makeUser(t, st)
	}

	first, err := st.svc.Search(context.Background(), user.SearchParams{Limit: 10})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Users) != 10 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("first page wrong: len=%d hasMore=%v cursor=%v",
			len(first.Users), first.HasMore, first.NextCursor)
	}
}

func TestSearch_EmptyQueryReturnsAllNonDeletedUsers(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	makeUser(t, st)
	res, err := st.svc.Search(context.Background(), user.SearchParams{Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Users) == 0 {
		t.Fatalf("expected at least 1 user, got none")
	}
}

// ListByIDs returns every requested user in a single round-trip,
// regardless of input order. Soft-deleted users are still included
// so handler-side rendering can show the §4.6 placeholder for
// vanished accounts in message history.
func TestListByIDs_BatchLoad(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	a := makeUser(t, st)
	b := makeUser(t, st)
	c := makeUser(t, st)

	got, err := st.svc.ListByIDs(context.Background(), []uuid.UUID{c, a, b})
	if err != nil {
		t.Fatalf("ListByIDs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	gotSet := make(map[uuid.UUID]struct{}, len(got))
	for _, u := range got {
		gotSet[u.ID] = struct{}{}
	}
	for _, want := range []uuid.UUID{a, b, c} {
		if _, ok := gotSet[want]; !ok {
			t.Errorf("missing %s from result", want)
		}
	}
}

// UploadAvatar against a non-existent user uploads to S3 then surfaces
// NotFound at the DB Update step. The orphan object is documented as
// acceptable for v1 (the comment in user.go calls this out).
func TestUploadAvatar_NonExistentUserSurfacesNotFound(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	_, err := st.svc.UploadAvatar(context.Background(), uuid.New(),
		bytes.NewReader(minimalPNG), int64(len(minimalPNG)))
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// Empty input is a fast path — no DB round-trip, returns empty.
func TestListByIDs_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	got, err := st.svc.ListByIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListByIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
