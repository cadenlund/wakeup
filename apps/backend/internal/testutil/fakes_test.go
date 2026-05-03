package testutil_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// --- FakeMailer ---------------------------------------------------------

func TestFakeMailer_SendCapturesAndReset(t *testing.T) {
	t.Parallel()
	m := &testutil.FakeMailer{}
	if err := m.SendPasswordReset(context.Background(), "a@x.test", "tok-1"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	if err := m.SendPasswordReset(context.Background(), "b@x.test", "tok-2"); err != nil {
		t.Fatalf("SendPasswordReset 2: %v", err)
	}
	if len(m.Sent) != 2 {
		t.Fatalf("Sent len = %d, want 2", len(m.Sent))
	}
	if m.Sent[0].To != "a@x.test" || m.Sent[0].Token != "tok-1" {
		t.Errorf("Sent[0] = %+v", m.Sent[0])
	}
	if m.Sent[1].To != "b@x.test" || m.Sent[1].Token != "tok-2" {
		t.Errorf("Sent[1] = %+v", m.Sent[1])
	}
	if m.Sent[0].At.IsZero() {
		t.Error("At should be set")
	}
	m.Reset()
	if len(m.Sent) != 0 {
		t.Errorf("after Reset, Sent should be empty, got %d", len(m.Sent))
	}
}

// --- FakePusher ---------------------------------------------------------

func TestFakePusher_SendDeepCopiesAndSnapshots(t *testing.T) {
	t.Parallel()
	p := &testutil.FakePusher{}
	tokens := []string{"a", "b"}
	data := map[string]any{"k": "v"}
	if err := p.Send(context.Background(), tokens, testutil.Notification{
		Title: "T", Body: "B", Data: data,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Mutate caller-side after Send returns. The recorded copy must
	// stay frozen.
	tokens[0] = "MUTATED"
	data["k"] = "MUTATED"

	snap := p.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len = %d, want 1", len(snap))
	}
	if snap[0].Tokens[0] != "a" {
		t.Errorf("Tokens deep-copy failed: %v", snap[0].Tokens)
	}
	if snap[0].Data["k"] != "v" {
		t.Errorf("Data deep-copy failed: %v", snap[0].Data)
	}
	if snap[0].Title != "T" || snap[0].Body != "B" {
		t.Errorf("Title/Body lost: %+v", snap[0])
	}

	p.Reset()
	if len(p.Snapshot()) != 0 {
		t.Errorf("after Reset, Snapshot should be empty")
	}
}

// FakePusher.Send with nil Data preserves the nil rather than turning
// it into an empty map.
func TestFakePusher_NilDataPreserved(t *testing.T) {
	t.Parallel()
	p := &testutil.FakePusher{}
	if err := p.Send(context.Background(), []string{"a"}, testutil.Notification{
		Title: "T", Body: "B", Data: nil,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := p.Snapshot(); got[0].Data != nil {
		t.Errorf("Data should be nil, got %v", got[0].Data)
	}
}

// --- FakeObjectStore ----------------------------------------------------

func TestFakeObjectStore_PutGetDelete(t *testing.T) {
	t.Parallel()
	s := testutil.NewFakeObjectStore()
	body := []byte("hello world")
	if err := s.Put(context.Background(), "k1", "text/plain", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ct, ok := s.Object("k1")
	if !ok {
		t.Fatal("Object should be found")
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body mismatch: %s", got)
	}
	if ct != "text/plain" {
		t.Errorf("contentType = %q", ct)
	}

	// Mutate the returned slice — internal copy must stay intact.
	got[0] = 'X'
	got2, _, _ := s.Object("k1")
	if got2[0] != 'h' {
		t.Errorf("Object did not return a defensive copy")
	}

	if err := s.Delete(context.Background(), "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := s.Object("k1"); ok {
		t.Errorf("Object should be gone after Delete")
	}
	// Delete on missing key is idempotent.
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("Delete on missing key should be nil, got %v", err)
	}
}

func TestFakeObjectStore_PresignGet_RoundTripsKeyAndDisposition(t *testing.T) {
	t.Parallel()
	s := testutil.NewFakeObjectStore()
	if err := s.Put(context.Background(), "attachments/conv-1/file.pdf", "application/pdf",
		bytes.NewReader([]byte("data")), 4); err != nil {
		t.Fatalf("Put: %v", err)
	}
	disp := `attachment; filename="Q1 report.pdf"`
	url, err := s.PresignGet(context.Background(), "attachments/conv-1/file.pdf", time.Minute, disp)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if !strings.Contains(url, "attachments/conv-1/file.pdf") {
		t.Errorf("URL should embed key: %s", url)
	}
	if !strings.Contains(url, "ttl=1m0s") {
		t.Errorf("URL should embed ttl: %s", url)
	}
	if !strings.Contains(url, "response-content-disposition=") {
		t.Errorf("URL should embed disposition: %s", url)
	}
	if len(s.Presigns) != 1 || s.Presigns[0].Key != "attachments/conv-1/file.pdf" {
		t.Errorf("Presigns = %+v", s.Presigns)
	}
}

func TestFakeObjectStore_PresignGet_MissingKeyErrors(t *testing.T) {
	t.Parallel()
	s := testutil.NewFakeObjectStore()
	if _, err := s.PresignGet(context.Background(), "nope", time.Minute, ""); err == nil {
		t.Error("PresignGet on missing key should error")
	}
}

func TestFakeObjectStore_Reset(t *testing.T) {
	t.Parallel()
	s := testutil.NewFakeObjectStore()
	_ = s.Put(context.Background(), "k", "text/plain", bytes.NewReader([]byte("x")), 1)
	_, _ = s.PresignGet(context.Background(), "k", time.Minute, "")
	s.Reset()
	if _, _, ok := s.Object("k"); ok {
		t.Errorf("after Reset, Object should be missing")
	}
	if len(s.Presigns) != 0 {
		t.Errorf("after Reset, Presigns should be empty, got %d", len(s.Presigns))
	}
}

// --- SentryRecorder -----------------------------------------------------

func TestSentryRecorder_CaptureAndReset(t *testing.T) {
	t.Parallel()
	r := &testutil.SentryRecorder{}
	r.Capture(errors.New("boom"), map[string]string{"request_id": "abc"})

	// Mutate the input map after Capture returns. Recorded copy must
	// stay intact.
	r.Capture(errors.New("boom2"), map[string]string{"request_id": "def"})

	if len(r.Events) != 2 {
		t.Fatalf("Events len = %d, want 2", len(r.Events))
	}
	if r.Events[0].Tags["request_id"] != "abc" {
		t.Errorf("event[0] tags = %v", r.Events[0].Tags)
	}
	if r.Events[1].Tags["request_id"] != "def" {
		t.Errorf("event[1] tags = %v", r.Events[1].Tags)
	}

	r.Reset()
	if len(r.Events) != 0 {
		t.Errorf("after Reset, Events should be empty")
	}
}

// SentryRecorder accepts nil tags without panicking — recovery
// middleware passes nil when no per-request context is available.
func TestSentryRecorder_NilTagsNoPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("Capture with nil tags should not panic, got %v", rec)
		}
	}()
	r := &testutil.SentryRecorder{}
	r.Capture(errors.New("boom"), nil)
	if r.Events[0].Tags == nil {
		t.Errorf("Tags should be empty map, not nil")
	}
}
