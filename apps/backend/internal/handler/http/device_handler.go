package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	devicesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/device"
)

// DeviceHandler hosts /v1/devices. Wraps the §11.4 device token service.
type DeviceHandler struct {
	devices *devicesvc.Service
	auth    *auth.Service
	v       *validator.Validate
}

// NewDeviceHandler wires the handler.
func NewDeviceHandler(devices *devicesvc.Service, a *auth.Service, v *validator.Validate) (*DeviceHandler, error) {
	if devices == nil {
		return nil, errors.New("httpapi: DeviceHandler requires non-nil device service")
	}
	if a == nil {
		return nil, errors.New("httpapi: DeviceHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: DeviceHandler requires non-nil validator")
	}
	return &DeviceHandler{devices: devices, auth: a, v: v}, nil
}

// Mount attaches device routes onto r.
func (h *DeviceHandler) Mount(r chi.Router) {
	r.Get("/v1/devices", h.List)
	r.Post("/v1/devices", h.Register)
	r.Delete("/v1/devices/{id}", h.Delete)
}

// List returns every device token registered to the caller. Used by
// the mobile settings/devices screen (WAKEUPEXPO.md §5.1) so the user
// can audit which devices have a push token on file and revoke any
// stale ones.
//
// @Summary      List my device tokens
// @Description  Returns every device token registered to the authenticated user, newest first. Pair with `DELETE /v1/devices/{id}` to revoke.
// @Tags         devices
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} DeviceTokenListResponse  "Device tokens"
// @Header       200  {string} X-Request-ID             "Echoed request id"
// @Failure      401  {object} ErrorResponse            "Not authenticated"
// @Failure      429  {object} ErrorResponse            "Rate limited"
// @Failure      500  {object} ErrorResponse            "Internal error"
// @Router       /v1/devices [get]
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	tokens, err := h.devices.ListForUser(r.Context(), uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := make([]DeviceTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toDeviceTokenResponse(t))
	}
	WriteJSON(w, http.StatusOK, DeviceTokenListResponse{Data: out})
}

// Register stores or refreshes the caller's Expo push token.
//
// @Summary      Register a device's Expo push token
// @Description  Stores (or refreshes) the caller's Expo push token + platform per §6.1. Idempotent on the (user_id, expo_token) pair: re-registering the same token from the same user updates `last_seen_at` and refreshes the platform rather than creating a duplicate row, so mobile clients can call this on every cold start without bloating the table. Returns the persisted row including its server-issued `id`, which the client uses to call DELETE /v1/devices/{id} on logout.
// @Tags         devices
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     RegisterDeviceRequest  true  "Token + platform"
// @Success      201      {object} DeviceTokenResponse  "Persisted device token"
// @Header       201      {string} X-Request-ID         "Echoed request id"
// @Failure      400      {object} ErrorResponse        "Malformed JSON or platform"
// @Failure      401      {object} ErrorResponse        "Not authenticated"
// @Failure      413      {object} ErrorResponse        "Request body too large"
// @Failure      422      {object} ErrorResponse        "Validation failed"
// @Failure      429      {object} ErrorResponse        "Rate limited"
// @Failure      500      {object} ErrorResponse        "Internal error"
// @Router       /v1/devices [post]
func (h *DeviceHandler) Register(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req RegisterDeviceRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	tok, err := h.devices.Register(r.Context(), uid, req.ExpoToken, domain.DevicePlatform(req.Platform))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, toDeviceTokenResponse(tok))
}

// Delete removes the caller's device token by id.
//
// @Summary      Delete a device token
// @Description  Removes the caller's device token by `id` (returned from POST /v1/devices). Scoped to the authenticated user — passing another user's id surfaces as 404 to avoid enumeration. The mobile client calls this on logout so we stop pushing to a device the user signed out of.
// @Tags         devices
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Device token id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "Device token not found"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/devices/{id} [delete]
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	deviceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	if err := h.devices.Delete(r.Context(), uid, deviceID); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}
