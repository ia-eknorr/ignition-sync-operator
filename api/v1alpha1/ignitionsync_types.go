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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================
// Utility Types
// ============================================================

// SecretKeyRef references a key within a Kubernetes Secret.
type SecretKeyRef struct {
	// name is the name of the Secret in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key is the key within the Secret data.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// ============================================================
// Git
// ============================================================

// GitSpec configures the source git repository.
type GitSpec struct {
	// repo is the git repository URL (SSH or HTTPS).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// ref is the git reference to sync — tag, branch, or commit SHA.
	// Typically managed by Kargo or a webhook.
	// +kubebuilder:validation:Required
	Ref string `json:"ref"`

	// auth configures git authentication. Exactly one method should be specified.
	// +optional
	Auth *GitAuthSpec `json:"auth,omitempty"`
}

// GitAuthSpec selects one git authentication method.
type GitAuthSpec struct {
	// sshKey authenticates via SSH deploy key.
	// +optional
	SSHKey *SSHKeyAuth `json:"sshKey,omitempty"`

	// githubApp authenticates via a GitHub App installation.
	// Enables bi-directional PR creation.
	// +optional
	GitHubApp *GitHubAppAuth `json:"githubApp,omitempty"`

	// token authenticates via a personal access token or service account token.
	// +optional
	Token *TokenAuth `json:"token,omitempty"`
}

// SSHKeyAuth references an SSH private key stored in a Secret.
type SSHKeyAuth struct {
	// secretRef points to the Secret containing the SSH private key.
	SecretRef SecretKeyRef `json:"secretRef"`
}

// GitHubAppAuth authenticates using a GitHub App installation.
type GitHubAppAuth struct {
	// appId is the GitHub App ID.
	AppID int64 `json:"appId"`

	// installationId is the GitHub App installation ID.
	InstallationID int64 `json:"installationId"`

	// privateKeySecretRef points to the Secret containing the App's private key PEM.
	PrivateKeySecretRef SecretKeyRef `json:"privateKeySecretRef"`
}

// TokenAuth references a token stored in a Secret.
type TokenAuth struct {
	// secretRef points to the Secret containing the git token.
	SecretRef SecretKeyRef `json:"secretRef"`
}

// ============================================================
// Webhook
// ============================================================

// WebhookSpec configures the inbound webhook receiver.
type WebhookSpec struct {
	// enabled controls whether the webhook listener is active.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// port is the webhook listener port.
	// +kubebuilder:default=8443
	// +optional
	Port int32 `json:"port,omitempty"`

	// secretRef points to the HMAC key Secret for payload verification.
	// +optional
	SecretRef *SecretKeyRef `json:"secretRef,omitempty"`
}

// ============================================================
// Polling
// ============================================================

// PollingSpec configures the fallback polling interval for git changes.
type PollingSpec struct {
	// enabled controls whether periodic polling is active.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// interval is the polling period (e.g., "60s", "5m").
	// +kubebuilder:default="60s"
	// +optional
	Interval string `json:"interval,omitempty"`
}

// ============================================================
// Gateway
// ============================================================

// GatewaySpec configures how the operator connects to Ignition gateways.
// These settings apply to all discovered gateways; individual gateways
// can override via pod annotations.
type GatewaySpec struct {
	// port is the Ignition gateway API port.
	// +kubebuilder:default=8043
	// +optional
	Port int32 `json:"port,omitempty"`

	// tls enables TLS for gateway API connections.
	// +kubebuilder:default=true
	// +optional
	TLS *bool `json:"tls,omitempty"`

	// apiKeySecretRef points to the Secret containing the Ignition API key.
	APIKeySecretRef SecretKeyRef `json:"apiKeySecretRef"`
}

// ============================================================
// Shared Resources
// ============================================================

// SharedSpec configures resources that are synced to all discovered gateways.
type SharedSpec struct {
	// externalResources configures sync of external resource config files.
	// +optional
	ExternalResources *ExternalResourcesSpec `json:"externalResources,omitempty"`

	// scripts configures sync of shared Python scripts.
	// +optional
	Scripts *ScriptsSpec `json:"scripts,omitempty"`

	// udts configures sync of shared UDT definitions.
	// +optional
	UDTs *UDTsSpec `json:"udts,omitempty"`
}

// ExternalResourcesSpec configures external resource file synchronization.
type ExternalResourcesSpec struct {
	// enabled controls whether external resource sync is active.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// source is the repo-relative path to the external resources directory.
	// +optional
	Source string `json:"source,omitempty"`

	// createFallback creates a minimal config-mode.json if the source doesn't exist.
	// +optional
	CreateFallback bool `json:"createFallback,omitempty"`
}

// ScriptsSpec configures shared script synchronization.
type ScriptsSpec struct {
	// enabled controls whether script sync is active.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// source is the repo-relative path to the scripts directory.
	// +optional
	Source string `json:"source,omitempty"`

	// destPath is the destination path relative to /ignition-data/projects/{projectName}/.
	// +optional
	DestPath string `json:"destPath,omitempty"`
}

// UDTsSpec configures shared UDT synchronization.
type UDTsSpec struct {
	// enabled controls whether UDT sync is active.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// source is the repo-relative path to the UDT definitions directory.
	// +optional
	Source string `json:"source,omitempty"`
}

// ============================================================
// Additional Files
// ============================================================

// AdditionalFile defines an arbitrary file or directory to sync.
type AdditionalFile struct {
	// source is the repo-relative path to the source file or directory.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`

	// dest is the destination path relative to /ignition-data/.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Dest string `json:"dest"`

	// type is the entry type — "file" or "dir".
	// +kubebuilder:default="file"
	// +kubebuilder:validation:Enum=file;dir
	// +optional
	Type string `json:"type,omitempty"`
}

// ============================================================
// Config Normalization
// ============================================================

// NormalizeSpec configures config.json field normalization during sync.
type NormalizeSpec struct {
	// systemName enables automatic systemName replacement in config.json files.
	// +optional
	SystemName bool `json:"systemName,omitempty"`

	// fields is a list of additional JSON field replacements.
	// +optional
	Fields []FieldReplacement `json:"fields,omitempty"`
}

// FieldReplacement defines a JSON field to replace in config files.
type FieldReplacement struct {
	// jsonPath is the dot-notation path to the field (e.g., ".someField").
	JSONPath string `json:"jsonPath"`

	// valueTemplate is a Go template for the replacement value.
	ValueTemplate string `json:"valueTemplate"`
}

// ============================================================
// Bi-Directional Sync
// ============================================================

// BidirectionalSpec configures gateway-to-git reverse sync.
// This is experimental and may change in v1beta1.
type BidirectionalSpec struct {
	// enabled activates bi-directional sync.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// watchPaths is an allowlist of gateway filesystem paths to watch.
	// Only paths in this list can flow back to git.
	// +optional
	WatchPaths []string `json:"watchPaths,omitempty"`

	// targetBranch is the branch to push gateway changes to.
	// Supports Go templates (e.g., "gateway-changes/{{.Namespace}}").
	// +optional
	TargetBranch string `json:"targetBranch,omitempty"`

	// debounce is the wait time after the last change before creating a PR.
	// +optional
	Debounce string `json:"debounce,omitempty"`

	// createPR controls whether changes are submitted as a pull request.
	// +optional
	CreatePR bool `json:"createPR,omitempty"`

	// prLabels are labels applied to auto-generated PRs.
	// +optional
	PRLabels []string `json:"prLabels,omitempty"`

	// conflictStrategy determines how conflicts between git and gateway are resolved.
	// +kubebuilder:default="gitWins"
	// +kubebuilder:validation:Enum=gitWins;gatewayWins;manual
	// +optional
	ConflictStrategy string `json:"conflictStrategy,omitempty"`

	// guardrails set limits to prevent accidental data exfiltration or repo bloat.
	// +optional
	Guardrails *BidirectionalGuardrailsSpec `json:"guardrails,omitempty"`
}

// BidirectionalGuardrailsSpec defines safety limits for reverse sync.
type BidirectionalGuardrailsSpec struct {
	// maxFileSize is the maximum size per file pushed to git (e.g., "10Mi").
	// +kubebuilder:default="10Mi"
	// +optional
	MaxFileSize string `json:"maxFileSize,omitempty"`

	// maxFilesPerPR is the maximum number of files per auto-generated PR.
	// +kubebuilder:default=100
	// +optional
	MaxFilesPerPR int32 `json:"maxFilesPerPR,omitempty"`

	// excludePatterns are glob patterns for files that should never be pushed to git.
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`
}

// ============================================================
// Validation & Safety
// ============================================================

// ValidationSpec configures pre-sync validation checks.
type ValidationSpec struct {
	// dryRunBefore runs a dry-run sync before applying changes.
	// +optional
	DryRunBefore bool `json:"dryRunBefore,omitempty"`

	// webhook configures an external validation webhook called before sync.
	// +optional
	Webhook *ValidationWebhookSpec `json:"webhook,omitempty"`
}

// ValidationWebhookSpec configures an external pre-sync validation endpoint.
type ValidationWebhookSpec struct {
	// url is the validation webhook endpoint.
	// +optional
	URL string `json:"url,omitempty"`

	// timeout is the maximum time to wait for a validation response.
	// +kubebuilder:default="10s"
	// +optional
	Timeout string `json:"timeout,omitempty"`
}

// ============================================================
// Snapshots & Rollback
// ============================================================

// SnapshotSpec configures pre-sync snapshot creation for rollback.
// This is experimental and may change in v1beta1.
type SnapshotSpec struct {
	// enabled activates pre-sync snapshots.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// retentionCount is the number of snapshots to retain.
	// +kubebuilder:default=5
	// +optional
	RetentionCount int32 `json:"retentionCount,omitempty"`

	// storage configures where snapshots are stored.
	// +optional
	Storage *SnapshotStorageSpec `json:"storage,omitempty"`
}

// SnapshotStorageSpec configures snapshot storage backend.
type SnapshotStorageSpec struct {
	// type selects the storage backend.
	// +kubebuilder:default="pvc"
	// +kubebuilder:validation:Enum=pvc;s3;gcs
	// +optional
	Type string `json:"type,omitempty"`

	// s3 configures S3-compatible snapshot storage.
	// +optional
	S3 *S3StorageSpec `json:"s3,omitempty"`
}

// S3StorageSpec configures S3-compatible storage for snapshots.
type S3StorageSpec struct {
	// bucket is the S3 bucket name.
	// +optional
	Bucket string `json:"bucket,omitempty"`

	// keyPrefix is the prefix for snapshot objects in the bucket.
	// +optional
	KeyPrefix string `json:"keyPrefix,omitempty"`
}

// ============================================================
// Deployment Strategy
// ============================================================

// DeploymentStrategySpec configures how syncs are rolled out across gateways.
// This is experimental and may change in v1beta1.
type DeploymentStrategySpec struct {
	// strategy selects the rollout strategy.
	// +kubebuilder:default="all-at-once"
	// +kubebuilder:validation:Enum="all-at-once";canary
	// +optional
	Strategy string `json:"strategy,omitempty"`

	// stages defines ordered deployment stages for canary rollouts.
	// +optional
	Stages []DeploymentStage `json:"stages,omitempty"`

	// syncOrder defines dependency-aware sync ordering by gateway name.
	// +optional
	SyncOrder []string `json:"syncOrder,omitempty"`

	// autoRollback configures automatic rollback on failure.
	// +optional
	AutoRollback *AutoRollbackSpec `json:"autoRollback,omitempty"`
}

// DeploymentStage defines a single stage in a canary deployment.
type DeploymentStage struct {
	// name identifies this deployment stage.
	Name string `json:"name"`

	// gatewaySelector selects gateways for this stage by label.
	// +optional
	GatewaySelector map[string]string `json:"gatewaySelector,omitempty"`
}

// AutoRollbackSpec configures automatic rollback behavior.
type AutoRollbackSpec struct {
	// enabled activates automatic rollback.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// triggers lists events that trigger a rollback (e.g., "scanFailure").
	// +optional
	Triggers []string `json:"triggers,omitempty"`
}

// ============================================================
// Ignition-Specific
// ============================================================

// IgnitionSpec configures Ignition-specific behavior during sync.
type IgnitionSpec struct {
	// designerSessionPolicy controls behavior when Designer sessions are active.
	// +kubebuilder:default="wait"
	// +kubebuilder:validation:Enum=wait;proceed;fail
	// +optional
	DesignerSessionPolicy string `json:"designerSessionPolicy,omitempty"`

	// perspectiveSessionPolicy controls behavior for active Perspective sessions.
	// +optional
	PerspectiveSessionPolicy string `json:"perspectiveSessionPolicy,omitempty"`

	// redundancyRole restricts sync to gateways with this redundancy role.
	// +optional
	RedundancyRole string `json:"redundancyRole,omitempty"`

	// peerGatewayName is the name of the redundancy peer gateway.
	// +optional
	PeerGatewayName string `json:"peerGatewayName,omitempty"`
}

// ============================================================
// Agent
// ============================================================

// AgentSpec configures the sync agent sidecar container.
type AgentSpec struct {
	// image configures the agent container image.
	// +optional
	Image *AgentImageSpec `json:"image,omitempty"`

	// resources configures compute resources for the agent container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AgentImageSpec configures the agent container image reference.
type AgentImageSpec struct {
	// repository is the container image repository.
	// +kubebuilder:default="ghcr.io/inductiveautomation/ignition-sync-agent"
	// +optional
	Repository string `json:"repository,omitempty"`

	// tag is the container image tag.
	// +optional
	Tag string `json:"tag,omitempty"`

	// pullPolicy is the image pull policy.
	// +kubebuilder:default="IfNotPresent"
	// +optional
	PullPolicy string `json:"pullPolicy,omitempty"`

	// digest is an optional pinned image digest for supply chain security.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// ============================================================
// Top-Level Spec
// ============================================================

// IgnitionSyncSpec defines the desired state of IgnitionSync.
type IgnitionSyncSpec struct {
	// === Stable fields — will not change in v1beta1 ===

	// git configures the source repository.
	// +kubebuilder:validation:Required
	Git GitSpec `json:"git"`

	// webhook configures the inbound webhook receiver.
	// +optional
	Webhook WebhookSpec `json:"webhook,omitempty"`

	// polling configures the fallback git polling interval.
	// +optional
	Polling PollingSpec `json:"polling,omitempty"`

	// gateway configures how the operator connects to Ignition gateways.
	Gateway GatewaySpec `json:"gateway"`

	// siteNumber identifies this site for config normalization.
	// +optional
	SiteNumber string `json:"siteNumber,omitempty"`

	// shared configures resources synced to all discovered gateways.
	// +optional
	Shared SharedSpec `json:"shared,omitempty"`

	// additionalFiles defines arbitrary file/directory syncs.
	// +optional
	AdditionalFiles []AdditionalFile `json:"additionalFiles,omitempty"`

	// excludePatterns are glob patterns for files to exclude from sync.
	// The pattern "**/.resources/**" is always enforced by the agent even if omitted.
	// +kubebuilder:default={"**/.git/","**/.gitkeep","**/.resources/**"}
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// normalize configures config.json field normalization.
	// +optional
	Normalize NormalizeSpec `json:"normalize,omitempty"`

	// === Experimental fields — may change in v1beta1 ===

	// bidirectional configures gateway-to-git reverse sync.
	// +optional
	Bidirectional *BidirectionalSpec `json:"bidirectional,omitempty"`

	// validation configures pre-sync validation checks.
	// +optional
	Validation ValidationSpec `json:"validation,omitempty"`

	// snapshots configures pre-sync snapshot creation for rollback.
	// +optional
	Snapshots *SnapshotSpec `json:"snapshots,omitempty"`

	// deployment configures how syncs are rolled out across gateways.
	// +optional
	Deployment *DeploymentStrategySpec `json:"deployment,omitempty"`

	// paused halts all sync operations when set to true.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// ignition configures Ignition-specific behavior during sync.
	// +optional
	Ignition IgnitionSpec `json:"ignition,omitempty"`

	// agent configures the sync agent sidecar container.
	// +optional
	Agent AgentSpec `json:"agent,omitempty"`
}

// ============================================================
// Status Types
// ============================================================

// DiscoveredGateway represents a single Ignition gateway discovered by the controller.
type DiscoveredGateway struct {
	// name is the gateway identity (from annotation or app.kubernetes.io/name label).
	Name string `json:"name"`

	// namespace is the namespace of the gateway pod.
	Namespace string `json:"namespace"`

	// podName is the name of the gateway pod.
	PodName string `json:"podName"`

	// servicePath is the repo-relative path to this gateway's service directory.
	// +optional
	ServicePath string `json:"servicePath,omitempty"`

	// syncProfile is the name of the SyncProfile referenced by this gateway.
	// +optional
	SyncProfile string `json:"syncProfile,omitempty"`

	// syncStatus is the current sync state of this gateway.
	// +kubebuilder:validation:Enum=Pending;Syncing;Synced;Error
	// +optional
	SyncStatus string `json:"syncStatus,omitempty"`

	// lastSyncTime is when this gateway was last synced.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// lastSyncDuration is how long the last sync took.
	// +optional
	LastSyncDuration string `json:"lastSyncDuration,omitempty"`

	// syncedCommit is the git commit SHA currently synced to this gateway.
	// +optional
	SyncedCommit string `json:"syncedCommit,omitempty"`

	// syncedRef is the git ref currently synced to this gateway.
	// +optional
	SyncedRef string `json:"syncedRef,omitempty"`

	// agentVersion is the version of the sync agent on this gateway.
	// +optional
	AgentVersion string `json:"agentVersion,omitempty"`

	// lastScanResult summarizes the last Ignition scan API response.
	// +optional
	LastScanResult string `json:"lastScanResult,omitempty"`

	// filesChanged is the number of files changed in the last sync.
	// +optional
	FilesChanged int32 `json:"filesChanged,omitempty"`

	// projectsSynced lists the Ignition project names synced to this gateway.
	// +optional
	ProjectsSynced []string `json:"projectsSynced,omitempty"`

	// lastSnapshot is the most recent pre-sync snapshot for this gateway.
	// +optional
	LastSnapshot *GatewaySnapshot `json:"lastSnapshot,omitempty"`

	// syncHistory is a bounded list of recent sync results.
	// +optional
	SyncHistory []SyncHistoryEntry `json:"syncHistory,omitempty"`
}

// GatewaySnapshot records metadata about a pre-sync snapshot.
type GatewaySnapshot struct {
	// id is the snapshot identifier.
	// +optional
	ID string `json:"id,omitempty"`

	// size is the snapshot size (e.g., "256MB").
	// +optional
	Size string `json:"size,omitempty"`

	// timestamp is when the snapshot was taken.
	// +optional
	Timestamp *metav1.Time `json:"timestamp,omitempty"`
}

// SyncHistoryEntry records a single sync operation result.
type SyncHistoryEntry struct {
	// timestamp is when the sync occurred.
	// +optional
	Timestamp *metav1.Time `json:"timestamp,omitempty"`

	// commit is the git commit SHA that was synced.
	// +optional
	Commit string `json:"commit,omitempty"`

	// result is the sync outcome (e.g., "success", "error").
	// +optional
	Result string `json:"result,omitempty"`

	// duration is how long the sync took.
	// +optional
	Duration string `json:"duration,omitempty"`
}

// IgnitionSyncStatus defines the observed state of IgnitionSync.
type IgnitionSyncStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// lastSyncTime is when the most recent sync completed.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// lastSyncRef is the git ref that was last synced.
	// +optional
	LastSyncRef string `json:"lastSyncRef,omitempty"`

	// lastSyncCommit is the git commit SHA that was last synced.
	// +optional
	LastSyncCommit string `json:"lastSyncCommit,omitempty"`

	// refResolutionStatus indicates the state of git ref resolution.
	// +kubebuilder:validation:Enum=NotResolved;Resolving;Resolved;Error
	// +optional
	RefResolutionStatus string `json:"refResolutionStatus,omitempty"`

	// discoveredGateways lists all gateways discovered by the controller in this namespace.
	// +optional
	DiscoveredGateways []DiscoveredGateway `json:"discoveredGateways,omitempty"`

	// conditions represent the current state of the IgnitionSync resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ============================================================
// Root Objects
// ============================================================

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=isync;igs
// +kubebuilder:printcolumn:name="Ref",type="string",JSONPath=`.spec.git.ref`
// +kubebuilder:printcolumn:name="Synced",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].status`
// +kubebuilder:printcolumn:name="Gateways",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].message`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`

// IgnitionSync is the Schema for the ignitionsyncs API.
type IgnitionSync struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IgnitionSync.
	// +required
	Spec IgnitionSyncSpec `json:"spec"`

	// status defines the observed state of IgnitionSync.
	// +optional
	Status IgnitionSyncStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// IgnitionSyncList contains a list of IgnitionSync.
type IgnitionSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IgnitionSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IgnitionSync{}, &IgnitionSyncList{})
}
