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
type AgentImageSpec struct {
	// repository is the container image repository.
	// +kubebuilder:default="ghcr.io/inductiveautomation/ignition-sync-agent"
	// +optional
	Repository string `json:"repository,omitempty"`

	// tag is the container image tag.
	// +kubebuilder:default="latest"
	// +optional
	Tag string `json:"tag,omitempty"`

	// pullPolicy is the image pull policy.
	// +kubebuilder:default="IfNotPresent"
	// +optional
	PullPolicy string `json:"pullPolicy,omitempty"`
}

// ============================================================
// Top-Level Spec
// ============================================================

// IgnitionSyncSpec defines the desired state of IgnitionSync.
type IgnitionSyncSpec struct {
	// git configures the source repository.
	// +kubebuilder:validation:Required
	Git GitSpec `json:"git"`

	// polling configures the fallback git polling interval.
	// +optional
	Polling PollingSpec `json:"polling,omitempty"`

	// gateway configures how the operator connects to Ignition gateways.
	Gateway GatewaySpec `json:"gateway"`

	// excludePatterns are glob patterns for files to exclude from sync.
	// The pattern "**/.resources/**" is always enforced by the agent even if omitted.
	// +kubebuilder:default={"**/.git/","**/.gitkeep","**/.resources/**"}
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

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

	// syncProfile is the name of the SyncProfile referenced by this gateway.
	// +optional
	SyncProfile string `json:"syncProfile,omitempty"`

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
