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
