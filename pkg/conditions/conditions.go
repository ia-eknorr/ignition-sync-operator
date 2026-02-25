package conditions

// Condition types for GatewaySync status.conditions[].type
const (
	// TypeReady indicates overall readiness — all gateways synced and healthy.
	TypeReady = "Ready"

	// TypeRefResolved indicates whether the git ref has been resolved to a commit SHA.
	TypeRefResolved = "RefResolved"

	// TypeProfilesValid indicates whether all embedded profiles pass validation.
	TypeProfilesValid = "ProfilesValid"

	// TypeAllGatewaysSynced indicates whether all discovered gateways have completed sync.
	TypeAllGatewaysSynced = "AllGatewaysSynced"

	// TypeSidecarInjected indicates whether all gateway pods have the stoker-agent sidecar.
	TypeSidecarInjected = "SidecarInjected"
)

// Condition reasons for GatewaySync status.conditions[].reason
const (
	ReasonReconciling             = "Reconciling"
	ReasonRefResolved             = "RefResolved"
	ReasonRefResolutionFailed     = "RefResolutionFailed"
	ReasonSyncSucceeded           = "SyncSucceeded"
	ReasonSyncInProgress          = "SyncInProgress"
	ReasonPaused                  = "Paused"
	ReasonNoGateways              = "NoGatewaysDiscovered"
	ReasonProfilesValid           = "ProfilesValid"
	ReasonProfilesInvalid         = "ProfilesInvalid"
	ReasonValidationPassed        = "ValidationPassed"
	ReasonValidationFailed        = "ValidationFailed"
	ReasonSidecarMissing          = "SidecarMissing"
	ReasonSidecarPresent          = "SidecarPresent"
	ReasonGitHubAppExchangeFailed = "GitHubAppExchangeFailed"
)

// Event reasons for K8s Events (not used as condition reasons).
const (
	ReasonSyncCompleted           = "SyncCompleted"
	ReasonSyncFailed              = "SyncFailed"
	ReasonDesignerSessionsBlocked = "DesignerSessionsBlocked"
	ReasonWebhookReceived         = "WebhookReceived"
	ReasonCloneFailed             = "CloneFailed"
)
