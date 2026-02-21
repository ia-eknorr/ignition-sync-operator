<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 02-controller.md, 03-sync-agent.md, 04-deployment-operations.md, 05-enterprise-examples.md, 06-security-testing-roadmap.md, 07-sync-profile.md -->

# Ignition Sync Operator — Custom Resource Definition

## Custom Resource Definition

The CRD is **namespace-scoped** — each namespace that runs Ignition gateways creates its own `IgnitionSync` CR. The cluster-scoped controller watches all namespaces.

**Design Principle: Sensible Defaults** — The CRD uses kubebuilder default markers extensively so a minimal CR needs only `spec.git` and `spec.gateway.apiKeySecretRef`. Everything else has production-ready defaults. This reduces a typical CR from ~60 lines to ~24 lines.

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: proveit-sync
  namespace: site1
spec:
  # ============================================================
  # Git Repository
  # ============================================================
  git:
    repo: "git@github.com:inductive-automation/conf-proveit26-app.git"
    ref: "2.0.0"       # Tag, branch, or commit SHA — managed by Kargo or webhook
    auth:
      # Option A: SSH deploy key
      sshKey:
        secretRef:
          name: "git-sync-secret"
          key: "ssh-privatekey"
      # Option B: GitHub App (enables bi-directional PR creation)
      # githubApp:
      #   appId: 2716741
      #   installationId: 12345678
      #   privateKeySecretRef:
      #     name: "github-app-key"
      #     key: "private-key.pem"
      # Option C: Token-based (generic git hosts)
      # token:
      #   secretRef:
      #     name: "git-token"
      #     key: "token"

  # ============================================================
  # Storage — DEPRECATED (agent clones locally, no shared PVC)
  # ============================================================
  # The storage field is deprecated and ignored. Each sync agent
  # clones the repository to a local emptyDir. No shared PVC is needed.
  # This field will be removed in v1beta1.
  # storage:
  #   storageClassName: ""
  #   size: "1Gi"
  #   accessMode: ReadWriteMany

  # ============================================================
  # Webhook Receiver
  # ============================================================
  webhook:
    # +kubebuilder:default=true
    enabled: true
    # +kubebuilder:default=8443
    port: 8443
    # HMAC secret for webhook payload verification (constant-time HMAC comparison enforced)
    secretRef:
      name: "ignition-sync-webhook-secret"
      key: "hmac-key"
    # Accepted source formats (controller auto-detects)
    # - argocd:  ArgoCD resource hook / notification payload
    # - kargo:   Kargo promotion event
    # - github:  GitHub release/tag webhook
    # - generic: { "ref": "2.0.0" }

  # ============================================================
  # Polling (safety net)
  # ============================================================
  polling:
    # +kubebuilder:default=true
    enabled: true
    # +kubebuilder:default="60s"
    interval: 60s

  # ============================================================
  # Ignition Gateway Connection
  # ============================================================
  # Applied to all discovered gateways in this namespace.
  # Individual gateways can override via annotations.
  gateway:
    # +kubebuilder:default=8043
    port: 8043
    # +kubebuilder:default=true
    tls: true
    apiKeySecretRef:
      name: "ignition-api-key"
      key: "apiKey"

  # ============================================================
  # Exclude Patterns — applied after staging
  # ============================================================
  # Global patterns apply to all gateways. Per-profile patterns
  # are set in SyncProfile.spec.excludePatterns (merged additively).
  # +kubebuilder:default={"**/.git/","**/.gitkeep","**/.resources/**"}
  excludePatterns:
    - "**/.git/"
    - "**/.gitkeep"
    - "**/.resources/**"     # MANDATORY — always enforced by agent even if omitted

  # NOTE: ** glob patterns use the doublestar library (github.com/bmatcuk/doublestar)
  # for recursive matching. Go's filepath.Match does NOT support ** natively.

  # ────────────────────────────────────────────────────────────
  # DEPRECATED — replaced by SyncProfile CRD (see 07-sync-profile.md)
  # The following fields are removed in favor of SyncProfile's
  # ordered source→destination mappings:
  #   - siteNumber      → future: template variables via hooks
  #   - shared           → SyncProfile.spec.mappings
  #   - additionalFiles  → SyncProfile.spec.mappings
  #   - normalize        → future: pre/post-sync hooks
  # ────────────────────────────────────────────────────────────

  # ============================================================
  # Bi-Directional Sync (gateway → git)
  # ============================================================
  bidirectional:
    enabled: false
    # Paths to watch for changes on the gateway filesystem
    # Only paths in this allowlist can flow back to git — defense in depth
    watchPaths:
      - "config/resources/core/ignition/tag-definition/**"
      - "projects/**/com.inductiveautomation.perspective/views/**"
    # Branch to push gateway changes to
    targetBranch: "gateway-changes/{{.Namespace}}"
    # Debounce: wait this long after last change before creating PR
    debounce: 30s
    createPR: true
    prLabels:
      - "gateway-change"
      - "auto-generated"
    # Conflict resolution strategy
    # - gitWins:      git always wins; gateway changes are PR'd but overwritten on next sync (default)
    # - gatewayWins:  gateway changes block sync until the PR is merged or closed
    # - manual:       sync pauses on conflict, operator reports condition, user resolves
    conflictStrategy: gitWins
    # Guardrails — prevent accidental data exfiltration or repo bloat
    guardrails:
      maxFileSize: "10Mi"        # Max size per file pushed to git
      maxFilesPerPR: 100         # Max files per PR
      excludePatterns:           # Never push these back to git
        - "**/.resources/**"
        - "**/secrets/**"
        - "**/*.jar"

  # ============================================================
  # Validation & Safety
  # ============================================================
  validation:
    dryRunBefore: false   # Dry-run sync before applying
    webhook:
      url: ""             # Optional pre-sync validation webhook
      timeout: 10s

  # ============================================================
  # Snapshots & Rollback
  # ============================================================
  snapshots:
    enabled: false
    retentionCount: 5
    storage:
      type: "pvc"  # or "s3", "gcs"
      s3:
        bucket: ""
        keyPrefix: ""

  # ============================================================
  # Deployment Strategy
  # ============================================================
  deployment:
    strategy: "all-at-once"  # or "canary"
    stages: []               # For canary strategy
    syncOrder: []            # Dependency-aware ordering
    autoRollback:
      enabled: false
      triggers:
        - "scanFailure"

  # ============================================================
  # Emergency Control
  # ============================================================
  paused: false            # Set to true to halt all syncs

  # ============================================================
  # Ignition-Specific Configuration
  # ============================================================
  ignition:
    designerSessionPolicy: "wait"  # or "proceed", "fail"
    perspectiveSessionPolicy: "graceful"
    redundancyRole: ""             # or "primary", "backup"
    peerGatewayName: ""

  # ============================================================
  # Agent Configuration
  # ============================================================
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
      pullPolicy: IfNotPresent
      digest: ""          # Optional: pinned digest for supply chain security
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 256Mi
```

### CRD Versioning Strategy

The CRD starts at `v1alpha1` with a planned migration path:

- **`v1alpha1`** — current version. Marked as `+kubebuilder:storageversion`. All fields are considered experimental and may change.
- **`v1beta1`** — targeted once the API has stabilized through production use. Breaking changes from `v1alpha1` are handled by a conversion webhook (included in Helm chart from day one, initially a no-op).
- **`v1`** — stable API. No breaking changes without a new API version.

Fields are annotated in Go types to indicate stability:

```go
type IgnitionSyncSpec struct {
    // Stable — will not change in v1beta1
    Git     GitSpec     `json:"git"`
    // Deprecated: Storage is no longer used. Agent clones locally.
    // +optional
    Storage StorageSpec `json:"storage,omitempty"`
    Gateway GatewaySpec `json:"gateway"`

    // Experimental — may change in v1beta1
    // +kubebuilder:validation:Optional
    Bidirectional *BidirectionalSpec `json:"bidirectional,omitempty"`
    Snapshots     *SnapshotSpec      `json:"snapshots,omitempty"`
    Deployment    *DeploymentSpec    `json:"deployment,omitempty"`
}
```

The Helm chart includes a conversion webhook endpoint from v1 release. For `v1alpha1`-only deployments, it's a passthrough. When `v1beta1` is introduced, the webhook handles automatic conversion so existing CRs continue working without manual migration.

### Status (Managed by Controller)

```yaml
status:
  observedGeneration: 3
  lastSyncTime: "2026-02-12T10:30:00Z"
  lastSyncRef: "2.0.0"
  lastSyncCommit: "abc123f"
  refResolutionStatus: Resolved    # NotResolved | Resolving | Resolved | Error

  # Discovered gateways — populated by the controller watching annotated pods
  discoveredGateways:
    - name: site
      namespace: site1
      podName: site1-site-gateway-0
      servicePath: "services/site"
      syncStatus: Synced       # Pending | Syncing | Synced | Error
      lastSyncTime: "2026-02-12T10:30:05Z"
      lastSyncDuration: "3.2s"
      syncedCommit: "abc123f"
      syncedRef: "2.0.0"
      agentVersion: "1.0.0"
      lastScanResult: "projects=200 config=200"
      filesChanged: 47
      projectsSynced: ["site", "area1"]
      lastSnapshot:
        id: "site-20260212-102959.tar.gz"
        size: "256MB"
        timestamp: "2026-02-12T10:29:59Z"
      syncHistory:
        - timestamp: "2026-02-12T10:30:05Z"
          commit: "abc123f"
          result: "success"
          duration: "3.2s"
        - timestamp: "2026-02-12T10:20:00Z"
          commit: "def456g"
          result: "success"
          duration: "2.8s"
    - name: area1
      namespace: site1
      podName: site1-area1-gateway-0
      servicePath: "services/area"
      syncStatus: Synced
      lastSyncTime: "2026-02-12T10:30:08Z"
      lastSyncDuration: "2.5s"
      syncedCommit: "abc123f"
      syncedRef: "2.0.0"
      agentVersion: "1.0.0"
      lastScanResult: "projects=200 config=200"
    - name: area2
      namespace: site1
      podName: site1-area2-gateway-0
      servicePath: "services/area"
      syncStatus: Syncing
      lastSyncTime: "2026-02-12T10:29:00Z"

  # Conditions — the canonical status reporting mechanism
  conditions:
    - type: Ready
      status: "False"
      reason: GatewaysSyncing
      message: "4 of 5 gateways synced"
      lastTransitionTime: "2026-02-12T10:30:00Z"
      observedGeneration: 3

    - type: RefResolved
      status: "True"
      reason: RefResolved
      message: "Ref 2.0.0 resolved to abc123f"
      lastTransitionTime: "2026-02-12T10:00:00Z"
      observedGeneration: 3

    - type: WebhookReady
      status: "True"
      reason: ListenerActive
      message: "Webhook listener active on port 8443"
      lastTransitionTime: "2026-02-12T10:00:05Z"
      observedGeneration: 3

    - type: AllGatewaysSynced
      status: "False"
      reason: SyncInProgress
      message: "area2 still syncing (started 2026-02-12T10:30:00Z)"
      lastTransitionTime: "2026-02-12T10:30:00Z"
      observedGeneration: 3

    - type: BidirectionalReady
      status: "True"
      reason: WatchersActive
      message: "inotify watchers active on 5 gateways"
      lastTransitionTime: "2026-02-12T10:00:10Z"
      observedGeneration: 3
