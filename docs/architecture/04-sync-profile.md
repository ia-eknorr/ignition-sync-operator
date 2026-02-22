<!-- Part of: Stoker Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 06-stoker-agent.md, 08-deployment-operations.md, 09-security-testing-roadmap.md, 10-enterprise-examples.md -->

# Stoker — SyncProfile CRD

## Motivation

The v1alpha1 `Stoker` CRD bundles Ignition-specific file mapping opinions directly into the infrastructure spec:

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
3. **Mixed concerns** — The Stoker CR mixes infrastructure (git, webhook) with file routing (what goes where on each gateway).
4. **Flat annotation model** — Per-gateway overrides via 6+ annotations per pod are verbose and error-prone.

## Design Decision: Git Ref Does NOT Belong in SyncProfile

A natural question is whether `SyncProfile` should include a `ref` field to allow per-profile (and therefore per-gateway-role) version pinning. After architectural review, **the ref stays exclusively in `Stoker.spec.git.ref`**. SyncProfile contains no git ref field.

### Rationale

1. **Separation of concerns.** The 3-tier model has a clean boundary: Stoker handles infrastructure ("what version, from where, how to connect"), SyncProfile handles file routing ("what files go where on the gateway"). The ref answers "which version of the whole repository" — that is infrastructure, not routing. Adding ref to SyncProfile collapses two tiers into an incoherent hybrid.

2. **SCADA safety.** Mixed config versions within a single Ignition site create silent failures that produce no alarms:
   - **Tag inheritance breaks** — parent UDT definitions at v2.0 and child at v2.1 cause tag quality to degrade to "bad" on mismatched members. No alarm, no error — just missing values flowing upward through tag inheritance.
   - **Project inheritance causes runtime errors** — child gateway expecting script functions from v2.1 that parent gateway's v2.0 project does not define.
   - **Historian data integrity degrades** — inconsistent tag paths produce split timeseries in the historian, requiring manual SQL cleanup.
   - **Regulatory compliance** (FDA 21 CFR Part 11, IEC 62443, NERC CIP) requires a single documented version baseline per site.

3. **Precedence ambiguity.** If both the CR and the profile define a ref, every answer to "which wins?" is bad. The CR's ref becomes decorative (misleading), or operators must cross-reference two resources to determine actual state. The current model — one ref per CR, visible in `kubectl get stk` — is unambiguous.

4. **Controller complexity.** Currently: 1 `ls-remote` per CR, 1 metadata ConfigMap, 1 `RefResolved` condition. With per-profile refs: N+1 resolutions, N+1 ConfigMaps, N+1 conditions, fragmented webhook targeting. All for a pattern that real-world SCADA sites rarely need.

### How Multi-Version Scenarios Are Handled

