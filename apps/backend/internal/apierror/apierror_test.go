package apierror_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
)

// TestHTTPStatus_AllCodesMapped is the gate that prevents the table from
// drifting when a new Code constant is added. Every Code in AllCodes() must
// produce a non-500 status (except CodeInternal itself).
func TestHTTPStatus_AllCodesMapped(t *testing.T) {
	t.Parallel()
	wantPerCode := map[apierror.Code]int{
		apierror.CodeBadRequest:                 400,
		apierror.CodeUnauthorized:               401,
		apierror.CodeForbidden:                  403,
		apierror.CodeBlockedDuringImpersonation: 403,
		apierror.CodeNotFound:                   404,
		apierror.CodeConflict:                   409,
		apierror.CodePayloadTooLarge:            413,
		apierror.CodeValidation:                 422,
		apierror.CodeIdempotencyKeyReused:       422,
		apierror.CodeRateLimited:                429,
		apierror.CodeInternal:                   500,
	}

	for _, code := range apierror.AllCodes() {
		want, ok := wantPerCode[code]
		if !ok {
			t.Fatalf("Code %q is in AllCodes() but missing from this test's wantPerCode — keep them in sync", code)
		}
		got := (&apierror.Error{Code: code}).HTTPStatus()
		if got != want {
			t.Errorf("HTTPStatus(%q) = %d, want %d", code, got, want)
		}
	}

	// Also assert AllCodes covers the same set of keys as wantPerCode (no
	// orphan entries in the test table).
	if len(apierror.AllCodes()) != len(wantPerCode) {
		t.Fatalf("AllCodes() length %d != wantPerCode length %d — drift", len(apierror.AllCodes()), len(wantPerCode))
	}
}

func TestHTTPStatus_NilReceiverReturns500(t *testing.T) {
	t.Parallel()
	var e *apierror.Error
	if got := e.HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("nil receiver should return 500, got %d", got)
	}
}

func TestError_StringFormat(t *testing.T) {
	t.Parallel()
	e := apierror.NotFound("user")
	if e.Error() != "RESOURCE_NOT_FOUND: user not found" {
		t.Fatalf("Error() = %q, want %q", e.Error(), "RESOURCE_NOT_FOUND: user not found")
	}

	var nilErr *apierror.Error
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil receiver Error() = %q, want empty", got)
	}
}

func TestWithCause_AndUnwrap(t *testing.T) {
	t.Parallel()
	root := errors.New("pgx: connection refused")
	wrapped := apierror.Internal("database unavailable").WithCause(root)

	if !errors.Is(wrapped, root) {
		t.Fatal("errors.Is should chase WithCause's chain")
	}
	if got := wrapped.Unwrap(); !errors.Is(got, root) {
		t.Fatalf("Unwrap = %v, want %v", got, root)
	}
}

