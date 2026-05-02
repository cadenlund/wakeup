package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
)

// AuthHandler hosts every /v1/auth/* endpoint. Composes the auth service
// (§3.2) and the package-level validator. Goroutine-safe; instantiate
// once in cmd/server/main.go.
type AuthHandler struct {
	svc *auth.Service
	v   *validator.Validate
}

// NewAuthHandler wires up the handler. The validator can be shared
// across all handlers — `httpapi.NewValidator()` returns one configured
// to use JSON tag names so apierror.FieldError paths match the wire.
func NewAuthHandler(svc *auth.Service, v *validator.Validate) (*AuthHandler, error) {
	if svc == nil {
		return nil, errors.New("httpapi: AuthHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: AuthHandler requires non-nil validator")
	}
	return &AuthHandler{svc: svc, v: v}, nil
}

// Mount attaches every /v1/auth/* route onto r. Caller controls the
// outer middleware chain (CORS, session-load, rate-limit, etc.).
func (h *AuthHandler) Mount(r chi.Router) {
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", h.Register)
		r.Post("/login", h.Login)
		r.Post("/logout", h.Logout)
		r.Post("/logout-all", h.LogoutAll)
		r.Get("/me", h.Me)
		r.Post("/password-reset/request", h.RequestPasswordReset)
		r.Post("/password-reset/confirm", h.ConfirmPasswordReset)
	})
}

