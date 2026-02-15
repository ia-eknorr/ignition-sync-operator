<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 03-sync-agent.md, 04-deployment-operations.md, 05-enterprise-examples.md, 06-security-testing-roadmap.md -->

# Ignition Sync Operator — SyncProfile CRD

## Motivation

The v1alpha1 `IgnitionSync` CRD bundles Ignition-specific file mapping opinions directly into the infrastructure spec:

```yaml
# Current: opinionated, Ignition-specific fields
spec:
  siteNumber: "1"
  shared:
    externalResources:
      enabled: true
      source: "shared/ignition-gateway/config/resources/external"
    scripts:
      enabled: true
      source: "shared/scripts"
      destPath: "ignition/script-python/exchange/proveit2026"
    udts:
      enabled: true
      source: "shared/udts"
  additionalFiles:
    - source: "shared/config/factory-config.json"
      dest: "factory-config.json"
      type: file
  normalize:
    systemName: true
```

Problems:

1. **Tightly coupled** — `shared.scripts`, `shared.udts`, `shared.externalResources` assume a specific Ignition project structure. Non-standard layouts require `additionalFiles` workarounds.
2. **Not reusable** — Two gateways with the same role (e.g., all area gateways) duplicate the same mapping configuration in annotations.
3. **Mixed concerns** — The IgnitionSync CR mixes infrastructure (git, storage, webhook) with file routing (what goes where on each gateway).
4. **Flat annotation model** — Per-gateway overrides via 6+ annotations per pod are verbose and error-prone.

## Design: SyncProfile CRD

`SyncProfile` is a namespace-scoped CRD that defines **ordered source→destination mappings** for a gateway role. It replaces `shared`, `additionalFiles`, `normalize`, and `siteNumber` with a single generic abstraction.

### 3-Tier Configuration Model

```
┌──────────────────────────────────────────────────────────────┐
│  Tier 1: IgnitionSync CR (namespace defaults)                │
│  git, storage, gateway API, webhook, polling, global excludes│
│  agent image — pure infrastructure                           │
├──────────────────────────────────────────────────────────────┤
│  Tier 2: SyncProfile (reusable gateway role config)          │
│  ordered mappings, deployment mode overlay, exclude patterns │
│  sync period — what files go where                           │
├──────────────────────────────────────────────────────────────┤
│  Tier 3: Pod Annotations (instance overrides)                │
│  inject, cr-name, sync-profile, gateway-name                 │
│  highest priority — per-pod tweaks                           │
└──────────────────────────────────────────────────────────────┘

Precedence: annotation > profile > IgnitionSync > defaults
```

**Key benefit:** Pods reference a profile by name instead of carrying 6+ mapping annotations. Two area gateways with identical roles reference the same `SyncProfile`:

```yaml
# Before: 6+ annotations per pod
area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/area"
      ignition-sync.io/tag-provider: "edge"
      ignition-sync.io/system-name-template: "site{{.SiteNumber}}-{{.GatewayName}}"
      ignition-sync.io/deployment-mode: "prd-cloud"

# After: 3 annotations + a shared profile
area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/sync-profile: "proveit-area"
```

### CRD Specification

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-site
  namespace: site1
spec:
  # ============================================================
  # Ordered Mappings — applied top to bottom
  # ============================================================
  # Later mappings overlay earlier ones when destinations overlap.
  # Each mapping copies a repo-relative source path to a gateway-
  # relative destination path. No implicit behavior — everything
  # is explicit.
  mappings:
    - source: "services/site/projects"
      destination: "projects"
      # type: dir (default)

    - source: "services/site/config/resources/core"
      destination: "config/resources/core"

    - source: "shared/external-resources"
      destination: "config/resources/external"

    - source: "shared/scripts"
      destination: "projects/site/ignition/script-python/exchange/proveit2026"

    - source: "shared/udts"
      destination: "config/tags/default"

    - source: "shared/factory-config.json"
      destination: "factory-config.json"
      type: file    # single file copy, not directory

  # ============================================================
  # Deployment Mode Overlay
  # ============================================================
  # First-class field (not just another mapping) because it has
  # special overlay semantics: applied AFTER all mappings onto
  # config/resources/core/ — always recomposed even if the
  # overlay source hasn't changed (core changes still need
  # overlay re-applied).
  deploymentMode:
    name: "prd-cloud"
    source: "services/site/overlays/prd-cloud"

  # ============================================================
  # Exclude Patterns (merged with IgnitionSync global excludes)
  # ============================================================
  excludePatterns:
    - "**/tag-*/MQTT Engine/"
    - "**/tag-*/System/"

  # ============================================================
  # Sync Period
  # ============================================================
  # Agent-side polling interval (seconds). When ConfigMap watch
  # is active, this serves as a safety-net fallback.
  syncPeriod: 30

  # ============================================================
  # Pause
  # ============================================================
  # Halt sync for all gateways referencing this profile.
  paused: false
