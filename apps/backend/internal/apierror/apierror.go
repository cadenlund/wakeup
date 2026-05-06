// Package apierror is the typed-error contract every service returns and every
// HTTP handler renders. The Error type carries enough metadata that
// handler/http.WriteError can produce the standard envelope from §4.4 with
// the right status code, the right field-level details for VALIDATION_FAILED,
// and a Retry-After header for RATE_LIMITED — all without leaking unwrapped
// Go error strings to the client.
//
// Discipline (§4.4):
//   - Services return *apierror.Error. Never bare errors.New / fmt.Errorf to
//     a handler.
//   - Internal failures wrap the cause via Internal("...").WithCause(err) so
//     slog + Sentry see the full chain while clients only see the generic
//     "internal error" message.
package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
)

// Code is the machine-readable error tag that frontend clients switch on.
// Strings are stable wire identifiers — never rename without a frontend change.
type Code string

// Code constants — every value the API returns to a client.
const (
	CodeBadRequest                 Code = "BAD_REQUEST"
	CodeUnauthorized               Code = "UNAUTHORIZED"
	CodeResetTokenExpired          Code = "RESET_TOKEN_EXPIRED"
	CodeForbidden                  Code = "FORBIDDEN"
	CodeBlockedDuringImpersonation Code = "BLOCKED_DURING_IMPERSONATION"
	CodeNotFound                   Code = "RESOURCE_NOT_FOUND"
	CodeConflict                   Code = "CONFLICT"
	CodePayloadTooLarge            Code = "PAYLOAD_TOO_LARGE"
	CodeValidation                 Code = "VALIDATION_FAILED"
	CodeIdempotencyKeyReused       Code = "IDEMPOTENCY_KEY_REUSED"
	CodeRateLimited                Code = "RATE_LIMITED"
	CodeInternal                   Code = "INTERNAL"
)

// httpStatus maps every Code to its HTTP status. Tests cover every key
// (TestHTTPStatus_AllCodesMapped) so adding a Code without updating this
// table fails the suite.
var httpStatus = map[Code]int{
	CodeBadRequest:                 http.StatusBadRequest,
	CodeUnauthorized:               http.StatusUnauthorized,
	CodeResetTokenExpired:          http.StatusUnauthorized,
	CodeForbidden:                  http.StatusForbidden,
	CodeBlockedDuringImpersonation: http.StatusForbidden,
	CodeNotFound:                   http.StatusNotFound,
	CodeConflict:                   http.StatusConflict,
	CodePayloadTooLarge:            http.StatusRequestEntityTooLarge,
	CodeValidation:                 http.StatusUnprocessableEntity,
	CodeIdempotencyKeyReused:       http.StatusUnprocessableEntity,
	CodeRateLimited:                http.StatusTooManyRequests,
	CodeInternal:                   http.StatusInternalServerError,
}

// allCodes is the canonical iteration order — used by tests to assert every
// Code constant is in the httpStatus map.
var allCodes = []Code{
	CodeBadRequest,
	CodeUnauthorized,
	CodeResetTokenExpired,
	CodeForbidden,
	CodeBlockedDuringImpersonation,
	CodeNotFound,
	CodeConflict,
	CodePayloadTooLarge,
	CodeValidation,
	CodeIdempotencyKeyReused,
	CodeRateLimited,
	CodeInternal,
}

// AllCodes returns a fresh copy of every Code constant. Used by tests + the
// CI sanity script that cross-checks swaggo @Failure annotations against the
// codes a handler can produce.
func AllCodes() []Code {
	out := make([]Code, len(allCodes))
	copy(out, allCodes)
	return out
}

// FieldError describes one validation failure on a request body. Wire shape
// matches §4.4's example exactly so frontend clients can switch on Code.
type FieldError struct {
	Field   string `json:"field"`   // dot path, e.g. "user.email"
	Code    string `json:"code"`    // machine code, e.g. "INVALID_FORMAT", "TOO_SHORT"
	Message string `json:"message"` // human fallback
}

// Error is the typed error every service returns. JSON-shape mirrors §4.4 —
// `cause` is intentionally unexported so it never leaks to the wire.
//
// The Swagger schema for this shape is published from the handler package
// via httpapi.APIError + httpapi.ErrorResponse — they mirror the JSON
// shape exactly. We keep the docs source local to the handler package
// because swag's --parseDependency mode struggles to resolve named types
// across internal/* packages.
type Error struct {
	Code              Code         `json:"code"`
	Message           string       `json:"message"`
	Fields            []FieldError `json:"fields,omitempty"`              // populated for VALIDATION_FAILED
	RetryAfterSeconds int          `json:"retry_after_seconds,omitempty"` // populated for RATE_LIMITED
	cause             error        // not serialized — surfaced to slog/Sentry only
}

