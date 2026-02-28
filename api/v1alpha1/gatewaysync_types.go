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

	// apiBaseURL is the GitHub API base URL. Defaults to https://api.github.com.
	// Set this for GitHub Enterprise Server (e.g. https://github.example.com/api/v3).
	// +optional
	APIBaseURL string `json:"apiBaseURL,omitempty"`
}

// TokenAuth references a token stored in a Secret.
type TokenAuth struct {
	// secretRef points to the Secret containing the git token.
	SecretRef SecretKeyRef `json:"secretRef"`
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
	// +kubebuilder:default=8088
	// +optional
	Port int32 `json:"port,omitempty"`

	// tls enables TLS for gateway API connections.
	// +kubebuilder:default=false
	// +optional
	TLS *bool `json:"tls,omitempty"`

	// api configures the Ignition gateway API key secret.
	API GatewayAPISpec `json:"api"`
}

// GatewayAPISpec references the Secret containing the Ignition API key.
type GatewayAPISpec struct {
	// secretName is the name of the Secret containing the Ignition API key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// secretKey is the key within the Secret. Defaults to "apiKey".
	// +kubebuilder:default="apiKey"
	// +optional
	SecretKey string `json:"secretKey,omitempty"`
}

// ============================================================
// Agent
// ============================================================