// Register creates a new user account and starts a session.
//
// @Summary      Register a new user
// @Description  Creates a new user account, hashes the password with argon2id, and binds the new session cookie.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        Idempotency-Key  header   string             false  "Idempotency key (UUID v7); enables safe retries"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request          body     RegisterRequest    true   "Registration payload"
// @Success      201              {object} RegisterResponse   "Created"
// @Header       201              {string} X-Request-ID       "Echoed request id"
// @Failure      400              {object} apierror.Error     "Malformed JSON / empty body"
// @Failure      409              {object} apierror.Error     "Username or email already taken"
// @Failure      413              {object} apierror.Error     "Request body too large"
// @Failure      422              {object} apierror.Error     "Validation failed"
// @Failure      429              {object} apierror.Error     "Rate limited"
// @Failure      500              {object} apierror.Error     "Internal error"
// @Router       /v1/auth/register [post]
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := DecodeJSON(r, h.v, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	created, err := h.svc.Register(r.Context(), auth.RegisterParams{
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Password:    req.Password,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, RegisterResponse{User: toMeResponse(created)})
}

// Login validates credentials and binds the session cookie.
//
// @Summary      Log in
// @Description  Verifies credentials and binds a session cookie. Same generic 401 for any failure path (no enumeration).
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body     LoginRequest    true  "Login payload"
// @Success      200      {object} LoginResponse   "Authenticated"
// @Header       200      {string} X-Request-ID    "Echoed request id"
// @Failure      400      {object} apierror.Error  "Malformed JSON / empty body"
// @Failure      401      {object} apierror.Error  "Invalid credentials"
// @Failure      413      {object} apierror.Error  "Request body too large"
// @Failure      422      {object} apierror.Error  "Validation failed"
// @Failure      429      {object} apierror.Error  "Rate limited"
// @Failure      500      {object} apierror.Error  "Internal error"
// @Router       /v1/auth/login [post]
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := DecodeJSON(r, h.v, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	u, err := h.svc.Login(r.Context(), auth.LoginParams{
		Identifier: req.Identifier,
		Password:   req.Password,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, LoginResponse{User: toMeResponse(u)})
}

// Logout destroys the current session. Idempotent — missing session is
// not an error, the response is still 204.
//
// @Summary      Log out current session
// @Description  Destroys the current session cookie. Idempotent — missing session returns 204.
// @Tags         auth
// @Produce      json
// @Security     CookieAuth
// @Success      204  "No Content"
// @Header       204  {string} X-Request-ID  "Echoed request id"
// @Failure      429  {object} apierror.Error "Rate limited"
// @Failure      500  {object} apierror.Error "Internal error"
// @Router       /v1/auth/logout [post]
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// LogoutAll destroys every session belonging to the authenticated user.
//
// @Summary      Log out every active session for this user
// @Description  Destroys every active session for the authenticated user. Used for "sign out everywhere" or post-password-reset cleanup.
// @Tags         auth
// @Produce      json
// @Security     CookieAuth
// @Success      204  "No Content"
// @Header       204  {string} X-Request-ID  "Echoed request id"
// @Failure      401  {object} apierror.Error "Not authenticated"
// @Failure      429  {object} apierror.Error "Rate limited"
// @Failure      500  {object} apierror.Error "Internal error"
// @Router       /v1/auth/logout-all [post]
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	uid, err := h.svc.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if err := h.svc.LogoutAll(r.Context(), uid); err != nil {
		WriteError(w, r, err)
		return
	}
	// Also destroy the session bound to THIS request so the caller's
	// cookie stops working immediately — LogoutAll iterates the store
	// but the in-flight session is loaded into ctx and gets re-saved
	// by the LoadAndSave middleware otherwise.
	if err := h.svc.Logout(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// Me returns the authenticated user's self view.
//
// @Summary      Get authenticated user
// @Description  Returns the user backing the active session. Use this on app launch to populate the local user store.
// @Tags         auth
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} MeResponse     "Authenticated user"
// @Header       200  {string} X-Request-ID   "Echoed request id"
// @Failure      401  {object} apierror.Error "Not authenticated"
// @Failure      429  {object} apierror.Error "Rate limited"
// @Failure      500  {object} apierror.Error "Internal error"
// @Router       /v1/auth/me [get]
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.Me(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toMeResponse(u))
}

// RequestPasswordReset emails a reset link if the email belongs to a
// real user. Always 204 (no enumeration) per §6.2.
//
// @Summary      Request a password reset
// @Description  Generates a reset token and emails it. Always returns 204 even when the email is unknown — defeats account enumeration.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body     PasswordResetRequestRequest  true  "Email to send the reset link to"
// @Success      204                "No Content"
// @Header       204      {string}  X-Request-ID                       "Echoed request id"
// @Failure      400      {object}  apierror.Error                     "Malformed JSON / empty body"
// @Failure      413      {object}  apierror.Error                     "Request body too large"
// @Failure      422      {object}  apierror.Error                     "Validation failed"
// @Failure      429      {object}  apierror.Error                     "Rate limited"
// @Failure      500      {object}  apierror.Error                     "Internal error"
// @Router       /v1/auth/password-reset/request [post]
func (h *AuthHandler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetRequestRequest
	if err := DecodeJSON(r, h.v, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	// The service swallows errors deliberately for the always-204 contract;
	// we just re-assert that contract here even if the service ever changes.
	_ = h.svc.RequestPasswordReset(r.Context(), req.Email)
	WriteNoContent(w)
}

// ConfirmPasswordReset consumes the token and sets the new password.
//
// @Summary      Confirm a password reset
// @Description  Sets the user's new password if the token is valid + unused. Returns generic 401 on any failure (no oracle).
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body     PasswordResetConfirmRequest  true  "Token + new password"
// @Success      204                "No Content"
// @Header       204      {string}  X-Request-ID                       "Echoed request id"
// @Failure      400      {object}  apierror.Error                     "Malformed JSON / empty body"
// @Failure      401      {object}  apierror.Error                     "Invalid or expired reset token"
// @Failure      413      {object}  apierror.Error                     "Request body too large"
// @Failure      422      {object}  apierror.Error                     "Validation failed"
// @Failure      429      {object}  apierror.Error                     "Rate limited"
// @Failure      500      {object}  apierror.Error                     "Internal error"
// @Router       /v1/auth/password-reset/confirm [post]
func (h *AuthHandler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetConfirmRequest
	if err := DecodeJSON(r, h.v, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := h.svc.ConfirmPasswordReset(r.Context(), auth.ConfirmPasswordResetParams{
		Token:       req.Token,
		NewPassword: req.NewPassword,
	}); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}