// Error implements the error interface. Format is "<code>: <message>" so log
// lines stay greppable; field/cause detail goes through Unwrap.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause to errors.Is / errors.As. Handler-side
// logging and Sentry capture chase the chain via Unwrap.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// HTTPStatus returns the status code from the §4.4 mapping table. Falls back
// to 500 if the Code is somehow unknown — that's a programmer bug; the test
// suite refuses to let the table drift.
func (e *Error) HTTPStatus() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	if status, ok := httpStatus[e.Code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// WithCause attaches an underlying error for slog + Sentry. Returns the
// receiver for fluent-style chaining: `apierror.Internal("oops").WithCause(err)`.
func (e *Error) WithCause(err error) *Error {
	if e == nil {
		return nil
	}
	e.cause = err
	return e
}

// --- Constructors --------------------------------------------------------

// NotFound returns a CodeNotFound error. The resource argument is interpolated
// into a message like "user not found" — pass the singular noun.
func NotFound(resource string) *Error {
	r := strings.TrimSpace(resource)
	if r == "" {
		r = "resource"
	}
	return &Error{Code: CodeNotFound, Message: r + " not found"}
}

// Unauthorized returns a CodeUnauthorized error. msg is shown to the client;
// keep it generic ("authentication required", not "session token expired in
// row N of sessions table") so attackers don't learn what's wrong.
func Unauthorized(msg string) *Error {
	if msg == "" {
		msg = "unauthorized"
	}
	return &Error{Code: CodeUnauthorized, Message: msg}
}

// ResetTokenExpired is the password-reset specific 401: distinguished
// from CodeUnauthorized so the mobile client can surface a "your link
// expired, request a new one" UX without string-matching the message.
// Disclosing "expired" specifically is harmless — an attacker holding
// an expired token already had it (e.g. email leak) and learns nothing
// new from the distinction. See passwordreset/repo.go ErrExpired.
func ResetTokenExpired(msg string) *Error {
	if msg == "" {
		msg = "reset link has expired"
	}
	return &Error{Code: CodeResetTokenExpired, Message: msg}
}

// Forbidden returns a CodeForbidden error.
func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "forbidden"
	}
	return &Error{Code: CodeForbidden, Message: msg}
}

// BlockedDuringImpersonation is the special-case 403 for routes
// (/v1/users/me DELETE, /v1/auth/password-reset/*, etc.) that an admin
// impersonating another user must NOT be able to invoke. See §8.7.
func BlockedDuringImpersonation(msg string) *Error {
	if msg == "" {
		msg = "this action is not allowed during admin impersonation"
	}
	return &Error{Code: CodeBlockedDuringImpersonation, Message: msg}
}

// Validation returns a CodeValidation error with one or more FieldErrors.
// Pass an empty slice if the failure isn't field-scoped (rare — usually you
// just want BadRequest in that case).
func Validation(fields []FieldError) *Error {
	return &Error{
		Code:    CodeValidation,
		Message: "request validation failed",
		Fields:  append([]FieldError(nil), fields...), // defensive copy
	}
}

// Conflict returns a CodeConflict error (e.g. duplicate username on register).
func Conflict(msg string) *Error {
	if msg == "" {
		msg = "conflict"
	}
	return &Error{Code: CodeConflict, Message: msg}
}

// RateLimited returns a CodeRateLimited error with the retry-after window the
// rate limiter computed. Handler middleware lifts that to the Retry-After
// response header (§4.4).
func RateLimited(retryAfterSec int) *Error {
	if retryAfterSec < 0 {
		retryAfterSec = 0
	}
	return &Error{
		Code:              CodeRateLimited,
		Message:           "too many requests",
		RetryAfterSeconds: retryAfterSec,
	}
}

// BadRequest returns a CodeBadRequest error. Use for malformed input that
// isn't field-level (malformed JSON, missing body, etc.).
func BadRequest(msg string) *Error {
	if msg == "" {
		msg = "bad request"
	}
	return &Error{Code: CodeBadRequest, Message: msg}
}

