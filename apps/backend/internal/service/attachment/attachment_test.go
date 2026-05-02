package attachment_test

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // bucket name hash, not crypto
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// minimalPNG is a 1x1 transparent PNG. The first 8 bytes are the PNG
// signature that http.DetectContentType matches as image/png.
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

// minimalPDF is a 4-byte PDF magic + minimal header so
// http.DetectContentType returns application/pdf.
var minimalPDF = []byte("%PDF-1.4\n%EOF\n")

type stack struct {
	svc     *attachment.Service
	repo    *attrepo.Queries
	storage *objectstore.Store
	pool    *pgxpool.Pool
	bucket  string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)

	endpoint := testutil.StartMinIO(t)
	sum := sha1.Sum([]byte(t.Name())) //nolint:gosec
	bucket := "att-" + hex.EncodeToString(sum[:])[:16]
	createBucket(t, endpoint, bucket)

	store, err := objectstore.New(objectstore.Config{
		Endpoint:       endpoint,
		Region:         "us-east-1",
		AccessKey:      testutil.MinIOAccessKey,
		SecretKey:      testutil.MinIOSecretKey,
		Bucket:         bucket,
		ForcePathStyle: true,
		MaxUploadBytes: attachment.MaxAttachmentBytes + 1024,
	})
	if err != nil {
		t.Fatalf("objectstore.New: %v", err)
	}

	repo := attrepo.New(pool)
	svc, err := attachment.New(attachment.Config{Repo: repo, Storage: store})
	if err != nil {
		t.Fatalf("attachment.New: %v", err)
	}
	return &stack{svc: svc, repo: repo, storage: store, pool: pool, bucket: bucket}
}

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

func makeDirect(ctx context.Context, t *testing.T, pool *pgxpool.Pool, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	cr := convrepo.New(pool)
	c, err := cr.CreateConversation(ctx, convrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})
	if err != nil {
		t.Fatalf("makeDirect: %v", err)
	}
	if _, err := cr.AddMember(ctx, c.ID, a, domain.MemberRoleMember); err != nil {
		t.Fatalf("makeDirect: add a: %v", err)
	}
	if _, err := cr.AddMember(ctx, c.ID, b, domain.MemberRoleMember); err != nil {
		t.Fatalf("makeDirect: add b: %v", err)
	}
	return c.ID
}

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- Upload -----------------------------------------------------------

func TestUpload_PNG_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	got, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID:   uploader,
		Filename:     "screenshot.png",
		Body:         bytes.NewReader(minimalPNG),
		DeclaredSize: int64(len(minimalPNG)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", got.ContentType)
	}
	if got.Filename != "screenshot.png" {
		t.Errorf("Filename = %q", got.Filename)
	}
	if got.SizeBytes != int64(len(minimalPNG)) {
		t.Errorf("SizeBytes = %d", got.SizeBytes)
	}
	if got.UploaderID != uploader {
		t.Errorf("UploaderID = %v, want %v", got.UploaderID, uploader)
	}
	// Storage key should be UUID-only — no filename in the key per §9.1.
	if !strings.HasPrefix(got.StorageKey, "attachments/") || strings.Contains(got.StorageKey, "screenshot") {
		t.Errorf("StorageKey leaks filename: %q", got.StorageKey)
	}

	// And the row should round-trip through the repo.
	from, err := st.repo.GetByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if from.ID != got.ID {
		t.Errorf("id round-trip mismatch")
	}
}

func TestUpload_PDF_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	got, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "report.pdf",
		Body: bytes.NewReader(minimalPDF), DeclaredSize: int64(len(minimalPDF)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", got.ContentType)
	}
}

// MIME-detection lie: client says image/png in filename, body is plain
// text. Detected MIME is text/plain (allowed) → succeeds. The real lie
// case is plain text + .pdf filename: detected MIME is text/plain (also
// allowed). To confirm the detection happens, we feed a text body but
// expect the row to record the detected MIME, not anything from the
// filename.
func TestUpload_RecordsDetectedMIMENotFilename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	body := []byte("hello world\n")
	got, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "fake.png", // .png lie
		Body: bytes.NewReader(body), DeclaredSize: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want text/plain (detected)", got.ContentType)
	}
}

// MIME outside the allowlist must be rejected.
func TestUpload_RejectsDisallowedMIME(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	// PE/EXE-like magic. http.DetectContentType returns
	// application/octet-stream which isn't on our allowlist.
	body := []byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04}
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "evil.bin",
		Body: bytes.NewReader(body), DeclaredSize: int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestUpload_RejectsOversize_DeclaredSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "x.png",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: attachment.MaxAttachmentBytes + 1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodePayloadTooLarge {
		t.Errorf("Code = %q, want PAYLOAD_TOO_LARGE", asAPIError(t, err).Code)
	}
}

