/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package types

const (
	// Sync status values used by agents and mapped to CRD status.

	// SyncStatusPending indicates the agent is waiting to begin sync.
	SyncStatusPending = "Pending"

	// SyncStatusSyncing indicates the agent is actively syncing.
	SyncStatusSyncing = "Syncing"

	// SyncStatusSynced indicates the agent has successfully completed sync.
	SyncStatusSynced = "Synced"

	// SyncStatusError indicates the agent encountered an error during sync.
	SyncStatusError = "Error"
)

// GatewayStatus is the JSON payload each sync agent writes
// as a value in ConfigMap ignition-sync-status-{crName}.
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

	// SyncProfileName is the name of the SyncProfile used for this sync.
	SyncProfileName string `json:"syncProfileName,omitempty"`

	// DryRun indicates the sync was a dry-run (no files written to live dir).
	DryRun bool `json:"dryRun,omitempty"`

	// DryRunDiffAdded is the count of files that would be created.
	DryRunDiffAdded int32 `json:"dryRunDiffAdded,omitempty"`

	// DryRunDiffModified is the count of files that would be changed.
	DryRunDiffModified int32 `json:"dryRunDiffModified,omitempty"`

	// DryRunDiffDeleted is the count of files that would be removed.
	DryRunDiffDeleted int32 `json:"dryRunDiffDeleted,omitempty"`
}
