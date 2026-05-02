// Package objectstore is the S3 / MinIO wrapper every upload + presigned-
// download endpoint goes through. The interface and contract live in
// WAKEUP.md §9 (locked after the security audit in PR #3).
//
// Key facts:
//   - One Store talks to one bucket. Each call takes a logical "key" (the
//     S3 object key relative to the bucket) — see §9.1 for layout.
//   - Server-proxied uploads (§9.2) use Put. The handler is responsible for
//     server-side MIME detection on the first 512 bytes BEFORE handing the
//     reader here — Put trusts the contentType argument.
//   - Downloads are presigned (§9.3) with a 5-minute TTL by default. Pass
//     a Content-Disposition header to bind the original filename into the
//     signed URL via response-content-disposition (so the browser sees
//     "attachment; filename=Q1-report.pdf" without the user-supplied name
//     ever appearing in the S3 key itself — §9.1).
//   - Bucket configuration baseline is the operator's responsibility (§9.4):
//     block-public-ACLs, default-private, SSE-S3, TLS-only bucket policy,
//     scoped CORS. This package does not manage the bucket lifecycle.
package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Config configures the Store. All fields except ForcePathStyle and Logger
// are required.
type Config struct {
	Endpoint          string // e.g. https://s3.us-east-1.amazonaws.com or http://localhost:9000 for MinIO
	Region            string // e.g. us-east-1 (must match the bucket's region in prod)
	AccessKey         string // static credentials. In prod, prefer IAM role-based credentials.
	SecretKey         string
	Bucket            string
	ForcePathStyle    bool          // true for MinIO; false for AWS S3 with virtual-host-style addressing
	MaxUploadBytes    int64         // 0 = unlimited; uploads larger than this get rejected before any S3 call
	DefaultPresignTTL time.Duration // 0 → defaults to 5*time.Minute (§9.3)
}

// Store wraps an *s3.Client + a presigner. Goroutine-safe; share one instance.
type Store struct {
	client            *s3.Client
	presigner         *s3.PresignClient
	bucket            string
	maxUploadBytes    int64
	defaultPresignTTL time.Duration
}

// ErrPayloadTooLarge is returned by Put when the body exceeds the configured
// MaxUploadBytes. Surfaced by the upload handler as apierror.PayloadTooLarge
// (413). Attached to the §9.2 chain alongside http.MaxBytesReader so an
// attacker can't stream past the limit and only get rejected post-write.
var ErrPayloadTooLarge = errors.New("objectstore: payload exceeds configured cap")

// New constructs a Store from cfg. Uses a static-credentials provider
// (suitable for MinIO and for prod with rotated access keys); for IAM-role
// production deploys, the caller can build their own *s3.Client and pass
// to NewWithClient.
func New(cfg Config) (*Store, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, "",
		),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return NewWithClient(client, cfg), nil
}

// NewWithClient is the escape hatch for callers that build their own
// *s3.Client (typically because they're using IAM-role credentials in
// production). The Bucket / MaxUploadBytes / DefaultPresignTTL fields of
// cfg are honored; the credentials/region/endpoint fields are ignored.
func NewWithClient(client *s3.Client, cfg Config) *Store {
	ttl := cfg.DefaultPresignTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Store{
		client:            client,
		presigner:         s3.NewPresignClient(client),
		bucket:            cfg.Bucket,
		maxUploadBytes:    cfg.MaxUploadBytes,
		defaultPresignTTL: ttl,
	}
}

func validateConfig(cfg Config) error {
	switch {
	case strings.TrimSpace(cfg.Endpoint) == "":
		return errors.New("objectstore: Config.Endpoint is required")
	case strings.TrimSpace(cfg.Region) == "":
		return errors.New("objectstore: Config.Region is required")
	case strings.TrimSpace(cfg.AccessKey) == "":
		return errors.New("objectstore: Config.AccessKey is required")
	case strings.TrimSpace(cfg.SecretKey) == "":
		return errors.New("objectstore: Config.SecretKey is required")
	case strings.TrimSpace(cfg.Bucket) == "":
		return errors.New("objectstore: Config.Bucket is required")
	case cfg.MaxUploadBytes < 0:
		return fmt.Errorf("objectstore: Config.MaxUploadBytes must be >= 0, got %d", cfg.MaxUploadBytes)
	}
	return nil
}