```

Key status design decisions following K8s conventions:

- **`observedGeneration`** on both the top-level status and on each condition — lets clients know if the status reflects the current spec.
- **Conditions over phases** — `Ready`, `RefResolved`, `AllGatewaysSynced`, `WebhookReady`, `BidirectionalReady` give a multi-dimensional view. No single "phase" field that can only express one state.
- **`discoveredGateways`** is dynamic — controller populates it by watching for pods with `ignition-sync.io/cr-name` matching this CR. Gateways appear/disappear as pods are created/deleted.

---

## SyncProfile CRD

> Full design document: [07-sync-profile.md](07-sync-profile.md)

`SyncProfile` is a second namespace-scoped CRD that defines **ordered source→destination mappings** for a gateway role. It replaces the opinionated `shared`, `additionalFiles`, `normalize`, and `siteNumber` fields from IgnitionSync with a generic abstraction.

**Why a separate CRD?** SyncProfile separates "what files go where" (file routing) from "how to connect to git and gateways" (infrastructure). Multiple gateways with the same role (e.g., 4 area gateways) reference a single SyncProfile instead of duplicating mapping annotations on each pod.

### Minimal SyncProfile

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
```

Pods reference it via annotation:

```yaml
podAnnotations:
  ignition-sync.io/inject: "true"
  ignition-sync.io/cr-name: "proveit-sync"
  ignition-sync.io/sync-profile: "proveit-area"
```

### Deprecation of IgnitionSync File-Routing Fields

| Removed Field | SyncProfile Replacement |
|---------------|------------------------|
| `spec.siteNumber` | Future: template variables via hooks |
| `spec.shared` (externalResources, scripts, udts) | `SyncProfile.spec.mappings` |
| `spec.additionalFiles` | `SyncProfile.spec.mappings` |
| `spec.normalize` | Future: pre/post-sync hooks |

This is a breaking change at v1alpha1. Pods without a `sync-profile` annotation continue to work in 2-tier mode using existing annotations (`service-path`, `deployment-mode`, etc.).

### 3-Tier Configuration Precedence

```
annotation > SyncProfile > IgnitionSync > defaults
```

See [07-sync-profile.md](07-sync-profile.md) for the full CRD spec, Go types, worked examples, and backward compatibility details.

---

