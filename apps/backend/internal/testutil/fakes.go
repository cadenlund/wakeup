package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"
)

// In-memory fakes used by Harness in handler/service tests. Their method
// signatures match the production interfaces defined in §9 (objectstore),
// §10.1 (mailer), §10.2 (pushnotif), and §11 (sentry hook). When those
// packages land in Phase 2, the fakes will satisfy them by structural typing
// — no explicit interface declaration is required from this side, but Phase 2
// can add a `var _ X = (*FakeX)(nil)` assertion in the production package.

// FakeMailer captures every email handed to SendPasswordReset. Callers
// assert on Sent under their own t.Cleanup; the harness exposes the slice
// directly so subtests can drain it between cases.
type FakeMailer struct {
	mu   sync.Mutex
	Sent []FakeEmail
}

// FakeEmail records the parameters of one mailer call.
type FakeEmail struct {
	To    string
	Token string
	At    time.Time
}

// SendPasswordReset records the call without doing any I/O. Always returns nil.
func (m *FakeMailer) SendPasswordReset(_ context.Context, to, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = append(m.Sent, FakeEmail{To: to, Token: token, At: time.Now().UTC()})
	return nil
}

// Reset clears captured emails. Useful between subtests sharing one Harness.
func (m *FakeMailer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = nil
}

// FakePusher captures every Expo push payload handed to Send. Same shape
// guidelines as FakeMailer.
type FakePusher struct {
	mu   sync.Mutex
	Sent []FakePush
}

// FakePush records the parameters of one Send call.
type FakePush struct {
	Tokens []string
	Title  string
	Body   string
	Data   map[string]any
	At     time.Time
}

// Notification mirrors §10.2's pushnotif.Notification shape so callers can
// pass the same struct shape they'd pass in production. When the production
// pushnotif.Notification lands, this type can be aliased.
type Notification struct {
	Title string
	Body  string
	Data  map[string]any
}

// Send records the call. Always returns nil.
func (p *FakePusher) Send(_ context.Context, tokens []string, n Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Sent = append(p.Sent, FakePush{
		Tokens: append([]string(nil), tokens...),
		Title:  n.Title,
		Body:   n.Body,
		Data:   n.Data,
		At:     time.Now().UTC(),
	})
	return nil
}

// Reset clears captured pushes.
func (p *FakePusher) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Sent = nil
}

// FakeObjectStore is an in-memory bucket. Put writes; PresignGet returns a
// fake URL whose path is the storage key (so handler tests can detect which
// key was presigned without actually fetching anything). Delete removes the
// entry. Calls capture the contentDisposition argument so handler tests can
// assert that the original filename round-trips.
type FakeObjectStore struct {
	mu      sync.Mutex
	objects map[string]fakeObject
	// Presigns records every PresignGet call in order. Useful for asserting
	// the disposition + ttl the service layer requested.
	Presigns []FakePresign
}

type fakeObject struct {
	Body        []byte
	ContentType string
}

// FakePresign records a PresignGet call.
type FakePresign struct {
	Key                string
	TTL                time.Duration
	ContentDisposition string
	URL                string
	At                 time.Time
}

// NewFakeObjectStore returns a ready-to-use in-memory store.
func NewFakeObjectStore() *FakeObjectStore {
	return &FakeObjectStore{objects: map[string]fakeObject{}}
}

// Put copies body into the bucket under key.
func (s *FakeObjectStore) Put(_ context.Context, key, contentType string, body io.Reader, _ int64) error {
	buf, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("FakeObjectStore: read body: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = fakeObject{Body: buf, ContentType: contentType}
	return nil
}

// PresignGet returns a deterministic fake URL embedding the key and disposition
// so handler tests can assert without dialing back into the fake. Returns an
// error if the key isn't present (real S3 would return a usable URL even for
// missing keys, but the fake fails fast to make missing-write bugs obvious).
func (s *FakeObjectStore) PresignGet(_ context.Context, key string, ttl time.Duration, contentDisposition string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return "", fmt.Errorf("FakeObjectStore: key %q not found (was Put called?)", key)
	}
	q := url.Values{}
	q.Set("ttl", ttl.String())
	if contentDisposition != "" {
		q.Set("response-content-disposition", contentDisposition)
	}
	fakeURL := fmt.Sprintf("https://fake-s3.local/%s?%s", key, q.Encode())
	s.Presigns = append(s.Presigns, FakePresign{
		Key:                key,
		TTL:                ttl,
		ContentDisposition: contentDisposition,
		URL:                fakeURL,
		At:                 time.Now().UTC(),
	})
	return fakeURL, nil
}

// Delete removes the object at key. Idempotent — missing keys are not an error.
func (s *FakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// Object returns the object's bytes + content type for test assertions.
// Returns ok=false if the key was never written.
func (s *FakeObjectStore) Object(key string) (body []byte, contentType string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, "", false
	}
	return append([]byte(nil), o.Body...), o.ContentType, true
}

// Reset wipes the in-memory bucket and presign history.
func (s *FakeObjectStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = map[string]fakeObject{}
	s.Presigns = nil
}

// SentryRecorder captures events that would be sent to Sentry in production.
// Wired in Phase 13.1 (sentry init); for now the Harness exposes one so
// failing handler tests can assert "Sentry got an event with the request_id."
type SentryRecorder struct {
	mu     sync.Mutex
	Events []SentryEvent
}

// SentryEvent is a minimal capture: error + tags. The real sentry-go SDK
// captures more, but for assertion purposes this is enough.
type SentryEvent struct {
	Err  error
	Tags map[string]string
	At   time.Time
}

// Capture records err and tags as a Sentry event.
func (r *SentryRecorder) Capture(err error, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tagCopy := make(map[string]string, len(tags))
	for k, v := range tags {
		tagCopy[k] = v
	}
	r.Events = append(r.Events, SentryEvent{Err: err, Tags: tagCopy, At: time.Now().UTC()})
}

// Reset clears recorded events.
func (r *SentryRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = nil
}

// Compile-time guard: the bytes.Buffer reader is the canonical io.Reader Put
// will receive; this `var _` line is just here so unused-import linters can't
// complain that bytes is dead. (Also useful for future inline test data.)
var _ io.Reader = (*bytes.Reader)(nil)