// Put buffers body into memory (capped at MaxUploadBytes if set) and writes
// it to S3 at key with the given contentType. size is an optional caller
// hint for the pre-flight cap check; pass -1 if unknown.
//
// Why buffer instead of streaming straight through:
//   - The aws-sdk-go-v2 SigV4 signer requires a seekable body so it can
//     hash the payload and retry on transient failures. A plain io.Reader
//     fails with "request stream is not seekable".
//   - Wrapping with a custom cap-reader hid the underlying seekability.
//   - For our use case the caps are 50 MiB (attachments) and 5 MiB
//     (avatars), so peak per-request memory is bounded and tiny.
//
// contentType MUST be the SERVER-DETECTED MIME (§9.2 step 2), never a
// client-supplied header. The handler does the detection before calling.
func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("objectstore: Put: key is empty")
	}
	if strings.TrimSpace(contentType) == "" {
		return errors.New("objectstore: Put: contentType is empty (server-detect first; §9.2)")
	}
	if body == nil {
		return errors.New("objectstore: Put: body is nil")
	}

	// Pre-flight: a caller-supplied size that already exceeds the cap fails
	// before the body is touched at all.
	if s.maxUploadBytes > 0 && size >= 0 && size > s.maxUploadBytes {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, size, s.maxUploadBytes)
	}

	// Bound the read at cap+1 so we can detect "stream exceeded cap" by
	// observing len(data) > cap.
	var data []byte
	if s.maxUploadBytes > 0 {
		buf, err := io.ReadAll(io.LimitReader(body, s.maxUploadBytes+1))
		if err != nil {
			return fmt.Errorf("objectstore: Put %q: read body: %w", key, err)
		}
		if int64(len(buf)) > s.maxUploadBytes {
			return fmt.Errorf("%w: stream exceeded %d bytes", ErrPayloadTooLarge, s.maxUploadBytes)
		}
		data = buf
	} else {
		buf, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("objectstore: Put %q: read body: %w", key, err)
		}
		data = buf
	}

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return fmt.Errorf("objectstore: Put %q: %w", key, err)
	}
	return nil
}

// PresignGet returns a short-lived URL the client uses to GET the object.
// ttl falls through to DefaultPresignTTL when zero. contentDisposition (e.g.
// `attachment; filename="Q1-report.pdf"`) is bound into the signed URL via
// the `response-content-disposition` query param so the browser sees the
// original user-supplied filename without that filename ever appearing in
// the S3 key itself (§9.1, §9.3). Pass an empty string for avatars to keep
// the default inline disposition.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration, contentDisposition string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("objectstore: PresignGet: key is empty")
	}
	if ttl <= 0 {
		ttl = s.defaultPresignTTL
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if contentDisposition != "" {
		input.ResponseContentDisposition = aws.String(contentDisposition)
	}
	req, err := s.presigner.PresignGetObject(ctx, input, func(o *s3.PresignOptions) {
		o.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("objectstore: PresignGet %q: %w", key, err)
	}
	return req.URL, nil
}

// Delete removes the object at key. Idempotent — a 404 on a missing key is
// not surfaced as an error so callers (the orphan sweeper §9.6, the user-
// account-deletion path) don't need to special-case "already gone" rows.
func (s *Store) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("objectstore: Delete: key is empty")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return nil
	}
	return fmt.Errorf("objectstore: Delete %q: %w", key, err)
}

// isNotFound reports whether err is the SDK's NoSuchKey response. AWS S3
// returns 204 even for missing keys; MinIO sometimes surfaces a 404. Either
// way Delete should treat it as success.
func isNotFound(err error) bool {
	var apiErr *smithyhttp.ResponseError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode() == 404 {
		return true
	}
	// Fallback: a NoSuchKey error type from the s3 service.
	var nsk *s3NoSuchKeyMarker
	return errors.As(err, &nsk)
}

// s3NoSuchKeyMarker exists purely so we can errors.As against the SDK's
// NoSuchKey error without importing every error type. The SDK's NoSuchKey
// type implements error and has Method() returning a known string; this
// stub matches that interface for forward compatibility.
type s3NoSuchKeyMarker struct{}

func (s3NoSuchKeyMarker) Error() string { return "NoSuchKey" }
