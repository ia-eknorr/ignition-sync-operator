package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================
// SyncProfile Spec Types
// ============================================================

// SyncProfileSpec defines the desired state of SyncProfile.
type SyncProfileSpec struct {
	// mappings is an ordered list of source→destination file mappings.
	// Applied top to bottom; later mappings overlay earlier ones.
	// +kubebuilder:validation:MinItems=1
	Mappings []SyncMapping `json:"mappings"`

	// deploymentMode configures an Ignition deployment mode overlay.
	// Applied after all mappings onto config/resources/core/.
	// +optional
	DeploymentMode *DeploymentModeSpec `json:"deploymentMode,omitempty"`

	// excludePatterns are glob patterns for files to exclude.
	// Merged with Stoker global excludePatterns (additive).
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// dependsOn declares profile dependencies for sync ordering.
	// This profile's gateways will not sync until the named profile's
	// gateways all report the specified condition. Single-level only —
	// no transitive dependency chains.
	// +optional
	DependsOn []ProfileDependency `json:"dependsOn,omitempty"`

	// vars is a map of template variables resolved by the agent at sync
	// time. Available in destination paths and config normalization as
	// {{.Vars.key}}. Replaces the removed siteNumber and normalize fields.
	// +optional
	Vars map[string]string `json:"vars,omitempty"`

	// syncPeriod is the agent-side polling interval in seconds.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=3600
	// +optional
	SyncPeriod int32 `json:"syncPeriod,omitempty"`

	// dryRun causes the agent to sync to a staging directory without
	// copying to /ignition-data/. The diff report is written to the
	// status ConfigMap for inspection.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// designerSessionPolicy controls sync behavior when Ignition Designer
	// sessions are active. "proceed" (default) logs a warning and continues,
	// "wait" retries until sessions close (up to 5 min), "fail" aborts the sync.
	// +kubebuilder:default="proceed"
	// +kubebuilder:validation:Enum=proceed;wait;fail
	// +optional
	DesignerSessionPolicy string `json:"designerSessionPolicy,omitempty"`

	// paused halts sync for all gateways referencing this profile.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// SyncMapping defines a single source→destination file mapping.
type SyncMapping struct {
	// source is the repo-relative path to copy from.
	// Supports Go template variables: {{.Vars.key}}, {{.GatewayName}}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`

	// destination is the gateway-relative path to copy to.
	// Supports Go template variables: {{.Vars.key}}, {{.GatewayName}}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Destination string `json:"destination"`

	// type is the entry type — "dir" (default) or "file".
	// +kubebuilder:default="dir"
	// +kubebuilder:validation:Enum=dir;file
	// +optional
	Type string `json:"type,omitempty"`

	// required causes the sync to fail if the source path does not exist
	// in the repo at the resolved commit.
	// +optional
	Required bool `json:"required,omitempty"`
}

// ProfileDependency declares a dependency on another SyncProfile.
type ProfileDependency struct {
	// profileName is the name of the SyncProfile this profile depends on.
	// Must exist in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ProfileName string `json:"profileName"`

	// condition is the status condition that must be true on all gateways
	// using the dependency profile before this profile's gateways sync.
	// +kubebuilder:default="Synced"
	// +kubebuilder:validation:Enum=Synced
	// +optional
	Condition string `json:"condition,omitempty"`
}

// DeploymentModeSpec configures an Ignition deployment mode overlay.
type DeploymentModeSpec struct {
	// name is the mode name (informational, shown in status).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// source is the repo-relative overlay directory.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`
}

// ============================================================
// SyncProfile Status Types
// ============================================================

// SyncProfileStatus defines the observed state of SyncProfile.
type SyncProfileStatus struct {
	// observedGeneration is the most recent generation observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// gatewayCount is the number of gateways referencing this profile.
	// +optional
	GatewayCount int32 `json:"gatewayCount,omitempty"`

	// conditions represent the current state.
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
// +kubebuilder:resource:shortName=sp
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=`.spec.deploymentMode.name`
// +kubebuilder:printcolumn:name="Gateways",type="integer",JSONPath=`.status.gatewayCount`
// +kubebuilder:printcolumn:name="Accepted",type="string",JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`

// SyncProfile is the Schema for the syncprofiles API.
type SyncProfile struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of SyncProfile.
	// +required
	Spec SyncProfileSpec `json:"spec"`

	// status defines the observed state of SyncProfile.
	// +optional
	Status SyncProfileStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SyncProfileList contains a list of SyncProfile.
type SyncProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SyncProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SyncProfile{}, &SyncProfileList{})
}
