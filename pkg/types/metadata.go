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
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Type        string          `json:"type,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Template    bool            `json:"template,omitempty"`
	Patches     []ResolvedPatch `json:"patches,omitempty"`
}

// ResolvedPatch carries a single patch spec from the CR into the agent.
// Set values may contain Go template syntax; the agent resolves them at sync time.
type ResolvedPatch struct {
	// File is relative to the mapping's destination. Supports doublestar globs.
	// Empty means "the mapped file itself" (only valid for file mappings).
	File string            `json:"file,omitempty"`
	Set  map[string]string `json:"set"`
}
