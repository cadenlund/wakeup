// Package httpapi is the HTTP handler layer for /v1/* routes. Each
// aggregate has a `<name>_handler.go` file plus a `<name>_dto.go` for
// the wire types. This package never imports `internal/repository/*`
// directly; everything goes through the `internal/service/*` layer that
// returns `*apierror.Error` per §4.4.
//
// Package name is `httpapi` (not `http`) so we can still import
// `net/http` without aliasing — every handler signature uses the
// standard `http.ResponseWriter` / `*http.Request`.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
)

// ErrorResponse is the §4.4 outer JSON shape for error responses:
//
//	{ "error": { "code": "...", "message": "...", "fields": [...], ... } }
//
// Exported so swaggo `@Failure` annotations render the correct envelope
// shape in the generated OpenAPI spec (CodeRabbit caught the missing
// wrapper on PR #25). Every handler should reference this type — never
// `apierror.Error` directly — in `@Failure` lines.
type ErrorResponse struct {
	Error *apierror.Error `json:"error"`
}

// WriteJSON marshals body and writes with the given status. JSON encoding
// failures are logged-only — the handler is past the point of returning a
// useful error to the client (headers may already be on the wire). We log
// via WriteError → eventually slog/Sentry once the recovery middleware lands.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	// Best-effort encode. Failure here usually means the writer is closed
	// (client disconnected) — nothing useful left to do.
	_ = json.NewEncoder(w).Encode(body)
}

// WriteNoContent writes a 204 with no body. Used by logout / always-204
// endpoints (§6.2 password-reset/request).
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// WriteError translates any error into the standard error envelope:
//   - *apierror.Error      → use its Code, Message, Fields, HTTPStatus
//   - validator.ValidationErrors → convert via apierror.FromValidationErrors
//   - anything else        → wrap as Internal so the cause goes to logs
//     while the client only sees the generic "internal error" message.
//
// Also lifts RetryAfterSeconds onto the Retry-After response header (§4.4).
// Never leaks a raw Go error string to the client.
func WriteError(w http.ResponseWriter, _ *http.Request, err error) {
	if err == nil {
		WriteJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: apierror.Internal("WriteError called with nil"),
		})
		return
	}

	apiErr := toAPIError(err)
	if apiErr.Code == apierror.CodeRateLimited && apiErr.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(apiErr.RetryAfterSeconds))
	}
	WriteJSON(w, apiErr.HTTPStatus(), ErrorResponse{Error: apiErr})
}

// toAPIError performs the single-source-of-truth conversion used by both
// WriteError and DecodeJSON when validator returns a non-apierror error.
func toAPIError(err error) *apierror.Error {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		return apierror.FromValidationErrors(verrs)
	}
	// Unknown error → 500 with the cause attached so logging middleware can
	// capture it. The client never sees the wrapped string.
	return apierror.Internal("internal error").WithCause(err)
}

// DecodeJSON reads the request body into dst and runs validator-tag
// validation. Three error shapes can come back:
//   - apierror.BadRequest   — empty body, malformed JSON, unknown fields
//   - apierror.Validation   — one or more `validate:"..."` tag failures
//   - nil                   — dst is populated; caller proceeds
//
// Body size is capped at maxJSONBodyBytes (1 MiB) — uploads use a
// different code path with their own larger cap.
func DecodeJSON(r *http.Request, v *validator.Validate, dst any) *apierror.Error {
	if r.Body == nil {
		return apierror.BadRequest("request body is empty")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		// Specific decoder errors → typed responses. We don't want to
		// leak the raw Go message; just identify the class of failure.
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return apierror.PayloadTooLarge("request body too large")
		}
		if errors.Is(err, io.EOF) {
			return apierror.BadRequest("request body is empty")
		}
		return apierror.BadRequest("malformed JSON")
	}
	// Reject trailing tokens after the first JSON value — protects against
	// "two JSON objects concatenated" smuggling and obvious typos.
	if dec.More() {
		return apierror.BadRequest("malformed JSON")
	}

	if v != nil {
		if err := v.Struct(dst); err != nil {
			var verrs validator.ValidationErrors
			if errors.As(err, &verrs) {
				return apierror.FromValidationErrors(verrs)
			}
			// Validator can also return InvalidValidationError for misuse;
			// surface as Internal — never as Validation, that'd be misleading.
			return apierror.Internal("validate request").WithCause(err)
		}
	}
	return nil
}

// maxJSONBodyBytes is the upper bound on a JSON request body. 1 MiB is
// generous for our schema (longest payload is a 10k-char message body
// plus envelope, ≈10 KiB). Uploads use multipart with their own cap.
const maxJSONBodyBytes = 1 << 20 // 1 MiB

// NewValidator returns a validator/v10 instance configured to use the
// `json` tag name for field paths, so FromValidationErrors produces
// snake_case field names that match the wire shape.
func NewValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(jsonTagName)
	return v
}

// jsonTagName extracts the JSON wire-name from a struct field's `json` tag.
// Example: `json:"display_name,omitempty"` → "display_name".
// Returns "" when the tag is missing or "-", which makes validator fall
// back to the Go field name.
func jsonTagName(fld reflect.StructField) string {
	tag := fld.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}
