package httpapi

// HealthzResponse is the body of GET /v1/healthz. The mobile force-
// upgrade gate (WAKEUPEXPO.md §4.10) polls this on every authenticated
// foreground; if `min_client_version` is non-empty and greater than the
// installed app version, the client renders a blocking "update
// required" modal.
//
// Empty `min_client_version` means "no minimum, every client is OK."
type HealthzResponse struct {
	Status           string `json:"status"             example:"ok"`
	MinClientVersion string `json:"min_client_version" example:"1.0.0"`
}