// PayloadTooLarge returns a CodePayloadTooLarge error. Used by upload handlers
// when http.MaxBytesReader trips (§9.2 attachment upload).
func PayloadTooLarge(msg string) *Error {
	if msg == "" {
		msg = "payload too large"
	}
	return &Error{Code: CodePayloadTooLarge, Message: msg}
}

// IdempotencyKeyReused returns a CodeIdempotencyKeyReused error. Produced by
// the §4.8 middleware when the same Idempotency-Key arrives with a different
// request body.
func IdempotencyKeyReused() *Error {
	return &Error{
		Code:    CodeIdempotencyKeyReused,
		Message: "idempotency key already used for a different request",
	}
}

// Internal returns a generic 500 with msg as the client-visible text. ALWAYS
// pair with WithCause(err) so the underlying failure makes it to slog+Sentry.
// Pass a generic message — never leak DB error details to the client.
func Internal(msg string) *Error {
	if msg == "" {
		msg = "internal error"
	}
	return &Error{Code: CodeInternal, Message: msg}
}

// --- validator integration ------------------------------------------------

// FromValidationErrors converts a go-playground/validator/v10 ValidationErrors
// (the type returned by validator.Struct(req)) into a CodeValidation error
// with one FieldError per field. Handler-side WriteError uses errors.As to
// detect this case and call this function.
//
// The mapping turns validator's "tag" (e.g. "required", "email", "min", "max",
// "url") into a stable machine code (REQUIRED, INVALID_FORMAT, TOO_SHORT,
// TOO_LONG, ...) so the frontend can switch on FieldError.Code without
// parsing english.
func FromValidationErrors(verrs validator.ValidationErrors) *Error {
	fields := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, FieldError{
			Field:   fieldPath(fe),
			Code:    fieldErrorCodeForTag(fe.Tag()),
			Message: validationMessage(fe),
		})
	}
	return Validation(fields)
}

// fieldPath turns a validator.FieldError into the dot-path the FieldError
// type documents — e.g. `user.email`, not just `email`. Namespace returns
// the full path including the top-level struct name (e.g.
// `RegisterRequest.Username`); we strip that prefix so the wire-side path
// looks like the JSON shape, then lowercase to match §4.6 conventions.
//
// When milestone 3.6 lands the handler validator we'll register a
// TagNameFunc so the JSON tag names take precedence over Go field names;
// this helper keeps working unchanged in that case because Namespace already
// honors the registered tag name.
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if idx := strings.IndexByte(ns, '.'); idx >= 0 {
		ns = ns[idx+1:]
	}
	return strings.ToLower(ns)
}

func fieldErrorCodeForTag(tag string) string {
	switch tag {
	case "required":
		return "REQUIRED"
	case "email":
		return "INVALID_FORMAT"
	case "url":
		return "INVALID_FORMAT"
	case "uuid", "uuid4", "uuid7":
		return "INVALID_FORMAT"
	case "min":
		return "TOO_SHORT"
	case "max":
		return "TOO_LONG"
	case "len":
		return "WRONG_LENGTH"
	case "oneof":
		return "INVALID_VALUE"
	case "alphanum":
		return "INVALID_FORMAT"
	default:
		return "INVALID"
	}
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case "email":
		return fmt.Sprintf("%s must be a valid email", fe.Field())
	case "url":
		return fmt.Sprintf("%s must be a valid URL", fe.Field())
	case "uuid", "uuid4", "uuid7":
		return fmt.Sprintf("%s must be a valid UUID", fe.Field())
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", fe.Field(), fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", fe.Field(), fe.Param())
	case "len":
		return fmt.Sprintf("%s must be exactly %s characters", fe.Field(), fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", fe.Field(), fe.Param())
	case "alphanum":
		return fmt.Sprintf("%s must contain only letters and numbers", fe.Field())
	default:
		return fmt.Sprintf("%s is invalid", fe.Field())
	}
}

// --- helpers --------------------------------------------------------------

// IsCode reports whether err — or any *Error wrapped anywhere in its chain —
// has the given Code. Useful for assertions in tests / branching in services.
//
// errors.As alone only finds the OUTERMOST *Error, so if a service stacks
// Internal("...").WithCause(NotFound("x")) we'd miss the inner CodeNotFound.
// This helper walks the chain manually and checks each *Error link in turn.
func IsCode(err error, code Code) bool {
	for err != nil {
		var e *Error
		if errors.As(err, &e) {
			if e.Code == code {
				return true
			}
			err = e.Unwrap()
			continue
		}
		err = errors.Unwrap(err)
	}
	return false
}
