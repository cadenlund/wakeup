package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

func TestLogger_LogsRequestFields(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	uid := uuid.New()
	user := &domain.User{ID: uid, Username: "caden", Role: "user"}

	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	ctx := mw.WithRequestID(req.Context(), "req-abc")
	ctx = mw.WithUser(ctx, user)
	req = req.WithContext(ctx)

	h.ServeHTTP(httptest.NewRecorder(), req)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode log line: %v\n%s", err, buf.String())
	}
	if line["method"] != "POST" || line["path"] != "/v1/auth/login" {
		t.Errorf("missing method/path: %+v", line)
	}
	if int(line["status"].(float64)) != 200 {
		t.Errorf("status = %v, want 200", line["status"])
	}
	if line["request_id"] != "req-abc" {
		t.Errorf("request_id = %v, want req-abc", line["request_id"])
	}
	if line["user_id"] != uid.String() {
		t.Errorf("user_id = %v, want %s", line["user_id"], uid)
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Errorf("duration_ms missing: %+v", line)
	}
}

func TestLogger_5xxIsErrorLevel(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var line map[string]any
	_ = json.Unmarshal(buf.Bytes(), &line)
	if line["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", line["level"])
	}
}

func TestLogger_ImplicitWriteHeaderRecordedAs200(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Skip explicit WriteHeader; calling Write triggers an implicit 200.
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var line map[string]any
	_ = json.Unmarshal(buf.Bytes(), &line)
	if int(line["status"].(float64)) != 200 {
		t.Errorf("implicit status = %v, want 200", line["status"])
	}
}

// statusRecorder.Hijack forwards through to the underlying writer when
// it implements http.Hijacker (net/http's default chi-wrapped writer
// does). The WS upgrade path depends on this — without it the upgrade
// errors out before reading the request.
func TestLogger_HijackForwardsToUnderlying(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	hijacked := make(chan struct{}, 1)
	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("statusRecorder did not expose Hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		// Close immediately so the test request finishes cleanly.
		_ = conn.Close()
		hijacked <- struct{}{}
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, _ := http.Get(srv.URL) //nolint:gosec,noctx // test server
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	select {
	case <-hijacked:
	default:
		t.Error("Hijack handler never fired")
	}
}

// statusRecorder.Flush forwards to the wrapped writer's Flusher.
// Calling Flush before any write should be safe (the recorder's
// status defaults to 200 and Flush is a no-op until headers are
// committed).
func TestLogger_FlushForwardsToUnderlying(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	flushed := make(chan struct{}, 1)
	h := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("statusRecorder did not expose Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hi\n\n"))
		f.Flush()
		flushed <- struct{}{}
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL) //nolint:gosec,noctx // test server
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	select {
	case <-flushed:
	default:
		t.Error("Flush handler never fired")
	}
}