func TestJSONMarshal_NormalError(t *testing.T) {
	t.Parallel()
	e := apierror.NotFound("user")
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"code":"RESOURCE_NOT_FOUND","message":"user not found"}`
	if string(got) != want {
		t.Fatalf("Marshal:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestJSONMarshal_RateLimitedHasRetryAfter(t *testing.T) {
	t.Parallel()
	e := apierror.RateLimited(30)
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(got), `"retry_after_seconds":30`) {
		t.Fatalf("RateLimited should serialize retry_after_seconds: %s", got)
	}
}

func TestJSONMarshal_NormalErrorOmitsRetryAfter(t *testing.T) {
	t.Parallel()
	e := apierror.NotFound("x")
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(got), "retry_after_seconds") {
		t.Fatalf("non-RateLimited should omit retry_after_seconds: %s", got)
	}
}

func TestJSONMarshal_ValidationErrorHasFields(t *testing.T) {
	t.Parallel()
	e := apierror.Validation([]apierror.FieldError{
		{Field: "email", Code: "INVALID_FORMAT", Message: "must be a valid email"},
		{Field: "password", Code: "TOO_SHORT", Message: "must be at least 8 characters"},
	})
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, `"code":"VALIDATION_FAILED"`) {
		t.Errorf("missing code: %s", s)
	}
	if !strings.Contains(s, `"field":"email"`) || !strings.Contains(s, `"code":"INVALID_FORMAT"`) {
		t.Errorf("missing first field: %s", s)
	}
	if !strings.Contains(s, `"field":"password"`) || !strings.Contains(s, `"code":"TOO_SHORT"`) {
		t.Errorf("missing second field: %s", s)
	}
}

func TestJSONMarshal_ValidationOmitsFieldsIfEmpty(t *testing.T) {
	t.Parallel()
	// A Validation with no fields should not emit "fields":[] — the json tag
	// uses omitempty + we copy nil slice as nil.
	e := apierror.Validation(nil)
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(got), "fields") {
		t.Fatalf("empty Validation should omit fields: %s", got)
	}
}

func TestFieldError_Shape(t *testing.T) {
	t.Parallel()
	fe := apierror.FieldError{Field: "user.email", Code: "INVALID_FORMAT", Message: "must be a valid email"}
	got, err := json.Marshal(fe)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"field":"user.email","code":"INVALID_FORMAT","message":"must be a valid email"}`
	if string(got) != want {
		t.Fatalf("FieldError JSON:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestFromValidationErrors_MapsTagsToFieldErrors(t *testing.T) {
	t.Parallel()
	v := validator.New()
	type RegisterRequest struct {
		Username    string `validate:"required,min=3,max=32,alphanum"`
		Email       string `validate:"required,email,max=254"`
		DisplayName string `validate:"required,min=1,max=64"`
		Password    string `validate:"required,min=8,max=128"`
		ColorScheme string `validate:"omitempty,oneof=light dark system"`
	}

	bad := RegisterRequest{
		Username:    "ab",           // too short
		Email:       "not-an-email", // invalid format
		DisplayName: "",             // required
		Password:    "short",        // too short
		ColorScheme: "fuschia",      // not in oneof
	}
	verr := v.Struct(bad)

	var verrs validator.ValidationErrors
	if !errors.As(verr, &verrs) {
		t.Fatalf("expected ValidationErrors, got %T: %v", verr, verr)
	}

	apiErr := apierror.FromValidationErrors(verrs)
	if apiErr.Code != apierror.CodeValidation {
		t.Fatalf("Code = %q, want VALIDATION_FAILED", apiErr.Code)
	}
	if len(apiErr.Fields) != 5 {
		t.Fatalf("expected 5 FieldErrors, got %d: %+v", len(apiErr.Fields), apiErr.Fields)
	}

	byField := map[string]apierror.FieldError{}
	for _, f := range apiErr.Fields {
		byField[f.Field] = f
	}
	checks := []struct {
		field    string
		wantCode string
	}{
		{"username", "TOO_SHORT"},
		{"email", "INVALID_FORMAT"},
		{"displayname", "REQUIRED"},
		{"password", "TOO_SHORT"},
		{"colorscheme", "INVALID_VALUE"},
	}
	for _, tc := range checks {
		got, ok := byField[tc.field]
		if !ok {
			t.Errorf("missing FieldError for %q. all fields: %+v", tc.field, apiErr.Fields)
			continue
		}
		if got.Code != tc.wantCode {
			t.Errorf("Field %q: Code = %q, want %q", tc.field, got.Code, tc.wantCode)
		}
		if got.Message == "" {
			t.Errorf("Field %q: empty Message", tc.field)
		}
	}
}

func TestIsCode(t *testing.T) {
	t.Parallel()
	notFound := apierror.NotFound("user")
	wrapped := apierror.Internal("oops").WithCause(notFound)

	if !apierror.IsCode(notFound, apierror.CodeNotFound) {
		t.Error("direct match should return true")
	}
	if apierror.IsCode(notFound, apierror.CodeInternal) {
		t.Error("wrong code should return false")
	}
	if !apierror.IsCode(wrapped, apierror.CodeInternal) {
		t.Error("wrapped: should match outer Code")
	}
	// Inner *Error must also match — IsCode walks the chain so a service
	// stacking Internal(...).WithCause(NotFound(...)) is searchable for both.
	if !apierror.IsCode(wrapped, apierror.CodeNotFound) {
		t.Error("wrapped: should match inner Code via chain walk")
	}
	if apierror.IsCode(errors.New("plain"), apierror.CodeNotFound) {
		t.Error("non-apierror should return false")
	}
	// Stop on chain end: a code that doesn't appear anywhere returns false.
	if apierror.IsCode(wrapped, apierror.CodeRateLimited) {
		t.Error("absent code should return false")
	}
}

func TestFromValidationErrors_PreservesNestedFieldPath(t *testing.T) {
	t.Parallel()
	v := validator.New()
	type Address struct {
		Street string `validate:"required"`
	}
	type Outer struct {
		User struct {
			Email string `validate:"required,email"`
		}
		Addr Address
	}
	bad := Outer{}
	bad.User.Email = "not-an-email"

	verr := v.Struct(bad)
	var verrs validator.ValidationErrors
	if !errors.As(verr, &verrs) {
		t.Fatalf("expected ValidationErrors, got %T", verr)
	}
	apiErr := apierror.FromValidationErrors(verrs)

	got := map[string]string{}
	for _, f := range apiErr.Fields {
		got[f.Field] = f.Code
	}
	// Namespace strips the top-level struct ("Outer.") so we get the same
	// shape the wire envelope expects: "user.email", "addr.street".
	if c, ok := got["user.email"]; !ok || c != "INVALID_FORMAT" {
		t.Errorf("missing user.email or wrong code: %+v", got)
	}
	if c, ok := got["addr.street"]; !ok || c != "REQUIRED" {
		t.Errorf("missing addr.street or wrong code: %+v", got)
	}
}

// Spot-check a couple of constructor edge cases.
func TestConstructors_DefaultsForEmptyMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		err           *apierror.Error
		wantSubstring string
	}{
		{"NotFound empty", apierror.NotFound(""), "resource not found"},
		{"Unauthorized empty", apierror.Unauthorized(""), "unauthorized"},
		{"Forbidden empty", apierror.Forbidden(""), "forbidden"},
		{"BadRequest empty", apierror.BadRequest(""), "bad request"},
		{"Conflict empty", apierror.Conflict(""), "conflict"},
		{"Internal empty", apierror.Internal(""), "internal error"},
		{"PayloadTooLarge empty", apierror.PayloadTooLarge(""), "payload too large"},
		{"BlockedDuringImpersonation empty", apierror.BlockedDuringImpersonation(""), "impersonation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tc.err.Message, tc.wantSubstring) {
				t.Fatalf("Message %q does not contain %q", tc.err.Message, tc.wantSubstring)
			}
		})
	}
}

func TestRateLimited_NegativeRetryClampedToZero(t *testing.T) {
	t.Parallel()
	e := apierror.RateLimited(-5)
	if e.RetryAfterSeconds != 0 {
		t.Fatalf("negative retry should clamp to 0, got %d", e.RetryAfterSeconds)
	}
}
