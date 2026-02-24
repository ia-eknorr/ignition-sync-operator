package types

const (
	// Sync status values used by agents and mapped to CRD status.

	// SyncStatusPending indicates the agent is waiting to begin sync.
	SyncStatusPending = "Pending"

	// SyncStatusSynced indicates the agent has successfully completed sync.
	SyncStatusSynced = "Synced"

	// SyncStatusError indicates the agent encountered an error during sync.
	SyncStatusError = "Error"
)

// GatewayStatus is the JSON payload each sync agent writes
// as a value in ConfigMap stoker-status-{crName}.
// Key = gateway name, Value = JSON of this struct.
type GatewayStatus struct {
	// SyncStatus is the current sync state (Pending, Syncing, Synced, Error).
	SyncStatus string `json:"syncStatus"`

	// SyncedCommit is the git commit SHA currently synced to this gateway.
	SyncedCommit string `json:"syncedCommit"`

	// SyncedRef is the git ref currently synced to this gateway.
	SyncedRef string `json:"syncedRef"`

	// LastSyncTime is when this gateway was last synced (RFC3339 format).
	LastSyncTime string `json:"lastSyncTime"`

	// LastSyncDuration is how long the last sync took (e.g., "2.5s").
	LastSyncDuration string `json:"lastSyncDuration"`

	// AgentVersion is the version of the sync agent on this gateway.
	AgentVersion string `json:"agentVersion"`

	// LastScanResult summarizes the last Ignition scan API response.
	LastScanResult string `json:"lastScanResult"`

	// FilesChanged is the number of files changed in the last sync.
	FilesChanged int32 `json:"filesChanged"`

	// ProjectsSynced lists the Ignition project names synced to this gateway.
	ProjectsSynced []string `json:"projectsSynced"`

	// ErrorMessage contains error details if SyncStatus is Error.
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ProfileName is the name of the sync profile used for this sync.
	ProfileName string `json:"profileName,omitempty"`

	// DryRun indicates the sync was a dry-run (no files written to live dir).
	DryRun bool `json:"dryRun,omitempty"`

	// DryRunDiffAdded is the count of files that would be created.
	DryRunDiffAdded int32 `json:"dryRunDiffAdded,omitempty"`

	// DryRunDiffModified is the count of files that would be changed.
	DryRunDiffModified int32 `json:"dryRunDiffModified,omitempty"`

	// DryRunDiffDeleted is the count of files that would be removed.
	DryRunDiffDeleted int32 `json:"dryRunDiffDeleted,omitempty"`

	// DesignerSessionsBlocked indicates the agent is waiting for designer sessions to close.
	DesignerSessionsBlocked bool `json:"designerSessionsBlocked,omitempty"`
}