```

### Go Type Definitions

```go
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
    // Merged with IgnitionSync global excludePatterns (additive).
    // +optional
    ExcludePatterns []string `json:"excludePatterns,omitempty"`

    // syncPeriod is the agent-side polling interval in seconds.
    // +kubebuilder:default=30
    // +kubebuilder:validation:Minimum=5
    // +kubebuilder:validation:Maximum=3600
    // +optional
    SyncPeriod int32 `json:"syncPeriod,omitempty"`

    // paused halts sync for all gateways referencing this profile.
    // +optional
    Paused bool `json:"paused,omitempty"`
}

// SyncMapping defines a single source→destination file mapping.
type SyncMapping struct {
    // source is the repo-relative path to copy from.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Source string `json:"source"`

    // destination is the gateway-relative path to copy to.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Destination string `json:"destination"`

    // type is the entry type — "dir" (default) or "file".
    // +kubebuilder:default="dir"
    // +kubebuilder:validation:Enum=dir;file
    // +optional
    Type string `json:"type,omitempty"`
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
```

### kubebuilder Markers

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=sp
// +kubebuilder:printcolumn:name="Mappings",type="integer",JSONPath=`.spec.mappings`,description="Number of source→dest mappings"
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=`.spec.deploymentMode.name`
// +kubebuilder:printcolumn:name="Gateways",type="integer",JSONPath=`.status.gatewayCount`
// +kubebuilder:printcolumn:name="Accepted",type="string",JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
```

### Status & Conditions

The SyncProfile controller manages a single condition:

| Condition | Meaning |
|-----------|---------|
| `Accepted` | Profile spec is valid (mappings non-empty, paths valid, no `..` traversal) |

The `gatewayCount` status field is updated by the IgnitionSync controller whenever it reconciles and discovers pods referencing this profile.

---

## Agent Sync Algorithm

The sync agent uses the profile's ordered mappings to construct the gateway filesystem:

```
1. Read SyncProfile from ConfigMap (injected by webhook)
2. For each mapping in order:
   a. Resolve source path against repo clone (/repo/{source})
   b. If type=dir: rsync directory contents to /ignition-data/{destination}
   c. If type=file: copy single file to /ignition-data/{destination}
   d. Later mappings overlay earlier ones (last-write-wins)
3. If deploymentMode specified:
   a. Overlay source onto /ignition-data/config/resources/core/
   b. Always recomposed — even if overlay unchanged (core changes need re-overlay)
4. Apply exclude patterns (profile excludes + IgnitionSync global excludes)
5. Walk destination and delete orphans except protected dirs (.resources/)
6. Verify no .resources/ in staging (safety check)
```

**Two-pass orphan cleanup:**
1. Walk source, copy changed files to destination
2. Walk destination, delete files NOT present in source AND NOT in protected list

Protected paths (hardcoded, not configurable):
- `.resources/**` — Ignition runtime caches
- `.sync-staging/**` — agent working directory

---

## IgnitionSync Simplification

With SyncProfile absorbing file routing, the IgnitionSync CR becomes pure infrastructure:

### Fields Removed from IgnitionSyncSpec

| Field | Replacement |
|-------|-------------|
| `spec.shared` | Profile `mappings` |
| `spec.additionalFiles` | Profile `mappings` |
| `spec.normalize` | Future: pre/post-sync hooks |
| `spec.siteNumber` | Future: template variables |

### Types Removed

- `SharedSpec`, `ExternalResourcesSpec`, `ScriptsSpec`, `UDTsSpec`
- `AdditionalFile`
- `NormalizeSpec`, `FieldReplacement`

### What Stays in IgnitionSync

```go
type IgnitionSyncSpec struct {
    // Stable — infrastructure concerns
    Git             GitSpec             `json:"git"`
    Storage         StorageSpec         `json:"storage,omitempty"`
    Webhook         WebhookSpec         `json:"webhook,omitempty"`
    Polling         PollingSpec         `json:"polling,omitempty"`
    Gateway         GatewaySpec         `json:"gateway"`
    ExcludePatterns []string            `json:"excludePatterns,omitempty"`
    Paused          bool                `json:"paused,omitempty"`
    Ignition        IgnitionSpec        `json:"ignition,omitempty"`
    Agent           AgentSpec           `json:"agent,omitempty"`

    // Experimental
    Bidirectional   *BidirectionalSpec  `json:"bidirectional,omitempty"`
    Validation      ValidationSpec      `json:"validation,omitempty"`
    Snapshots       *SnapshotSpec       `json:"snapshots,omitempty"`
    Deployment      *DeploymentStrategySpec `json:"deployment,omitempty"`
}
```

---

## Backward Compatibility

**This is a breaking CRD change**, acceptable at v1alpha1:

1. **Pods without `sync-profile` annotation** still work in 2-tier mode (IgnitionSync + annotations). This is backward-compatible for existing deployments that haven't adopted SyncProfile yet.
2. **All existing pod annotations remain valid** — `service-path`, `tag-provider`, `deployment-mode`, `system-name-template` continue to function.
3. **Profile deletion** triggers graceful degradation — gateways fall back to annotation-based config. The agent logs a warning and continues with whatever annotations are present.
4. **Migration path:** During the transition, users can run without SyncProfile (2-tier) and adopt it incrementally per gateway role.

### Deprecation Timeline

| Phase | Action |
|-------|--------|
| v1alpha1 (current) | Remove `shared`, `additionalFiles`, `normalize`, `siteNumber` from IgnitionSyncSpec. Add SyncProfile CRD. |
| v1alpha1 (transition) | Pods without `sync-profile` annotation use `service-path` + other annotations (2-tier mode). |
| v1beta1 | SyncProfile is the recommended approach. `service-path` annotation still works but docs guide toward profiles. |
| v1 | Full 3-tier model is the standard. |

---

## Annotations (Updated)

With SyncProfile, the per-pod annotation set simplifies:

| Annotation | Required | Description |
|---|---|---|
| `ignition-sync.io/inject` | Yes | `"true"` to enable sidecar injection |
| `ignition-sync.io/cr-name` | No* | Name of the `IgnitionSync` CR. *Auto-derived if exactly one CR exists. |
| `ignition-sync.io/sync-profile` | No | Name of the `SyncProfile` to use. If omitted, falls back to `service-path` annotation. |
| `ignition-sync.io/gateway-name` | No | Override gateway identity (defaults to pod label `app.kubernetes.io/name`) |

Annotations that become **unnecessary when using SyncProfile** (but still work for 2-tier mode):

| Annotation | Replaced By |
|---|---|
| `ignition-sync.io/service-path` | `SyncProfile.spec.mappings` |
| `ignition-sync.io/deployment-mode` | `SyncProfile.spec.deploymentMode` |
| `ignition-sync.io/tag-provider` | `SyncProfile.spec.mappings` (explicit UDT destination) |
| `ignition-sync.io/sync-period` | `SyncProfile.spec.syncPeriod` |
| `ignition-sync.io/exclude-patterns` | `SyncProfile.spec.excludePatterns` |
| `ignition-sync.io/system-name` | Future: pre/post-sync hooks |
| `ignition-sync.io/system-name-template` | Future: pre/post-sync hooks |

---

## Worked Examples

### ProveIt 2026 — Site Gateway

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-site
  namespace: site1
spec:
  mappings:
    - source: "services/site/projects"
      destination: "projects"
    - source: "services/site/config/resources/core"
      destination: "config/resources/core"
    - source: "shared/external-resources"
      destination: "config/resources/external"
    - source: "shared/scripts"
      destination: "projects/site/ignition/script-python/exchange/proveit2026"
    - source: "shared/udts"
      destination: "config/tags/default"
    - source: "shared/factory-config.json"
      destination: "factory-config.json"
      type: file
  deploymentMode:
    name: "prd-cloud"
    source: "services/site/overlays/prd-cloud"
  excludePatterns:
    - "**/tag-*/MQTT Engine/"
    - "**/tag-*/System/"
```

### ProveIt 2026 — Area Gateway (shared by area1, area2, area3, area4)

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-area
  namespace: site1
spec:
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
  excludePatterns:
    - "**/tag-*/System/"
```

All four area gateways reference the same profile:

```yaml
area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/sync-profile: "proveit-area"

# area2, area3, area4 — identical annotations
```

### Simple Single Gateway (no profile needed)

For a single gateway where the repo root IS the gateway data, SyncProfile is optional:

```yaml
ignition:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "my-sync"
      ignition-sync.io/service-path: "."   # 2-tier mode, no profile
```

Or with a minimal profile:

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: single-gateway
  namespace: default
spec:
  mappings:
    - source: "."
      destination: "."
```

---

## SyncProfile Controller

The SyncProfile controller is lightweight:

1. **Validate** on create/update — check mappings non-empty, paths valid (no `..` traversal, no absolute paths), source and destination are non-empty.
2. **Set `Accepted` condition** — `True` if validation passes, `False` with reason if it fails.
3. **No reconciliation loop** — SyncProfile is a passive config object. The IgnitionSync controller and agents read it; the SyncProfile controller only validates.
4. **Watch for changes** — When a SyncProfile is updated, the IgnitionSync controller is notified to re-reconcile affected gateways (via watch with `EnqueueRequestsFromMapFunc`).

---

## Related Documents

- [01-crd.md](01-crd.md) — IgnitionSync CRD (simplified after SyncProfile extraction)
- [02-controller.md](02-controller.md) — Controller Manager reconciliation loop
- [03-sync-agent.md](03-sync-agent.md) — Sync agent uses SyncProfile for file mapping
- [05-enterprise-examples.md](05-enterprise-examples.md) — Enterprise examples updated for SyncProfile
