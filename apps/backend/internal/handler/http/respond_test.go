package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
)

// --- WriteJSON / WriteNoContent / WriteError ---------------------------

func TestWriteJSON_SetsContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteJSON(rec, http.StatusCreated, map[string]string{"hello": "world"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("body = %+v", body)
	}
}

func TestWriteNoContent_204(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteNoContent(rec)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body should be empty: %q", rec.Body.String())
	}
}

func TestWriteError_APIError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil),
		apierror.NotFound("user"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var env struct {
		Error apierror.Error `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", env.Error.Code)
	}
}

func TestWriteError_RateLimitedSetsRetryAfter(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil),
		apierror.RateLimited(42))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "42" {
		t.Errorf("Retry-After = %q, want \"42\"", got)
	}
}

func TestWriteError_UnknownErrorBecomes500Generic(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil),
		errors.New("something exploded internally"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	// MUST NOT leak the raw cause string.
	if strings.Contains(body, "exploded internally") {
		t.Fatalf("response leaked raw error string: %s", body)
	}
	// MUST contain the generic "internal error" message.
	if !strings.Contains(body, "internal error") {
		t.Fatalf("response missing generic message: %s", body)
	}
}

// --- DecodeJSON ---------------------------------------------------------

type decodeReq struct {
	Email string `json:"email"        validate:"required,email"`
	Name  string `json:"display_name" validate:"required,min=1,max=10"`
}

func TestDecodeJSON_Success(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(
		`{"email":"a@b.test","display_name":"caden"}`))
	v := httpapi.NewValidator()
	var dst decodeReq
	if e := httpapi.DecodeJSON(r, v, &dst); e != nil {
		t.Fatalf("DecodeJSON: %v", e)
	}
	if dst.Email != "a@b.test" || dst.Name != "caden" {
		t.Errorf("dst = %+v", dst)
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	var dst decodeReq
	if e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst); e == nil || e.Code != apierror.CodeBadRequest {
		t.Fatalf("got %v, want BadRequest", e)
	}
}

func TestDecodeJSON_MalformedJSON(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{not json"))
	var dst decodeReq
	if e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst); e == nil || e.Code != apierror.CodeBadRequest {
		t.Fatalf("got %v, want BadRequest", e)
	}
}

func TestDecodeJSON_UnknownField(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(
		`{"email":"a@b.test","display_name":"caden","extra":1}`))
	var dst decodeReq
	if e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst); e == nil || e.Code != apierror.CodeBadRequest {
		t.Fatalf("got %v, want BadRequest", e)
	}
}

func TestDecodeJSON_ValidationFails(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(
		`{"email":"not-an-email","display_name":""}`))
	var dst decodeReq
	e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst)
	if e == nil || e.Code != apierror.CodeValidation {
		t.Fatalf("got %v, want VALIDATION_FAILED", e)
	}
	// Field paths should reflect JSON tag names (display_name, not Name).
	var foundName, foundEmail bool
	for _, f := range e.Fields {
		switch f.Field {
		case "display_name":
			foundName = true
		case "email":
			foundEmail = true
		}
	}
	if !foundName {
		t.Errorf("expected display_name field error, got %+v", e.Fields)
	}
	if !foundEmail {
		t.Errorf("expected email field error, got %+v", e.Fields)
	}
}

func TestDecodeJSON_PayloadTooLarge(t *testing.T) {
	t.Parallel()
	big := make([]byte, 0, 1<<21)
	big = append(big, []byte(`{"email":"a@b.test","display_name":"`)...)
	for i := 0; i < 1<<21; i++ {
		big = append(big, 'x')
	}
	big = append(big, '"', '}')
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(big)))
	var dst decodeReq
	e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst)
	if e == nil || e.Code != apierror.CodePayloadTooLarge {
		t.Fatalf("got %v, want PAYLOAD_TOO_LARGE", e)
	}
}

func TestDecodeJSON_TrailingGarbage(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(
		`{"email":"a@b.test","display_name":"x"}{"more":1}`))
	var dst decodeReq
	if e := httpapi.DecodeJSON(r, httpapi.NewValidator(), &dst); e == nil || e.Code != apierror.CodeBadRequest {
		t.Fatalf("got %v, want BadRequest", e)
	}
}

// WriteJSON with nil body writes the status header but no body —
// covers the body==nil early-return that the typical write path
// can't reach.
func TestWriteJSON_NilBodyWritesHeaderOnly(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteJSON(rec, http.StatusAccepted, nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body should be empty: %q", rec.Body.String())
	}
}

// WriteError(nil) is a defensive guard — if a handler ever passes nil
// the response is a generic 500 instead of a panic. Documented in the
// function comment, but no test reaches it because callers always
// have a real error in hand.
func TestWriteError_NilErrorIs500(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// jsonTagName returns "" for a missing/dash tag so validator falls
// back to the Go field name. NewValidator wires this in for the
// snake_case field-error path; the helper is small but the dash and
// missing branches are uncovered without exercising them directly.
func TestNewValidator_UsesJSONTagsAndFallsBackOnDash(t *testing.T) {
	t.Parallel()
	type req struct {
		WithJSON  string `json:"with_json"  validate:"required"`
		WithDash  string `json:"-"          validate:"required"`
		NoJSONTag string `validate:"required"`
	}
	v := httpapi.NewValidator()
	err := v.Struct(req{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	got := err.Error()
	// json:"with_json" → field name is the json tag.
	if !strings.Contains(got, "with_json") {
		t.Errorf("expected with_json (snake_case from JSON tag), got %q", got)
	}
	// json:"-" → fall through to Go field name.
	if !strings.Contains(got, "WithDash") {
		t.Errorf("expected WithDash (Go name fallback), got %q", got)
	}
	// no json tag → fall through to Go field name.
	if !strings.Contains(got, "NoJSONTag") {
		t.Errorf("expected NoJSONTag (Go name fallback), got %q", got)
	}
}