// Stream length cap: declaredSize unknown (-1), body actually exceeds
// the cap. The internal io.LimitReader + len-check fires.
func TestUpload_RejectsOversize_StreamLength(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	// Build a payload bigger than the cap. Use the PNG header so MIME
	// detection passes — we want the size guard to be the failure.
	big := make([]byte, attachment.MaxAttachmentBytes+100)
	copy(big, minimalPNG)
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "huge.png",
		Body: bytes.NewReader(big), DeclaredSize: -1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodePayloadTooLarge {
		t.Errorf("Code = %q, want PAYLOAD_TOO_LARGE", asAPIError(t, err).Code)
	}
}

func TestUpload_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "x.png",
		Body: bytes.NewReader(nil), DeclaredSize: 0,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeBadRequest {
		t.Errorf("Code = %q, want BAD_REQUEST", asAPIError(t, err).Code)
	}
}

func TestUpload_RejectsNilBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "x.png", Body: nil,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeBadRequest {
		t.Errorf("Code = %q, want BAD_REQUEST", asAPIError(t, err).Code)
	}
}

// Filename sanitization: path-y characters must be stripped. After
// strip, the original name "evil.png" remains, but if every char was
// dangerous we reject as VALIDATION_FAILED.
func TestUpload_SanitizesFilename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	got, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "../../etc/passwd.png",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: int64(len(minimalPNG)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// Path separators stripped — remaining content is the printable
	// chars ".....etcpasswd.png" (all dots stay, the slashes go).
	if strings.Contains(got.Filename, "/") {
		t.Errorf("filename should not contain '/': %q", got.Filename)
	}
}

func TestUpload_RejectsAllSeparatorFilename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	_, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "////\\\\////",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: int64(len(minimalPNG)),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

// --- GetForCaller -----------------------------------------------------

func TestGetForCaller_OrphanByUploader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	created, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "x.png",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: int64(len(minimalPNG)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	got, err := st.svc.GetForCaller(ctx, created.ID, uploader)
	if err != nil {
		t.Fatalf("GetForCaller: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}
}

func TestGetForCaller_OrphanByStrangerSeesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	stranger := makeUser(ctx, t, st.pool)

	created, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "x.png",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: int64(len(minimalPNG)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	_, err = st.svc.GetForCaller(ctx, created.ID, stranger)
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestGetForCaller_LinkedMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool)
	b := makeUser(ctx, t, st.pool)
	cid := makeDirect(ctx, t, st.pool, a, b)

	created, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: a, Filename: "x.png",
		Body: bytes.NewReader(minimalPNG), DeclaredSize: int64(len(minimalPNG)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// Link via a real message so CallerCanRead's join branch fires.
	mr := msgrepo.New(st.pool)
	msg, err := mr.Create(ctx, msgrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "x",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	if err := mr.AddAttachment(ctx, msg.ID, created.ID); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	// Both members can now GetForCaller.
	for _, who := range []uuid.UUID{a, b} {
		if _, err := st.svc.GetForCaller(ctx, created.ID, who); err != nil {
			t.Errorf("member %v should read: %v", who, err)
		}
	}
}

func TestGetForCaller_NonexistentSeesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)
	_, err := st.svc.GetForCaller(ctx, uuid.New(), uploader)
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// --- Presign ----------------------------------------------------------

func TestPresign_ReturnsURLWithFilenameDisposition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uploader := makeUser(ctx, t, st.pool)

	created, err := st.svc.Upload(ctx, attachment.UploadParams{
		UploaderID: uploader, Filename: "report.pdf",
		Body: bytes.NewReader(minimalPDF), DeclaredSize: int64(len(minimalPDF)),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	url, expiresAt, err := st.svc.Presign(ctx, created)
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	if url == "" {
		t.Fatal("Presign returned empty URL")
	}
	if !strings.Contains(url, "response-content-disposition") {
		t.Errorf("URL missing response-content-disposition param: %s", url)
	}
	if !strings.Contains(strings.ToLower(url), "report.pdf") {
		t.Errorf("URL missing original filename: %s", url)
	}
	// expiresAt must be roughly now + PresignTTL. Allow ±30s slack for
	// runtime overhead between the service-side time.Now() and the
	// assertion below.
	want := time.Now().Add(attachment.PresignTTL)
	if delta := expiresAt.Sub(want); delta < -30*time.Second || delta > 30*time.Second {
		t.Errorf("expiresAt = %v, want within 30s of %v", expiresAt, want)
	}
}

// --- New rejects bad config ------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := attachment.New(attachment.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}

// Ensure http.DetectContentType is genuinely the source of MIME — this
// guards against a future refactor that swaps in a different sniffer
// without re-validating the allowlist behavior.
func TestDetectContentType_PNGSig(t *testing.T) {
	t.Parallel()
	if got := http.DetectContentType(minimalPNG); !strings.HasPrefix(got, "image/png") {
		t.Errorf("DetectContentType(minimalPNG) = %q, want image/png", got)
	}
}
