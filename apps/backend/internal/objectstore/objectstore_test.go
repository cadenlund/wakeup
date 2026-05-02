package objectstore_test

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // bucket-name hashing, not crypto
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// newStore returns a Store backed by the singleton MinIO container, with a
// per-test bucket so parallel tests don't tread on each other's keys.
//
// S3 bucket names are restricted: 3-63 lowercase chars, only [a-z0-9.-],
// no underscores. Hashing the test name keeps the bucket short and legal.
func newStore(t *testing.T, maxBytes int64) *objectstore.Store {
	t.Helper()
	endpoint := testutil.StartMinIO(t)
	sum := sha1.Sum([]byte(t.Name())) //nolint:gosec // not security-relevant
	bucket := "test-" + hex.EncodeToString(sum[:])[:16]

	cfg := objectstore.Config{
		Endpoint:       endpoint,
		Region:         "us-east-1",
		AccessKey:      testutil.MinIOAccessKey,
		SecretKey:      testutil.MinIOSecretKey,
		Bucket:         bucket,
		ForcePathStyle: true,
		MaxUploadBytes: maxBytes,
	}
	store, err := objectstore.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// MinIO does not auto-create the bucket; do it once here. CreateBucket
	// is idempotent (bucket-already-owned-by-you returns success on retry).
	rawClient := s3.NewFromConfig(aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			testutil.MinIOAccessKey, testutil.MinIOSecretKey, "",
		),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = rawClient.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		t.Fatalf("CreateBucket: %v", err)
	}

	return store
}

func TestPut_PresignGet_RoundTripWithContentDisposition(t *testing.T) {
	t.Parallel()
	store := newStore(t, 0)
	ctx := context.Background()

	const key = "attachments/conv-1/msg-1/abc"
	const ct = "application/pdf"
	const filename = `Q1 report.pdf`
	body := []byte("PDF-1.7\n%fake-pdf-bytes-for-test\n")

	if err := store.Put(ctx, key, ct, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	disposition := `attachment; filename="` + filename + `"`
	signed, err := store.PresignGet(ctx, key, time.Minute, disposition)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}

	// Sanity: the signed URL must include the response-content-disposition
	// query parameter so the SDK actually bound it.
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	gotDisp := u.Query().Get("response-content-disposition")
	if gotDisp != disposition {
		t.Errorf("response-content-disposition = %q, want %q", gotDisp, disposition)
	}

	// Fetch the URL: the response body must be our bytes and the
	// Content-Disposition header must contain the filename.
	resp, err := http.Get(signed) //nolint:gosec,noctx // signed URL is local MinIO
	if err != nil {
		t.Fatalf("GET signed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET signed status = %d", resp.StatusCode)
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("body round-trip differs")
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, filename) {
		t.Errorf("Content-Disposition %q should contain filename %q", cd, filename)
	}
}

func TestDelete_MissingKeyIsIdempotent(t *testing.T) {
	t.Parallel()
	store := newStore(t, 0)
	ctx := context.Background()

	if err := store.Delete(ctx, "never-existed"); err != nil {
		t.Fatalf("Delete on missing key should be no-error, got: %v", err)
	}
}

func TestDelete_RemovesObject(t *testing.T) {
	t.Parallel()
	store := newStore(t, 0)
	ctx := context.Background()

	const key = "to-delete"
	if err := store.Put(ctx, key, "text/plain", strings.NewReader("hi"), 2); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// A second Delete must also succeed.
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestPut_RejectsKnownOverCap(t *testing.T) {
	t.Parallel()
	store := newStore(t, 100) // 100-byte cap
	ctx := context.Background()

	body := bytes.Repeat([]byte("x"), 200)
	err := store.Put(ctx, "too-big", "text/plain", bytes.NewReader(body), int64(len(body)))
	if err == nil {
		t.Fatal("expected ErrPayloadTooLarge for known-oversize body")
	}
	if !errors.Is(err, objectstore.ErrPayloadTooLarge) {
		t.Fatalf("error chain should match ErrPayloadTooLarge, got: %v", err)
	}
}

func TestPut_RejectsStreamingOverCap(t *testing.T) {
	t.Parallel()
	store := newStore(t, 100)
	ctx := context.Background()

	// size=-1 hides the actual length from Put, so the cap must be enforced
	// while streaming. The reader produces 200 bytes; the cap is 100.
	body := bytes.NewReader(bytes.Repeat([]byte("x"), 200))
	err := store.Put(ctx, "streaming-too-big", "text/plain", body, -1)
	if err == nil {
		t.Fatal("expected error for streaming-oversize body")
	}
	if !errors.Is(err, objectstore.ErrPayloadTooLarge) {
		t.Fatalf("error chain should match ErrPayloadTooLarge, got: %v", err)
	}
}

func TestPut_AllowsExactlyAtCap(t *testing.T) {
	t.Parallel()
	store := newStore(t, 100)
	ctx := context.Background()

	body := bytes.Repeat([]byte("y"), 100) // exactly at cap → must succeed
	if err := store.Put(ctx, "exactly-cap", "text/plain", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put at exact cap should succeed, got: %v", err)
	}
}

func TestPut_RejectsBlankInputs(t *testing.T) {
	t.Parallel()
	store := newStore(t, 0)
	ctx := context.Background()

	if err := store.Put(ctx, "", "text/plain", strings.NewReader("x"), 1); err == nil {
		t.Error("blank key should error")
	}
	if err := store.Put(ctx, "k", "", strings.NewReader("x"), 1); err == nil {
		t.Error("blank contentType should error")
	}
	if err := store.Put(ctx, "k", "text/plain", nil, 1); err == nil {
		t.Error("nil body should error")
	}
}

func TestNew_ValidatesConfig(t *testing.T) {
	t.Parallel()
	base := objectstore.Config{
		Endpoint:  "http://localhost:9000",
		Region:    "us-east-1",
		AccessKey: "k",
		SecretKey: "s",
		Bucket:    "b",
	}
	cases := []struct {
		name string
		mod  func(*objectstore.Config)
	}{
		{"missing endpoint", func(c *objectstore.Config) { c.Endpoint = "" }},
		{"missing region", func(c *objectstore.Config) { c.Region = "" }},
		{"missing access key", func(c *objectstore.Config) { c.AccessKey = "" }},
		{"missing secret key", func(c *objectstore.Config) { c.SecretKey = "" }},
		{"missing bucket", func(c *objectstore.Config) { c.Bucket = "" }},
		{"negative max bytes", func(c *objectstore.Config) { c.MaxUploadBytes = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mod(&cfg)
			if _, err := objectstore.New(cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestPresignGet_RejectsBlankKey(t *testing.T) {
	t.Parallel()
	store := newStore(t, 0)
	if _, err := store.PresignGet(context.Background(), "", time.Minute, ""); err == nil {
		t.Fatal("blank key should error")
	}
}