| Scenario | Mechanism |
|----------|-----------|
| Staged rollout within a site (~70% of upgrades) | `spec.deployment.strategy: canary` with `stages[]` — same ref, ordered delivery with health checks |
| Multi-site rollout (~25%) | Separate Stoker CRs per namespace — each site advances independently |
| Dev/test gateway on feature branch (~5%) | Pod annotation `stoker.io/ref-override` — agent-side only, generates `RefSkew` warning (see [08-deployment-operations.md](08-deployment-operations.md#ref-override-escape-valve)) |
| Permanent multi-version within a site (rare) | Separate Stoker CRs — explicit, auditable, independent status |

---

## Design: SyncProfile CRD

`SyncProfile` is a namespace-scoped CRD that defines **ordered source→destination mappings** for a gateway role. It replaces `shared`, `additionalFiles`, `normalize`, and `siteNumber` with a single generic abstraction.

### 3-Tier Configuration Model

```
┌──────────────────────────────────────────────────────────────┐
│  Tier 1: Stoker CR (namespace defaults)                │
│  git, gateway API, webhook, polling, global excludes          │
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

Precedence: annotation > profile > Stoker > defaults
```

**Key benefit:** Pods reference a profile by name instead of carrying 6+ mapping annotations. Two area gateways with identical roles reference the same `SyncProfile`:

```yaml
# Before: 6+ annotations per pod
area1:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "proveit-sync"
      stoker.io/service-path: "services/area"
      stoker.io/tag-provider: "edge"
      stoker.io/system-name-template: "site{{.SiteNumber}}-{{.GatewayName}}"
      stoker.io/deployment-mode: "prd-cloud"

# After: 3 annotations + a shared profile
area1:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "proveit-sync"
      stoker.io/sync-profile: "proveit-area"
```

### CRD Specification

```yaml
apiVersion: stoker.io/v1alpha1
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
      required: true    # fail sync if source doesn't exist in repo
      # type: dir (default)

    - source: "services/site/config/resources/core"
      destination: "config/resources/core"
      required: true

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
  # Exclude Patterns (merged with Stoker global excludes)
  # ============================================================
  excludePatterns:
    - "**/tag-*/MQTT Engine/"
    - "**/tag-*/System/"

  # ============================================================
  # Profile Dependencies
  # ============================================================
  # Declare ordering constraints — this profile's gateways will
  # not sync until the named profile's gateways report Synced.
  # Single-level only (no transitive chains).
  dependsOn:
    - profileName: "proveit-site"
      condition: "Synced"

  # ============================================================
  # Template Variables
  # ============================================================
  # Key-value pairs resolved by the agent at sync time. Used in
  # destination paths and config normalization. Replaces the
  # removed siteNumber and normalize fields.
  vars:
    siteNumber: "1"
    region: "us-east"

  # ============================================================
  # Sync Period
  # ============================================================
  # Agent-side polling interval (seconds). When ConfigMap watch
  # is active, this serves as a safety-net fallback.
  syncPeriod: 30

  # ============================================================
  # Dry Run
  # ============================================================
  # When true, agent syncs to staging directory but does NOT
  # copy to /ignition-data/. Reports diff in status ConfigMap.
  dryRun: false

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
    // status ConfigMap for inspection. Useful for validating profile
    // changes before activating them.
    // +optional
    DryRun bool `json:"dryRun,omitempty"`

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
    // in the repo at the resolved commit. Catches typos and missing
    // directories before they cause silent failures.
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

// SyncProfileStatus defines the observed state of SyncProfile.
type SyncProfileStatus struct {
    // observedGeneration is the most recent generation observed.
    // +optional
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`

    // gatewayCount is the number of gateways referencing this profile.
    // +optional
    GatewayCount int32 `json:"gatewayCount,omitempty"`

    // dryRunDiff summarizes what would change when dryRun is true.
    // Populated by the controller from agent status ConfigMap data.
    // +optional
    DryRunDiff *DryRunDiffSummary `json:"dryRunDiff,omitempty"`

    // conditions represent the current state.
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DryRunDiffSummary reports what a dry-run sync would change.
type DryRunDiffSummary struct {
    // filesAdded is the count of files that would be created.
    FilesAdded int32 `json:"filesAdded,omitempty"`

    // filesModified is the count of files that would be changed.
    FilesModified int32 `json:"filesModified,omitempty"`

    // filesDeleted is the count of files that would be removed.
    FilesDeleted int32 `json:"filesDeleted,omitempty"`

    // lastEvaluated is when the dry-run was last performed.
    // +optional
    LastEvaluated *metav1.Time `json:"lastEvaluated,omitempty"`
}
```

### kubebuilder Markers

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=sp
// NOTE: Mappings printcolumn was removed (JSONPath `.spec.mappings` returns an array, not an integer)
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=`.spec.deploymentMode.name`
// +kubebuilder:printcolumn:name="Gateways",type="integer",JSONPath=`.status.gatewayCount`
// +kubebuilder:printcolumn:name="Accepted",type="string",JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
```

### Status & Conditions

The SyncProfile controller manages the following conditions:

| Condition | Meaning |
|-----------|---------|
| `Accepted` | Profile spec is valid (mappings non-empty, paths valid, no `..` traversal, no circular `dependsOn`) |
| `DependenciesMet` | All profiles listed in `dependsOn` exist and their gateways report `Synced`. Not set if `dependsOn` is empty. |
| `DryRunCompleted` | Set when `dryRun: true` and the agent has completed a staging sync. Message includes diff summary. |

The `gatewayCount` status field is updated by the Stoker controller whenever it reconciles and discovers pods referencing this profile.

**Note:** The `RefSkew` warning condition lives on the **Stoker** CR (not SyncProfile). It is set when any gateway's `syncedRef` differs from the CR's `lastSyncRef`, which happens when a pod uses the `stoker.io/ref-override` annotation.

---

## Agent Sync Algorithm

The sync agent clones the git repo to a local emptyDir volume (`/repo`) and uses the profile's ordered mappings to construct the gateway filesystem. There is no shared PVC between the controller and agent; each agent manages its own clone.

```
1. Read SyncProfile from ConfigMap (injected by webhook)
2. For each mapping in order:
   a. Resolve source path against local repo clone (/repo/{source})
   b. If type=dir: sync directory contents to /ignition-data/{destination}
   c. If type=file: copy single file to /ignition-data/{destination}
   d. Later mappings overlay earlier ones (last-write-wins)
3. If deploymentMode specified:
   a. Overlay source onto /ignition-data/config/resources/core/
   b. Always recomposed — even if overlay unchanged (core changes need re-overlay)
4. Apply exclude patterns (profile excludes + Stoker global excludes)
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

## Template Variables

The `vars` map on `SyncProfileSpec` replaces the removed `siteNumber` and `normalize` fields with a generic, extensible mechanism. Variables are resolved by the **agent** at sync time (not the controller), since the agent has access to pod metadata and the local clone.

### Available Variables

| Source | Template Syntax | Description |
|--------|----------------|-------------|
| Profile `vars` map | `{{.Vars.siteNumber}}` | User-defined key-value pairs from the SyncProfile |
| Built-in: gateway | `{{.GatewayName}}` | Gateway identity (from annotation or pod label) |
| Built-in: namespace | `{{.Namespace}}` | Pod namespace |
| Built-in: git | `{{.Ref}}`, `{{.Commit}}` | Resolved git ref and commit SHA |

### Where Variables Are Resolved

1. **Mapping destination paths** — the primary use case:

```yaml
spec:
  vars:
    siteNumber: "1"
  mappings:
    - source: "shared/scripts"
      destination: "projects/site{{.Vars.siteNumber}}/ignition/script-python/exchange"
```

2. **Config normalization** — the agent applies `vars` as field replacements when walking `config.json` files. This replaces the old `normalize.systemName` and `normalize.fields` mechanism with a single template-driven approach.

3. **Mapping source paths** — less common, but supported for monorepos where site-specific directories follow a naming convention:

```yaml
spec:
  vars:
    siteName: "us-east-1"
  mappings:
    - source: "sites/{{.Vars.siteName}}/projects"
      destination: "projects"
```

### Resolution Order

Variables are resolved in this order:
1. Built-in variables (`GatewayName`, `Namespace`, `Ref`, `Commit`) are populated from pod metadata and the metadata ConfigMap.
2. Profile `vars` are layered on top. A profile var cannot override a built-in — if a profile defines `vars.GatewayName`, it is ignored and the built-in wins.
3. Go `text/template` is used for resolution. Invalid templates cause the `Accepted` condition to be set to `False`.

---

## Profile Dependencies

The `dependsOn` field enforces sync ordering across profiles to respect Ignition's tag provider hierarchy and project inheritance.

### Why This Matters

In Ignition, parent gateways (site) must sync before child gateways (area) because:
- UDT definitions on the parent must be in place before children inherit them
- Shared script libraries in parent projects must exist before children reference them
- Tag provider configurations on the parent determine the shape of inherited tags

### How It Works

```yaml
# Area profile waits for site profile to sync
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-area
spec:
  dependsOn:
    - profileName: "proveit-site"
      condition: "Synced"
  mappings:
    - source: "services/area/projects"
      destination: "projects"
```

1. The controller checks whether all gateways using `proveit-site` have `SyncStatus: Synced` in the status ConfigMap.
2. If yes, the `DependenciesMet` condition on `proveit-area` is set to `True`, and its gateways proceed to sync.
3. If no, the `DependenciesMet` condition is `False` with a message identifying which gateways are still pending.
4. Agents for `proveit-area` gateways wait until `DependenciesMet` is `True` before syncing.

### Constraints

- **Single-level only.** If A depends on B, B cannot depend on C. The controller rejects transitive chains by validating that no profile in `dependsOn` itself has a non-empty `dependsOn`. This keeps resolution deterministic and debuggable.
- **Same namespace.** The dependency profile must exist in the same namespace. Cross-namespace dependencies are not supported.
- **Graceful on deletion.** If the dependency profile is deleted, `DependenciesMet` is set to `False` with reason `DependencyNotFound`. Gateways using this profile pause until the dependency is restored.

### Interaction with DeploymentStrategySpec

`dependsOn` operates at the **profile level** while `DeploymentStrategySpec.stages` operates at the **gateway level**. They are complementary:

- `stages` controls rollout ordering for gateways within a single version upgrade (canary → staging → production).
- `dependsOn` controls sync ordering between gateway roles (site before area), which applies to every sync — not just canary rollouts.

When both are active, `dependsOn` is evaluated first. A gateway must satisfy both its profile dependency AND its deployment stage before syncing.

---

## Dry Run Mode

When `dryRun: true` is set on a SyncProfile, the agent performs a full sync to the staging directory but does **not** copy files to `/ignition-data/` and does **not** trigger the Ignition scan API.

### Workflow

1. Create or update a profile with `dryRun: true`.
2. Agent runs the full sync pipeline (clone, mappings, overlay, excludes, normalization) to the staging directory.
3. Agent computes a diff between staging and the current `/ignition-data/` state.
4. Agent writes the diff summary to the status ConfigMap (files added/modified/deleted).
5. Controller aggregates the diff into `SyncProfileStatus.dryRunDiff` and sets the `DryRunCompleted` condition.
6. Operator inspects the diff: `kubectl get syncprofile proveit-area -n site1 -o json | jq '.status.dryRunDiff'`
7. If satisfied, set `dryRun: false` — the next sync cycle applies the changes for real.

### Use Cases

- **Validating a new profile** before assigning gateways to it.
- **Testing mapping changes** — see which files would move before committing.
- **Pre-upgrade verification** — create a dry-run profile pointing at the same mappings but let operators inspect the diff before flipping the Stoker CR to the new ref.

---

## Safety Guardrails

These behaviors are enforced by the agent regardless of profile or CR configuration. They are not optional.

### Mandatory Agent Behaviors

| Guardrail | Behavior |
|-----------|----------|
| **Path traversal prevention** | Agent rejects any resolved path containing `..` or an absolute path. Sync fails with `PathTraversalBlocked` error. |
| **`.resources/` protection** | The pattern `**/.resources/**` is always excluded from sync, even if omitted from `excludePatterns`. The agent also verifies that the staging directory does not contain `.resources/` before merging. |
| **JSON syntax validation** | Before writing any `config.json` to the gateway, the agent validates JSON syntax. Invalid JSON fails the sync for that file with condition `ConfigSyntaxError`. |
| **Mapping overlap warning** | When two mappings write to the same destination directory, the agent emits a Kubernetes `Warning` event on the Stoker CR. Last-write-wins behavior is documented but the warning helps catch accidental overlaps. |
| **Concurrent sync prevention** | Only one sync cycle runs at a time per agent. If a new trigger arrives while a sync is in progress, it is queued (not dropped). |

### Post-Sync Health Checks

After every non-dry-run sync, the agent verifies:

1. **Gateway responsive** — `GET /data/api/v1/gateway-info` returns 200 within 5 seconds.
2. **Projects loaded** — `GET /data/api/v1/projects/list` returns expected project count (compared against the mappings that target `projects/`).
3. **Tag providers intact** — `GET /data/api/v1/resources/list/ignition/tag-provider` returns expected providers.

If any check fails, the gateway is reported as `SyncStatus: Error` in the status ConfigMap. The Stoker CR's `AllGatewaysSynced` condition reflects this.

### Maximum Version Skew

During a rolling update (via `deployment.stages`), the controller enforces that gateways are never more than 1 commit apart. If a stage fails and a gateway cannot sync to the new commit, the rollout halts — subsequent stages are blocked until the failed gateway is resolved or the operator manually approves continuation.

---

## Planned Features (v1beta1)

These features are designed but deferred to v1beta1. They do not affect the v1alpha1 SyncProfile spec.

### Profile Composition (`includeMappingsFrom`)

Reference another profile's mappings as a base, then add or override:

```yaml
spec:
  includeMappingsFrom:
    - name: proveit-shared-base    # base profile's mappings prepended
  mappings:
    - source: "services/area/projects"    # role-specific mappings appended
      destination: "projects"
```

Single-level only (no chained includes). The base profile's mappings are prepended before this profile's mappings, maintaining the last-write-wins overlay semantics.

### Conditional Mappings

Apply a mapping only when the gateway pod has specific labels:

```yaml
spec:
  mappings:
    - source: "services/area/mqtt-config"
      destination: "config/resources/core/ignition/mqtt-engine"
      condition:
        podLabel:
          stoker.io/has-mqtt: "true"
```

Reduces the need for separate profiles when gateways differ by a single mapping.

### Mapping Constraints

Additional validation fields on `SyncMapping`:

```go
// maxSize fails the sync if the source exceeds this size (e.g., "50Mi").
MaxSize string `json:"maxSize,omitempty"`

// filePattern restricts sync to files matching this glob within the source directory.
FilePattern string `json:"filePattern,omitempty"`
```

### Resource Limits

Top-level safety guard against repository bloat:

```yaml
spec:
  limits:
    maxFiles: 10000
    maxTotalSize: "500Mi"
```

### Immutable Profiles

An annotation `stoker.io/immutable: "true"` prevents spec edits after creation. Operators create a new profile version (e.g., `proveit-area-v4`) and update pod annotations. Supports regulated environments where change control requires explicit versioned artifacts.

### Maintenance Windows

A field on the **Stoker CR** (not SyncProfile) that restricts when syncs can occur:

```yaml
spec:
  maintenanceWindow:
    schedule: "0 6 * * 6"    # Saturdays at 06:00
    duration: "4h"
    enforced: true            # Refuse to sync outside window
```

When `enforced: true` and the current time is outside the window, the controller skips sync and sets a `MaintenanceWindowBlocked` condition. Webhooks are still accepted (the annotation is stored) but the sync is deferred.

---

## Stoker Simplification

With SyncProfile absorbing file routing, the Stoker CR becomes pure infrastructure:

### Fields Removed from StokerSpec

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

### What Stays in Stoker

```go
type StokerSpec struct {
    // Stable — infrastructure concerns
    Git             GitSpec             `json:"git"`
    // Deprecated: Storage is no longer used. Agent clones to local emptyDir.
    // Retained at v1alpha1 for backward compatibility; will be removed in v1beta1.
    // +optional
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

1. **Pods without `sync-profile` annotation** still work in 2-tier mode (Stoker + annotations). This is backward-compatible for existing deployments that haven't adopted SyncProfile yet.
2. **All existing pod annotations remain valid** — `service-path`, `tag-provider`, `deployment-mode`, `system-name-template` continue to function.
3. **Profile deletion** triggers graceful degradation — gateways fall back to annotation-based config. The agent logs a warning and continues with whatever annotations are present.
4. **Migration path:** During the transition, users can run without SyncProfile (2-tier) and adopt it incrementally per gateway role.

### Deprecation Timeline

| Phase | Action |
|-------|--------|
| v1alpha1 (current) | Remove `shared`, `additionalFiles`, `normalize`, `siteNumber` from StokerSpec. Add SyncProfile CRD. |
| v1alpha1 (transition) | Pods without `sync-profile` annotation use `service-path` + other annotations (2-tier mode). |
| v1beta1 | SyncProfile is the recommended approach. `service-path` annotation still works but docs guide toward profiles. |
| v1 | Full 3-tier model is the standard. |

---

## Annotations (Updated)

With SyncProfile, the per-pod annotation set simplifies:

| Annotation | Required | Description |
|---|---|---|
| `stoker.io/inject` | Yes | `"true"` to enable sidecar injection |
| `stoker.io/cr-name` | No* | Name of the `Stoker` CR. *Auto-derived if exactly one CR exists. |
| `stoker.io/sync-profile` | No | Name of the `SyncProfile` to use. If omitted, falls back to `service-path` annotation. |
| `stoker.io/gateway-name` | No | Override gateway identity (defaults to pod label `app.kubernetes.io/name`) |
| `stoker.io/ref-override` | No | Override the git ref for this pod only. Read by the agent, not the controller. Generates a `RefSkew` warning condition on the Stoker CR. See [08-deployment-operations.md](08-deployment-operations.md#ref-override-escape-valve). |

Annotations that become **unnecessary when using SyncProfile** (but still work for 2-tier mode):

| Annotation | Replaced By |
|---|---|
| `stoker.io/service-path` | `SyncProfile.spec.mappings` |
| `stoker.io/deployment-mode` | `SyncProfile.spec.deploymentMode` |
| `stoker.io/tag-provider` | `SyncProfile.spec.mappings` (explicit UDT destination) |
| `stoker.io/sync-period` | `SyncProfile.spec.syncPeriod` |
| `stoker.io/exclude-patterns` | `SyncProfile.spec.excludePatterns` |
| `stoker.io/system-name` | Future: pre/post-sync hooks |
| `stoker.io/system-name-template` | Future: pre/post-sync hooks |

---

## Worked Examples

### ProveIt 2026 — Site Gateway

```yaml
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-site
  namespace: site1
spec:
  vars:
    siteNumber: "1"
    projectName: "proveit2026"
  mappings:
    - source: "services/site/projects"
      destination: "projects"
      required: true
    - source: "services/site/config/resources/core"
      destination: "config/resources/core"
      required: true
    - source: "shared/external-resources"
      destination: "config/resources/external"
    - source: "shared/scripts"
      destination: "projects/site/ignition/script-python/exchange/{{.Vars.projectName}}"
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
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: proveit-area
  namespace: site1
spec:
  dependsOn:
    - profileName: "proveit-site"
      condition: "Synced"
  mappings:
    - source: "services/area/projects"
      destination: "projects"
      required: true
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
      required: true
  excludePatterns:
    - "**/tag-*/System/"
```

All four area gateways reference the same profile:

```yaml
area1:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "proveit-sync"
      stoker.io/sync-profile: "proveit-area"

# area2, area3, area4 — identical annotations
```

### Simple Single Gateway (no profile needed)

For a single gateway where the repo root IS the gateway data, SyncProfile is optional:

```yaml
ignition:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "my-sync"
      stoker.io/service-path: "."   # 2-tier mode, no profile
```

Or with a minimal profile:

```yaml
apiVersion: stoker.io/v1alpha1
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
3. **No reconciliation loop** — SyncProfile is a passive config object. The Stoker controller and agents read it; the SyncProfile controller only validates.
4. **Watch for changes** — When a SyncProfile is updated, the Stoker controller is notified to re-reconcile affected gateways (via watch with `EnqueueRequestsFromMapFunc`).

---

## Related Documents

- [01-crd.md](01-crd.md) — Stoker CRD (simplified after SyncProfile extraction)
- [02-controller.md](02-controller.md) — Controller Manager reconciliation loop
- [06-stoker-agent.md](06-stoker-agent.md) — Sync agent uses SyncProfile for file mapping
- [10-enterprise-examples.md](10-enterprise-examples.md) — Enterprise examples updated for SyncProfile
