package conditions

// Condition types for Stoker status.conditions[].type
const (
	// TypeReady indicates overall readiness — all gateways synced and healthy.
	TypeReady = "Ready"

	// TypeRefResolved indicates whether the git ref has been resolved to a commit SHA.
	TypeRefResolved = "RefResolved"

	// TypeAllGatewaysSynced indicates whether all discovered gateways have completed sync.
	TypeAllGatewaysSynced = "AllGatewaysSynced"

	// TypeSidecarInjected indicates whether all gateway pods have the stoker-agent sidecar.
	TypeSidecarInjected = "SidecarInjected"

	// SyncProfile condition types

	// TypeAccepted indicates whether the SyncProfile spec is valid.
	TypeAccepted = "Accepted"
)

// Condition reasons for Stoker status.conditions[].reason
const (
	ReasonReconciling         = "Reconciling"
	ReasonRefResolved         = "RefResolved"
	ReasonRefResolutionFailed = "RefResolutionFailed"
	ReasonSyncSucceeded       = "SyncSucceeded"
	ReasonSyncInProgress      = "SyncInProgress"
	ReasonPaused              = "Paused"
	ReasonNoGateways          = "NoGatewaysDiscovered"
	ReasonValidationPassed    = "ValidationPassed"
	ReasonValidationFailed    = "ValidationFailed"
	ReasonSidecarMissing      = "SidecarMissing"
	ReasonSidecarPresent      = "SidecarPresent"
	ReasonCycleDetected       = "CycleDetected"
	ReasonDependencyNotFound  = "DependencyNotFound"
)

// Event reasons for K8s Events (not used as condition reasons).
const (
	ReasonSyncCompleted           = "SyncCompleted"
	ReasonSyncFailed              = "SyncFailed"
	ReasonDesignerSessionsBlocked = "DesignerSessionsBlocked"
	ReasonWebhookReceived         = "WebhookReceived"
	ReasonCloneFailed             = "CloneFailed"
)
