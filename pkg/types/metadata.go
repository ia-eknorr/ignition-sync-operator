package types

// ResolvedProfile is a fully-resolved profile with defaults applied,
// serialized as JSON into the metadata ConfigMap's "profiles" key.
type ResolvedProfile struct {
	Mappings              []ResolvedMapping `json:"mappings"`
	ExcludePatterns       []string          `json:"excludePatterns,omitempty"`
	Vars                  map[string]string `json:"vars,omitempty"`
	SyncPeriod            int32             `json:"syncPeriod"`
	DryRun                bool              `json:"dryRun"`
	DesignerSessionPolicy string            `json:"designerSessionPolicy"`
	Paused                bool              `json:"paused"`
}

// ResolvedMapping is a source->destination mapping in a resolved profile.
type ResolvedMapping struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
}