// AgentSpec configures the sync agent sidecar injected by the webhook.
type AgentSpec struct {
	// image configures the agent container image.
	// +optional
	Image AgentImageSpec `json:"image,omitempty"`

	// resources configures the agent container resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AgentImageSpec configures the agent container image.
// When unset, the webhook falls through to the DEFAULT_AGENT_IMAGE env var
// (set by the Helm chart's agentImage values).
type AgentImageSpec struct {
	// repository is the container image repository.
	// +optional
	Repository string `json:"repository,omitempty"`

	// tag is the container image tag.
	// +optional
	Tag string `json:"tag,omitempty"`

	// pullPolicy is the image pull policy.
	// +optional
	PullPolicy string `json:"pullPolicy,omitempty"`
}

// ============================================================
// Sync Configuration
// ============================================================

// SyncSpec configures file sync behavior and profiles.
type SyncSpec struct {
	// defaults provides baseline settings inherited by all profiles unless overridden.
	// +optional
	Defaults SyncDefaults `json:"defaults,omitempty"`

	// profiles is a named map of sync profiles. Each profile defines mappings
	// and optional behavioral overrides. Pods select a profile via the
	// stoker.io/profile annotation. The "default" profile is used as fallback.
	// +kubebuilder:validation:MinProperties=1
	Profiles map[string]SyncProfileSpec `json:"profiles"`
}

// SyncDefaults provides baseline settings inherited by all profiles unless overridden.
type SyncDefaults struct {
	// excludePatterns are glob patterns for files to exclude from sync.
	// The pattern "**/.resources/**" is always enforced by the agent even if omitted.
	// +kubebuilder:default={"**/.git/","**/.gitkeep","**/.resources/**"}
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// vars provides default template variables inherited by all profiles.
	// Profile-level vars override these on a per-key basis; unmatched keys are inherited.
	// +optional
	Vars map[string]string `json:"vars,omitempty"`

	// syncPeriod is the agent-side polling interval in seconds.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=3600
	// +optional
	SyncPeriod int32 `json:"syncPeriod,omitempty"`

	// designerSessionPolicy controls sync behavior when Ignition Designer
	// sessions are active. "proceed" (default) logs a warning and continues,
	// "wait" retries until sessions close (up to 5 min), "fail" aborts the sync.
	// +kubebuilder:default="proceed"
	// +kubebuilder:validation:Enum=proceed;wait;fail
	// +optional
	DesignerSessionPolicy string `json:"designerSessionPolicy,omitempty"`

	// dryRun causes the agent to sync to a staging directory without
	// copying to /ignition-data/.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// paused halts sync for all gateways using profiles that don't
	// explicitly override this setting.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// SyncProfileSpec defines a sync profile's configuration.
type SyncProfileSpec struct {
	// mappings is an ordered list of source->destination file mappings.
	// +kubebuilder:validation:MinItems=1
	Mappings []SyncMapping `json:"mappings"`

	// excludePatterns are additional glob patterns for files to exclude.
	// Merged with defaults.excludePatterns (additive).
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// vars is a map of template variables resolved by the agent at sync time.
	// +optional
	Vars map[string]string `json:"vars,omitempty"`

	// syncPeriod overrides defaults.syncPeriod for this profile.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=3600
	// +optional
	SyncPeriod *int32 `json:"syncPeriod,omitempty"`

	// dryRun overrides defaults.dryRun for this profile.
	// +optional
	DryRun *bool `json:"dryRun,omitempty"`

	// designerSessionPolicy overrides defaults.designerSessionPolicy.
	// +kubebuilder:validation:Enum=proceed;wait;fail
	// +optional
	DesignerSessionPolicy string `json:"designerSessionPolicy,omitempty"`

	// paused overrides defaults.paused for this profile.
	// +optional
	Paused *bool `json:"paused,omitempty"`
}

// SyncMapping defines a single source->destination file mapping.
type SyncMapping struct {
	// source is the repo-relative path to copy from.
	// Supports Go template variables: {{.GatewayName}}, {{.PodName}}, {{.CRName}},
	// {{.Labels.key}}, {{.Vars.key}}, {{.Namespace}}, {{.Ref}}, {{.Commit}}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`

	// destination is the gateway-relative path to copy to.
	// Supports Go template variables: {{.GatewayName}}, {{.PodName}}, {{.CRName}},
	// {{.Labels.key}}, {{.Vars.key}}, {{.Namespace}}, {{.Ref}}, {{.Commit}}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Destination string `json:"destination"`

	// type is optional and inferred automatically from the repository at sync time.
	// Explicit values ("dir" or "file") are validated against the actual entry type.
	// +kubebuilder:validation:Enum=dir;file
	// +optional
	Type string `json:"type,omitempty"`

	// required causes the sync to fail if the source path does not exist
	// in the repo at the resolved commit.
	// +optional
	Required bool `json:"required,omitempty"`

	// template enables Go template rendering of file contents during staging.
	// When true, the agent resolves {{.GatewayName}}, {{.PodName}}, {{.Vars.key}},
	// and other TemplateContext fields inside each synced file before writing to disk.
	// Binary files (containing null bytes) are rejected with an error.
	// +optional
	Template bool `json:"template,omitempty"`

	// patches applies surgical JSON field updates to files within this mapping after staging.
	// Only valid for JSON files. Each patch targets a specific file (or glob pattern) and
	// sets one or more fields using sjson-style dot-notation paths.
	// +optional
	Patches []MappingPatch `json:"patches,omitempty"`
}

// MappingPatch applies sjson-style field updates to a JSON file within a mapping.
type MappingPatch struct {
	// file is the path to the JSON file to patch, relative to the mapping's destination.
	// Supports glob patterns (e.g. "*.json", "connections/*.json") for directory mappings.
	// For file mappings, file may be omitted — the mapped file itself is patched.
	// +optional
	File string `json:"file,omitempty"`

	// set is a map of sjson dot-notation paths to template values.
	// Nested fields use dots: "SystemName", "networkInterfaces.0.address".
	// Values support Go template syntax: {{.GatewayName}}, {{.Vars.key}}, etc.
	// Values are type-inferred: JSON literals (true, false, numbers) are set as their
	// native types; everything else is set as a string.
	// +kubebuilder:validation:MinProperties=1
	Set map[string]string `json:"set"`
}

// ============================================================
// Top-Level Spec
// ============================================================

// GatewaySyncSpec defines the desired state of GatewaySync.
type GatewaySyncSpec struct {
	// git configures the source repository.
	// +kubebuilder:validation:Required
	Git GitSpec `json:"git"`

	// polling configures the fallback git polling interval.
	// +optional
	Polling PollingSpec `json:"polling,omitempty"`

	// gateway configures how the operator connects to Ignition gateways.
	Gateway GatewaySpec `json:"gateway"`

	// sync configures file sync behavior and profiles.
	// +kubebuilder:validation:Required
	Sync SyncSpec `json:"sync"`

	// agent configures the sync agent sidecar injected by the mutating webhook.
	// +optional
	Agent AgentSpec `json:"agent,omitempty"`

	// paused halts all sync operations when set to true.
	// +optional
	Paused bool `json:"paused,omitempty"`
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

	// serviceAccountName is the ServiceAccount used by the gateway pod.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// profile is the name of the sync profile used by this gateway.
	// +optional
	Profile string `json:"profile,omitempty"`

	// syncStatus is the current sync state of this gateway.
	// +kubebuilder:validation:Enum=Pending;Synced;Error;MissingSidecar
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
}

// GatewaySyncStatus defines the observed state of GatewaySync.
type GatewaySyncStatus struct {
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

	// lastSyncCommitShort is the abbreviated (7-char) git commit SHA for display.
	// +optional
	LastSyncCommitShort string `json:"lastSyncCommitShort,omitempty"`

	// refResolutionStatus indicates the state of git ref resolution.
	// +kubebuilder:validation:Enum=NotResolved;Resolving;Resolved;Error
	// +optional
	RefResolutionStatus string `json:"refResolutionStatus,omitempty"`

	// profileCount is the number of profiles defined in spec.sync.profiles.
	// +optional
	ProfileCount int32 `json:"profileCount,omitempty"`

	// discoveredGateways lists all gateways discovered by the controller.
	// +optional
	DiscoveredGateways []DiscoveredGateway `json:"discoveredGateways,omitempty"`

	// conditions represent the current state of the GatewaySync resource.
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
// +kubebuilder:resource:shortName=gs
// +kubebuilder:printcolumn:name="Ref",type="string",JSONPath=`.spec.git.ref`
// +kubebuilder:printcolumn:name="Commit",type="string",JSONPath=`.status.lastSyncCommitShort`
// +kubebuilder:printcolumn:name="Profiles",type="integer",JSONPath=`.status.profileCount`
// +kubebuilder:printcolumn:name="Synced",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].status`
// +kubebuilder:printcolumn:name="Gateways",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].message`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Last Sync",type="date",JSONPath=`.status.lastSyncTime`,priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`

// GatewaySync is the Schema for the gatewaysyncs API.
type GatewaySync struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GatewaySync.
	// +required
	Spec GatewaySyncSpec `json:"spec"`

	// status defines the observed state of GatewaySync.
	// +optional
	Status GatewaySyncStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GatewaySyncList contains a list of GatewaySync.
type GatewaySyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GatewaySync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GatewaySync{}, &GatewaySyncList{})
}
