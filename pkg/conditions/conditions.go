package conditions

// Condition types for IgnitionSync status.conditions[].type
const (
	// TypeReady indicates overall readiness — all gateways synced and healthy.
	TypeReady = "Ready"

	// TypeRefResolved indicates whether the git ref has been resolved to a commit SHA.
	TypeRefResolved = "RefResolved"

	// TypeAllGatewaysSynced indicates whether all discovered gateways have completed sync.
	TypeAllGatewaysSynced = "AllGatewaysSynced"

	// TypeSidecarInjected indicates whether all gateway pods have the sync-agent sidecar.
	TypeSidecarInjected = "SidecarInjected"

	// SyncProfile condition types

	// TypeAccepted indicates whether the SyncProfile spec is valid.
	TypeAccepted = "Accepted"
)

// Condition reasons for IgnitionSync status.conditions[].reason
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
