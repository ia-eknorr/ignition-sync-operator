# Ignition Sync Operator — Architecture Design (v3)

> **This document has been split into smaller files under [`docs/architecture/`](architecture/).** The split versions are the authoritative source. This file is kept for git history reference.
>
> - [00-overview.md](architecture/00-overview.md) — Vision, design principles, quick start, architecture overview
> - [01-crd.md](architecture/01-crd.md) — Custom Resource Definition
> - [02-controller.md](architecture/02-controller.md) — Controller manager, RBAC, storage, multi-repo
> - [03-sync-agent.md](architecture/03-sync-agent.md) — Sync agent, sync flow, Ignition-aware sync
> - [04-deployment-operations.md](architecture/04-deployment-operations.md) — Helm chart, safety, observability, scale
> - [05-enterprise-examples.md](architecture/05-enterprise-examples.md) — Integration patterns, examples, enterprise
> - [06-security-testing-roadmap.md](architecture/06-security-testing-roadmap.md) — Security, testing, migration, roadmap

## Vision

A first-class Kubernetes operator, published and maintained by Inductive Automation alongside the `ignition` Helm chart at `charts.ia.io`. It provides declarative, webhook-driven, bi-directional git synchronization for Ignition gateway deployments — replacing the current git-sync sidecar pattern with a purpose-built, production-ready solution.

The operator auto-discovers Ignition gateway pods via annotations, injects sync agent sidecars through a mutating admission webhook, manages one or more git repositories, and reconciles file state across any number of gateways and namespaces. It works on any Kubernetes distribution — EKS, GKE, AKS, on-prem, single-node — with configurable storage backends.

## Design Principles

This operator is built on core principles that guide all architectural and implementation decisions:

1. **Annotation-Driven Discovery** — Gateways declare intent via Kubernetes annotations. No hardcoded lists or custom CRD resource blocks per gateway.
2. **K8s-Native Patterns** — Uses ConfigMaps for metadata signaling (preferred over trigger files), informers for change detection, conditions for status reporting, and standard K8s conventions for RBAC and ownership.
3. **Ignition Domain Awareness** — Deep understanding of Ignition's architecture: gateway hierarchy, tag inheritance, module systems, scan API semantics, session management, and configuration best practices.
4. **Security by Default** — No plaintext secrets in CRDs, HMAC validation on webhooks, signed container images, air-gap support, and least-privilege access controls.
5. **Cloud-Agnostic** — Works on any Kubernetes distribution without vendor lock-in. Abstracts storage backend (EFS, Filestore, NFS, Longhorn) behind a StorageClass interface.

```
charts.ia.io/
├── ignition               # The Ignition gateway Helm chart
├── ignition-sync          # The Ignition Sync Operator chart
└── (future: ignition-stack, etc.)
```

---

## Quick Start

Get a single-gateway sync running in under 5 minutes:

```bash
# 1. Install the operator
helm repo add ia https://charts.ia.io
helm install ignition-sync ia/ignition-sync -n ignition-sync-system --create-namespace

# 2. Create a git auth secret
kubectl create secret generic git-sync-secret -n default \
  --from-file=ssh-privatekey=$HOME/.ssh/id_rsa

# 3. Create an API key secret
kubectl create secret generic ignition-api-key -n default \
  --from-literal=apiKey=YOUR_IGNITION_API_KEY

# 4. Create the IgnitionSync CR (minimal — defaults handle the rest)
cat <<EOF | kubectl apply -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: my-sync
  namespace: default
spec:
  git:
    repo: "git@github.com:myorg/my-ignition-app.git"
    ref: "main"
    auth:
      sshKey:
        secretRef:
          name: git-sync-secret
          key: ssh-privatekey
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# 5. Add annotation to your Ignition gateway pod (via Helm values)
# gateway:
#   podAnnotations:
#     ignition-sync.io/inject: "true"
#     ignition-sync.io/service-path: "services/gateway"

# 6. Check status
kubectl get ignitionsyncs
```

That's it. The operator auto-discovers the gateway via annotation, injects the sync agent sidecar, clones the repo, and syncs files. All other fields (`storage`, `polling`, `webhook`, `excludePatterns`) use sensible defaults.

---

## Problem Statement

The current git-sync approach has fundamental limitations:

1. **Polling-only** — relies on a configurable interval, no event-driven updates
2. **One clone per sidecar** — 5 gateways = 5 identical git clones in the same namespace
3. **One-directional only** — no path for gateway changes to flow back to git
4. **Fighting the tool** — we override the entrypoint, bypass the exec hook model, and only use git-sync for SSH auth
5. **Limited tooling** — the git-sync image only has `cp` and basic coreutils; no rsync, jq, or yq
6. **No observability** — sync status is buried in container logs with no structured reporting
7. **Tightly coupled** — hardcoded `site`/`area*` path mapping, per-project script destinations, provider-specific assumptions

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  Cluster-Scoped                                                              │
│                                                                              │
│  ┌─────────────────────────────────────────────────┐                         │
│  │  Ignition Sync Controller Manager               │                         │
│  │  (Deployment, leader-elected, 1 active replica) │                         │
│  │                                                  │                         │
│  │  Reconciles: IgnitionSync CRs (all namespaces)  │                         │
│  │  Manages: Repo PVCs, git clones, PR creation    │                         │
│  │  Reports: CR .status with conditions             │                         │
│  └─────────────────────┬───────────────────────────┘                         │
│                        │                                                     │
│  ┌─────────────────────┴───────────────────────────┐                         │
│  │  Mutating Admission Webhook                      │                         │
│  │  (separate Deployment, HA, TLS via cert-manager) │                         │
│  │                                                  │                         │
│  │  Watches: Pod CREATE with annotation             │                         │
│  │    ignition-sync.io/inject: "true"               │                         │
│  │  Injects: Sync agent sidecar + volumes           │                         │
│  └──────────────────────────────────────────────────┘                         │
│                                                                              │
│  ┌─────────────────────────────────────────────────┐                         │
│  │  Webhook Receiver (Deployment or in-controller)  │                         │
│  │                                                  │                         │
│  │  POST /webhook/{namespace}/{crName}               │                         │
│  │  Accepts: ArgoCD, Kargo, GitHub, generic         │                         │
│  │  Action: Annotates CR → triggers reconcile       │                         │
│  └──────────────────────────────────────────────────┘                         │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│  Namespace: site1                                                            │
│                                                                              │
│  ┌───────────────────┐   ┌───────────────────┐   ┌───────────────────┐       │
│  │ IgnitionSync CR   │   │ Repo PVC (RWX)    │   │ Webhook Secret    │       │
│  │ "proveit-sync"    │   │ ignition-sync-    │   │ (HMAC for auth)   │       │
│  │                   │   │ repo-proveit-sync │   │                   │       │
│  └───────────────────┘   └────────┬──────────┘   └───────────────────┘       │
│                                   │                                          │
│  ┌────────────────────────────────┼─────────────────────────────────────┐    │
│  │ StatefulSet: site              │                                      │    │
│  │ ┌───────────┐ ┌───────────┐   │                                      │    │
│  │ │ ignition  │ │ sync-agent│◄──┘  /repo (RO)                          │    │
│  │ │ container │ │ (injected)│      /ignition-data (RW, shared w/ gw)   │    │
│  │ │           │ │           │                                           │    │
│  │ │  annotations:           │                                           │    │
│  │ │  ignition-sync.io/inject: "true"                                   │    │
│  │ │  ignition-sync.io/cr-name: "proveit-sync"                          │    │
│  │ │  ignition-sync.io/service-path: "services/site"                    │    │
│  │ └───────────┘ └───────────┘                                           │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌────────────────────────────────┬─────────────────────────────────────┐    │
│  │ StatefulSet: area1             │                                      │    │
│  │ ┌───────────┐ ┌───────────┐   │                                      │    │
│  │ │ ignition  │ │ sync-agent│◄──┘  /repo (RO)                          │    │
│  │ │ container │ │ (injected)│      /ignition-data (RW)                  │    │
│  │ └───────────┘ └───────────┘                                           │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
│  ... area2, area3, area4                                                     │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│  Namespace: site2                                                            │
│  (same pattern — own IgnitionSync CR, own Repo PVC, own gateway pods)        │
└──────────────────────────────────────────────────────────────────────────────┘
```

The operator has three logical components:

1. **Controller Manager** — a single cluster-scoped Deployment that reconciles all `IgnitionSync` CRs across namespaces, manages git clones, handles bi-directional PR creation, and reports status.

2. **Mutating Admission Webhook** — a separate high-availability Deployment that intercepts Pod creation and injects sync agent sidecars into annotated Ignition gateway pods. TLS certificates managed by cert-manager.

3. **Sync Agent** — a lightweight sidecar container injected into each Ignition gateway pod. No git operations — it reads from the shared repo PVC and writes to the local gateway data volume using rsync, jq, and yq.

---

## Gateway Discovery & Sidecar Injection

The operator discovers Ignition gateways via pod annotations, following the established pattern used by Istio, Vault Agent, and Linkerd. No gateways are hardcoded in the CRD — the CRD defines *what* to sync (repo, shared resources, service path mappings), and annotations on the pods declare *which* gateways participate.

### Annotations

Applied to Ignition gateway pods (via `podAnnotations` in the ignition Helm chart values):

| Annotation | Required | Description |
|---|---|---|
| `ignition-sync.io/inject` | Yes | `"true"` to enable sidecar injection |
| `ignition-sync.io/cr-name` | No* | Name of the `IgnitionSync` CR in this namespace. *Auto-derived if exactly one CR exists in the namespace. |
| `ignition-sync.io/service-path` | Yes | Repo-relative path to this gateway's service directory |
| `ignition-sync.io/gateway-name` | No | Override gateway identity (defaults to pod label `app.kubernetes.io/name`) |
| `ignition-sync.io/deployment-mode` | No | Config resource overlay to apply (e.g., `prd-cloud`) |
| `ignition-sync.io/tag-provider` | No | UDT tag provider destination (default: `default`) |
| `ignition-sync.io/sync-period` | No | Fallback poll interval in seconds (default: `30`) |
| `ignition-sync.io/exclude-patterns` | No | Comma-separated exclude globs for this gateway |
| `ignition-sync.io/system-name` | No | Override for config normalization systemName |
| `ignition-sync.io/system-name-template` | No | Go template for systemName (default: `{{.GatewayName}}` if omitted) |

### Example: ProveIt Site Chart

```yaml
# values.yaml — site gateway
site:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/site"
      ignition-sync.io/deployment-mode: "prd-cloud"
      ignition-sync.io/tag-provider: "default"
      ignition-sync.io/system-name-template: "site{{.SiteNumber}}"

# values.yaml — area gateways (all share the same config)
area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/area"
      ignition-sync.io/tag-provider: "edge"
      ignition-sync.io/system-name-template: "site{{.SiteNumber}}-{{.GatewayName}}"
```

### Example: Public Demo Chart

```yaml
# 2-gateway pattern with replicated frontends
frontend:
  gateway:
    replicas: 5
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "publicdemo-sync"
      ignition-sync.io/service-path: "services/ignition-frontend"

backend:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "publicdemo-sync"
      ignition-sync.io/service-path: "services/ignition-backend"
```

### Example: Simple Single-Gateway

```yaml
ignition:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "my-sync"
      ignition-sync.io/service-path: "services/gateway"
```

### How Injection Works

The `MutatingWebhookConfiguration` targets Pod CREATE events where `ignition-sync.io/inject: "true"` is present. The webhook:

1. Reads the pod annotations to determine CR name, service path, etc.
2. Looks up the referenced `IgnitionSync` CR in the pod's namespace.
3. **Validates service-path** — checks that `ignition-sync.io/service-path` is a valid relative path (no `..`, no absolute paths). Logs a warning if the path cannot be validated against the repo at injection time (repo may not be cloned yet). Agent validates path existence at sync time.
4. Injects a sidecar container with the sync agent image.
5. Adds volume mounts: shared repo PVC (read-only), agent config (projected ConfigMap/downward API).
6. Adds the Ignition API key secret volume mount (from the CR spec).
7. Sets environment variables derived from annotations + CR spec.
8. Adds a startup probe so the gateway doesn't start before initial sync completes.

The webhook **does not modify** if the annotation is absent or `"false"`, and it is configured with `failurePolicy: Ignore` so a webhook outage doesn't block unrelated pod creation.

---

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
  # Storage — shared repo PVC
  # ============================================================
  storage:
    # +kubebuilder:default=""
    storageClassName: ""        # e.g., "efs-sc", "nfs-client", "longhorn"; empty = cluster default
    # +kubebuilder:default="1Gi"
    size: "1Gi"
    # +kubebuilder:default=ReadWriteMany
    accessMode: ReadWriteMany   # ReadWriteOnce if only one gateway pod exists

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
  # Site Identity (for config normalization)
  # ============================================================
  siteNumber: "1"

  # ============================================================
  # Shared Resources — synced to all discovered gateways
  # ============================================================
  shared:
    externalResources:
      enabled: true
      source: "shared/ignition-gateway/config/resources/external"
      # If source doesn't exist, create a minimal config-mode.json
      createFallback: true

    scripts:
      enabled: true
      source: "shared/scripts"
      # Destination relative to /ignition-data/projects/{projectName}/
      # {projectName} derived from the service path basename
      destPath: "ignition/script-python/exchange/proveit2026"

    udts:
      enabled: true
      source: "shared/udts"
      # tagProvider is set per-gateway via annotation

  # ============================================================
  # Additional Files — arbitrary file/directory syncs
  # ============================================================
  additionalFiles:
    - source: "shared/config/factory-config.json"
      dest: "factory-config.json"
      type: file
    # - source: "shared/custom-modules"
    #   dest: "user-lib/modules"
    #   type: dir

  # ============================================================
  # Exclude Patterns — applied after staging
  # ============================================================
  # Global patterns apply to all gateways. Per-gateway patterns
  # are set via the ignition-sync.io/exclude-patterns annotation.
  # +kubebuilder:default={"**/.git/","**/.gitkeep","**/.resources/**"}
  excludePatterns:
    - "**/.git/"
    - "**/.gitkeep"
    - "**/.resources/**"     # MANDATORY — always enforced by agent even if omitted

  # NOTE: ** glob patterns use the doublestar library (github.com/bmatcuk/doublestar)
  # for recursive matching. Go's filepath.Match does NOT support ** natively.

  # ============================================================
  # Config Normalization
  # ============================================================
  normalize:
    # Replace systemName in config.json files
    systemName: true
    # Additional field replacements (applied to all config.json files)
    # fields:
    #   - jsonPath: ".someField"
    #     valueTemplate: "{{.GatewayName}}-custom"

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
  repoCloneStatus: Cloned    # NotCloned | Cloning | Cloned | Error

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

    - type: RepoCloned
      status: "True"
      reason: CloneSucceeded
      message: "Repository cloned at ref 2.0.0 (abc123f)"
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
- **Conditions over phases** — `Ready`, `RepoCloned`, `AllGatewaysSynced`, `WebhookReady`, `BidirectionalReady` give a multi-dimensional view. No single "phase" field that can only express one state.
- **`discoveredGateways`** is dynamic — controller populates it by watching for pods with `ignition-sync.io/cr-name` matching this CR. Gateways appear/disappear as pods are created/deleted.

---

## Controller Manager

### Language & Framework

**Go + kubebuilder (controller-runtime)**

This is the standard for Kubernetes operators used by cert-manager, external-secrets, Flux, and hundreds of production operators.

- Native CRD code generation from Go types
- Built-in reconciliation loop, leader election, webhook serving, health probes
- Multi-namespace watch with configurable label/field selectors
- Excellent testing framework (envtest for integration tests)
- Static binary → distroless base image (~20MB)
- Publishable via OLM (Operator Lifecycle Manager) for OpenShift and operator hubs

### Deployment Topology

```yaml
# Controller Manager — cluster-scoped, leader-elected
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ignition-sync-controller-manager
  namespace: ignition-sync-system     # operator's own namespace
spec:
  replicas: 2                          # HA — leader election ensures 1 active
  selector:
    matchLabels:
      app.kubernetes.io/name: ignition-sync-controller
  template:
    spec:
      serviceAccountName: ignition-sync-controller
      containers:
        - name: manager
          image: ghcr.io/inductiveautomation/ignition-sync-controller:1.0.0
          args:
            - --leader-elect=true
            - --health-probe-bind-address=:8081
            - --metrics-bind-address=:8080
            # Optional namespace filter — empty = all namespaces
            - --watch-namespaces=""
            # Label selector for narrowing watched resources
            - --watch-label-selector=""
          ports:
            - name: metrics
              containerPort: 8080
            - name: health
              containerPort: 8081
            - name: webhook-recv
              containerPort: 8443
          livenessProbe:
            httpGet:
              path: /healthz
              port: health
          readinessProbe:
            httpGet:
              path: /readyz
              port: health
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi
```

```yaml
# Admission Webhook — separate Deployment for HA, TLS via cert-manager
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ignition-sync-webhook
  namespace: ignition-sync-system
spec:
  replicas: 2       # HA — both active (no leader election needed)
  selector:
    matchLabels:
      app.kubernetes.io/name: ignition-sync-webhook
  template:
    spec:
      serviceAccountName: ignition-sync-webhook
      containers:
        - name: webhook
          image: ghcr.io/inductiveautomation/ignition-sync-controller:1.0.0
          args:
            - --mode=webhook
            - --webhook-port=9443
          ports:
            - name: webhook
              containerPort: 9443
          volumeMounts:
            - name: tls
              mountPath: /tmp/k8s-webhook-server/serving-certs
              readOnly: true
      volumes:
        - name: tls
          secret:
            secretName: ignition-sync-webhook-tls    # managed by cert-manager
```

Separating the webhook from the controller follows the cert-manager pattern. The webhook must be highly available (pod creation depends on it), while the controller can tolerate brief leader-election failovers.

### RBAC

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ignition-sync-controller
rules:
  # IgnitionSync CRs — spec, status, and finalizers
  - apiGroups: ["sync.ignition.io"]
    resources: ["ignitionsyncs"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: ["sync.ignition.io"]
    resources: ["ignitionsyncs/status"]
    verbs: ["update", "patch"]
  - apiGroups: ["sync.ignition.io"]
    resources: ["ignitionsyncs/finalizers"]
    verbs: ["update"]

  # Pods — for gateway discovery (read-only)
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]

  # PVCs — for repo PVC lifecycle
  - apiGroups: [""]
    resources: ["persistentvolumeclaims"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]

  # ConfigMaps — for sync metadata signaling to agents
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

  # Secrets — for git auth and API keys (read-only)
  # NOTE: In namespace-scoped mode, use Roles instead of ClusterRole to restrict scope.
  # The Helm chart generates per-namespace Role+RoleBinding when watchNamespaces is set.
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch"]

  # Events — for recording sync events
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]

  # Leases — for leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

For restricted environments, the Helm chart supports a `--watch-namespaces` flag that limits the controller to specific namespaces. When set, the chart generates namespace-scoped `Role` + `RoleBinding` per watched namespace instead of the `ClusterRole` shown above. This restricts Secret read access to only the namespaces that contain `IgnitionSync` CRs.

### Reconciliation Loop

**Controller Setup:**
- `MaxConcurrentReconciles: 5` (configurable) to prevent a single slow git clone from blocking all CRs
- `GenerationChangedPredicate` on the primary watch — prevents reconciliation storms from status-only updates
- Pod watch filtered to only pods with `ignition-sync.io/cr-name` annotation
- Context timeout on all git operations (default: 2m)

```
Watch: IgnitionSync CRs (all namespaces, or filtered)
  Predicate: GenerationChangedPredicate (skip status-only updates)
Watch: Pods with annotation ignition-sync.io/cr-name (for gateway discovery)
  Predicate: filter to only annotated pods
Owns: PVCs, ConfigMaps (for sync metadata)
       ↓
┌──────────────────────────────────────────────────────────────────┐
│  0. Finalizer handling                                           │
│     - If CR is being deleted (DeletionTimestamp set):            │
│       a. Signal agents to stop via ConfigMap (set "terminating") │
│       b. Wait for agents to acknowledge (with 30s timeout)       │
│       c. Clean up webhook receiver route                         │
│       d. Remove finalizer → allow garbage collection             │
│     - If finalizer not present: add it, return, requeue          │
├──────────────────────────────────────────────────────────────────┤
│  1. Validate CR spec                                             │
│     - Git repo URL and auth secret exist                         │
│     - Storage class exists (if specified)                        │
│     - Referenced secrets exist in CR's namespace                 │
├──────────────────────────────────────────────────────────────────┤
│  2. Ensure repo PVC exists                                       │
│     - Create RWX PVC in CR's namespace if not exists             │
│     - Name: ignition-sync-repo-{crName}                         │
│     - StorageClass from spec.storage.storageClassName            │
│     - Owner reference: the IgnitionSync CR (garbage collected)   │
├──────────────────────────────────────────────────────────────────┤
│  3. Clone or update repo (non-blocking)                          │
│     - Git operations run in a dedicated goroutine with context   │
│       timeout (default: 2m). Controller does not block on git.   │
│     - git clone (if PVC is empty)                                │
│     - git fetch + git checkout {spec.git.ref} (if exists)        │
│     - Update ConfigMap ignition-sync-metadata-{crName} with:     │
│       commit SHA, ref, trigger timestamp                         │
│     - Update condition: RepoCloned = True                        │
│     - If git operation is still in progress, requeue after 5s    │
├──────────────────────────────────────────────────────────────────┤
│  4. Discover gateways                                            │
│     - List pods in CR's namespace with annotation:               │
│       ignition-sync.io/cr-name == {crName}                       │
│     - Populate status.discoveredGateways from live pod list      │
│     - Remove entries for pods that no longer exist               │
├──────────────────────────────────────────────────────────────────┤
│  5. Collect gateway sync status                                  │
│     - Read sync status from ConfigMap                            │
│       ignition-sync-status-{crName} (written by agents)          │
│     - Update discoveredGateways[].syncStatus                     │
│     - Update condition: AllGatewaysSynced                        │
├──────────────────────────────────────────────────────────────────┤
│  6. Process bi-directional changes (if enabled)                  │
│     - Check /repo/.sync-changes/{gatewayName}/ for change files  │
│     - Apply conflict resolution strategy                         │
│     - git checkout -b {targetBranch}                             │
│     - Apply changes, commit, push                                │
│     - Create PR via GitHub API (if githubApp auth configured)    │
│     - Clean up processed change files                            │
│     - Update condition: BidirectionalReady                       │
├──────────────────────────────────────────────────────────────────┤
│  7. Update CR status                                             │
│     - Set observedGeneration = metadata.generation               │
│     - Set lastSyncTime, lastSyncRef, lastSyncCommit              │
│     - Set Ready condition based on all sub-conditions            │
│     - Emit Kubernetes events for significant state changes       │
├──────────────────────────────────────────────────────────────────┤
│  8. Requeue                                                      │
│     - On success: requeue after spec.polling.interval            │
│     - On error: exponential backoff (controller-runtime default) │
│     - On webhook trigger: immediate requeue (no delay)           │
└──────────────────────────────────────────────────────────────────┘
```

### Webhook Receiver

The controller exposes an HTTP endpoint for external systems to trigger sync:

```
POST /webhook/{namespace}/{crName}
Headers:
  X-Hub-Signature-256: sha256=...   (HMAC verification, constant-time comparison)
  Content-Type: application/json

Accepted payload formats (auto-detected):

1. Generic:
   { "ref": "2.0.0" }

2. GitHub release/tag:
   { "action": "published", "release": { "tag_name": "2.0.0" } }

3. ArgoCD notification:
   { "app": { "metadata": { "annotations": { "git.ref": "2.0.0" } } } }

4. Kargo promotion:
   { "freight": { "commits": [{ "tag": "2.0.0" }] } }

Response:
  202 Accepted - { "accepted": true, "ref": "2.0.0" }
  401 Unauthorized - HMAC validation failed
  404 Not Found - CR not found
```

**Important:** The receiver does **not** mutate `spec.git.ref`. Mutating a CR's spec from an HTTP endpoint is an anti-pattern — it conflicts with GitOps tools (ArgoCD would detect drift and revert) and breaks audit trails. Instead, the receiver writes to annotations:

```yaml
metadata:
  annotations:
    sync.ignition.io/requested-ref: "2.0.0"
    sync.ignition.io/requested-at: "2026-02-12T10:30:00Z"
    sync.ignition.io/requested-by: "webhook"
```

The controller reads the annotation, compares against `spec.git.ref`, and acts on the requested ref. The `spec.git.ref` remains under user/GitOps control — users update it via `kubectl`, Helm values, or ArgoCD Application manifests.

**Security:** HMAC validation uses `crypto/subtle.ConstantTimeCompare` to prevent timing oracle attacks. HMAC is validated **before** any CR lookup to prevent namespace/CR enumeration.

For ArgoCD integration, this replaces the post-sync Job pattern with a simpler ArgoCD Notification subscription or Kargo analysis step.

---

## Sync Agent

### Image & Implementation Strategy

```dockerfile
# Build stage
FROM golang:1.23 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o sync-agent ./cmd/agent/

# Production image — distroless for minimal attack surface (~20MB)
FROM gcr.io/distroless/static-debian12:nonroot

# Primary: Go binary implements all sync logic (file sync, JSON/YAML transforms, metadata handling)
COPY --from=builder /app/sync-agent /sync-agent

ENTRYPOINT ["/sync-agent"]
```

**Why distroless over Alpine:** The agent is a Go-first binary that handles all file operations in-process. No rsync, jq, curl, or shell needed in production. Distroless eliminates the shell attack surface entirely — there's no `sh` to exec into, reducing CVE exposure. Optional shell hooks (for advanced users) require a separate Alpine-based variant image.


The agent is **Go-first**: the sync agent binary is a self-contained executable that handles:
- File synchronization (rsync-equivalent operations using Go's `filepath` and `io` packages)
- Glob pattern matching with `**` support (using `github.com/bmatcuk/doublestar` — Go's `filepath.Match` does not support `**`)
- JSON/YAML manipulation (using Go's `encoding/json` and `gopkg.in/yaml.v2`)
- Metadata and ConfigMap-based signaling
- Health endpoints and status reporting
- Bidirectional change detection

Optional shell scripts (`/opt/ignition-sync/hooks/`) are available as an **escape hatch** for advanced users who need custom sync logic, not as the primary mechanism. This approach:
- Eliminates the operational complexity of coordinating Go + shell
- Provides better performance (no subprocess overhead for rsync/jq)
- Improves debuggability (single binary, structured logging)
- Reduces image size and attack surface

Image size: ~20MB (distroless + static Go binary). An Alpine-based variant (`-alpine` tag) is available for users who need shell hooks.

### Agent Sync Flow

#### Trigger Mechanism: K8s ConfigMap

The controller signals sync availability via ConfigMap:

- **ConfigMap Watch** — Controller writes sync metadata to a ConfigMap (`ignition-sync-metadata-{crName}`); agent uses K8s informer to watch changes. Fast, event-driven, no polling. Agent receives push notifications when a new commit is available.
- **Fallback: Polling Timer** — Agent polls the ConfigMap at `spec.polling.interval` (default: 60s) as a safety net in case the informer watch is disrupted.

ConfigMap is the single communication mechanism between controller and agents. No file-based signaling via PVC — this simplifies the architecture, eliminates PVC write contention concerns, and keeps controller-agent communication on well-understood K8s primitives.

#### Full Sync Flow

```
Startup:
  1. Mount /repo (RO) — shared PVC from controller
  2. Mount /ignition-data (RW) — gateway's data PVC (shared with ignition container)
  3. Read config from environment variables (set by webhook injection)
  4. Establish K8s API connection for ConfigMap watch (if KUBECONFIG available)
  5. Perform initial sync (blocking — gateway doesn't start until initial sync succeeds)
  6. Start watching ConfigMap for changes (preferred) or inotify fallback
  7. Start periodic fallback timer (configurable, default 30s)
  8. If bidirectional: start inotify on configured watch paths
  9. Expose health endpoint on :8082

On trigger (new content available via ConfigMap watch or polling timer):
  1. Read ref + commit from ConfigMap data
  2. Compare against last-synced commit — skip if unchanged
  3. **Project-Level Granular Sync**: Compute file-level SHA256 checksums of all files under {servicePath}
     - Compare against previous sync checksums (stored in ConfigMap)
     - If no file checksums changed, skip sync entirely (fast path)
     - If some files changed, identify changed PROJECT directories (not individual files)
     - Build sync scope: only include changed projects + shared resources that changed
  4. Create staging directory: /ignition-data/.sync-staging/
  5. Sync project files:
     - Source: /repo/{servicePath}/projects/{changedProject}/
     - Dest: staging/projects/{changedProject}/
     - Exclude patterns applied (using doublestar library for ** support)
  6. Sync config/resources/core (ALWAYS — overlay depends on fresh core):
     - Source: /repo/{servicePath}/config/resources/core/
     - Dest: staging/config/resources/core/
  7. Apply deployment mode overlay ON TOP OF core (ALWAYS recomposed):
     - Source: /repo/{servicePath}/config/resources/{deploymentMode}/
     - Dest: staging/config/resources/core/   ← overlay files overwrite core files
     - NOTE: overlay is ALWAYS applied after core, even if only core changed.
       This preserves correct precedence — overlay files override core defaults.
       Skipping overlay when "unchanged" would lose overrides on core updates.
  8. Sync shared resources:
     - External resources → staging/config/resources/external/ (if enabled)
       - If source dir doesn't exist in repo AND createFallback=true,
         create minimal dir with default config-mode.json
     - Scripts → staging/projects/{projectName}/{destPath}/ (if enabled)
     - UDTs → staging/config/resources/core/ignition/tag-type-definition/{tagProvider}/ (if enabled)
     - Additional files → staging/{dest} (if enabled)
  9. **Pre-Sync Validation**:
     - JSON syntax validation on all config.json files
     - Verify no .resources/ directory included in staging
     - Checksum verification on critical files
  10. Apply exclude patterns (combine global + per-gateway, doublestar matching)
  11. Normalize configs (recursive — ALL config.json files):
      - Use filepath.Walk to discover EVERY config.json in staging
      - Apply systemName replacement to each (not just top-level)
      - Use targeted JSON patching (modify field in-place) to avoid
        reformatting the entire file (prevents false diffs from re-serialization)
      - YAML manipulation (if needed) using in-process YAML library
      - Support Go template syntax for advanced field mappings
  12. Selective merge to live directory:
      - Walk staging/, copy files to /ignition-data/
      - Delete files in /ignition-data/ that are NOT in staging
        AND NOT in protected list (.resources/)
      - .resources/ is NEVER touched — not deleted, not overwritten, not modified
      - This is NOT an atomic swap — it is a merge with protected directories
  13. **Ignition API Health Check**:
      - GET /data/api/v1/status to verify gateway is responsive
      - If not ready, wait up to 5s before proceeding
  14. Trigger Ignition scan API (SKIP on initial sync):
      - On initial sync (first sync after pod startup): SKIP scan entirely.
        Ignition auto-scans its data directory on first boot. Calling the scan
        API during startup causes race conditions (duplicate project loads,
        partial config reads). The agent tracks an `initialSyncDone` flag.
      - On subsequent syncs:
        a. POST /data/api/v1/scan/projects (fire-and-forget — returns 200 immediately)
        b. POST /data/api/v1/scan/config (fire-and-forget)
        c. Order matters: projects MUST be scanned before config
        d. HTTP retry logic: 3 retries with exponential backoff on connection failures
        e. Accept any 2xx response as success — the scan runs asynchronously
           inside the gateway. Do NOT attempt to poll for completion.
  15. Write status to ConfigMap ignition-sync-status-{crName}:
      {
        "gateway": "{gatewayName}",
        "syncedAt": "2026-02-12T10:30:00Z",
        "commit": "abc123f",
        "ref": "2.0.0",
        "filesChanged": 47,
        "projectsSynced": ["site", "area1"],
        "scanResult": "projects=200 config=200",
        "duration": "3.2s",
        "checksums": { "projects/site/...": "sha256:...", ... }
      }
  16. Clean up staging directory

On filesystem change (bidirectional):
  1. inotify detects change in /ignition-data/{watchPath}
  2. Debounce (wait for configured quiet period)
  3. Diff changed files against /repo/{servicePath}/
  4. Write change manifest to /repo/.sync-changes/{gatewayName}/{timestamp}.json:
     {
       "gateway": "site",
       "timestamp": "2026-02-12T10:30:00Z",
       "commit": "abc123f",
       "files": [
         { "path": "projects/site/com.inductiveautomation.perspective/views/MainView/view.json",
           "action": "modified",
           "content": "<base64 encoded>",
           "checksum": "sha256:..." }
       ]
     }
  5. Controller picks this up on next reconciliation
```

### File Sync & Transformation Approach (Go-Native)

The agent implements all file operations in Go using the standard library, eliminating external tool dependencies:

```go
// Pseudo-code: file synchronization using Go's filepath and io packages
func SyncDirectory(src, dst string, excludePatterns []string, protected []string) error {
  // Walk src, compute SHA256 for each file
  // Compare against dst checksums
  // Copy only changed files (delta sync)
  // Delete files in dst that are NOT in src AND NOT in protected list
  // Use github.com/bmatcuk/doublestar for ** glob matching in excludePatterns
  // Go's filepath.Match does NOT support ** — doublestar is required
}

// Pseudo-code: targeted JSON patching (NOT full re-serialization)
func NormalizeConfig(configPath string, rules map[string]string) error {
  // Read raw bytes
  // Parse JSON into map[string]interface{}
  // Apply field replacements (systemName, custom fields)
  // Use targeted patching: only modify the specific field values
  // Do NOT re-serialize the entire file — this would reorder keys,
  // change whitespace, and cause false diffs in checksum-based detection.
  // Instead, use byte-level replacement of field values.
}

// Pseudo-code: recursive config.json discovery
func NormalizeAllConfigs(stagingDir string, rules map[string]string) error {
  // filepath.Walk to find ALL files named config.json at any depth
  // Apply NormalizeConfig to each — not just top-level config.json
  // Ignition modules store their own config.json in nested directories
}

// Pseudo-code: YAML manipulation using gopkg.in/yaml.v2
func ParseYAML(yamlPath string) (map[string]interface{}, error) {
  // Structured YAML parsing for config files
}
```

Advantages over shell tools:
- **rsync-equivalent**: Delta sync with checksums, clean deletion, exclude patterns — all in-process
- **jq/yq-equivalent**: Structured JSON/YAML parsing safe from edge cases (nested quotes, multiline values, reordered keys)
- **Performance**: No subprocess overhead, no shell escaping issues
- **Portability**: Single static binary, works on any Linux distro
- **Debuggability**: Clear Go error messages, no cryptic rsync exit codes

Optional shell hooks can be used for advanced transformations: users drop a script in `/opt/ignition-sync/hooks/pre-sync.sh` or `post-sync.sh`, and the agent will execute them if present (shell scripts are opt-in, not mandatory).

---

## Storage Strategy

The operator creates one shared PVC per `IgnitionSync` CR. The **controller mounts the PVC read-write** (for git clone/fetch operations). All **sync agents mount the same PVC read-only** (they only read repo content, never write to it). Agent status reporting uses ConfigMaps, not PVC files.

### PVC Lifecycle

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ignition-sync-repo-proveit-sync
  namespace: site1
  ownerReferences:
    - apiVersion: sync.ignition.io/v1alpha1
      kind: IgnitionSync
      name: proveit-sync
      uid: ...
  labels:
    app.kubernetes.io/managed-by: ignition-sync-controller
    sync.ignition.io/cr-name: proveit-sync
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: ""         # from spec.storage.storageClassName
  resources:
    requests:
      storage: 1Gi             # from spec.storage.size
```

The PVC has an ownerReference to the `IgnitionSync` CR, so it gets garbage collected when the CR is deleted.

### Storage Class Compatibility

| Environment | Storage Class | RWX Support | Notes |
|---|---|---|---|
| AWS EKS | `efs-sc` (EFS CSI) | Yes | Recommended for multi-gateway |
| GCP GKE | `standard-rwx` (Filestore) | Yes | Filestore basic tier |
| Azure AKS | `azurefile-csi` | Yes | Azure Files SMB/NFS |
| On-prem | NFS provisioner, Longhorn, Rook-Ceph | Yes | Any NFS-backed class |
| Single gateway | Any (even `gp3`) | RWO sufficient | Only one pod mounts it |

For clusters **without RWX storage** (e.g., a single-gateway deployment on a minimal cluster), the operator falls back gracefully:

- If `spec.storage.accessMode` is `ReadWriteOnce`, the controller runs git operations inside the same pod as the agent (via an init container pattern) instead of a separate persistent clone. This is essentially the current git-sync model but with better tooling.
- The CRD validates that RWO mode is only used when there's a single gateway pod referencing the CR.

### Communication via ConfigMap

The operator uses K8s ConfigMaps as the sole communication mechanism between controller and agents:

**Controller → Agents: Metadata ConfigMap**
- Controller creates ConfigMap `ignition-sync-metadata-{crName}` with current commit, ref, and trigger timestamp
- Agents watch the ConfigMap via K8s informer (fast, event-driven)
- Fallback: agents poll ConfigMap at `spec.polling.interval` if informer watch is disrupted

**Agents → Controller: Status ConfigMap**
- Agents write sync results to ConfigMap `ignition-sync-status-{crName}`
- Controller reads agent status from ConfigMap during reconciliation
- Each agent writes its status as a key (`{gatewayName}`) in the ConfigMap data

**Bidirectional: Change Manifests ConfigMap**
- When bidirectional sync is enabled, agents write change manifests to ConfigMap `ignition-sync-changes-{crName}`
- Controller reads and processes change manifests during reconciliation

```
ConfigMaps per IgnitionSync CR:
  ignition-sync-metadata-{crName}    # Controller → Agents (commit, ref, trigger timestamp)
  ignition-sync-status-{crName}      # Agents → Controller (per-gateway sync results)
  ignition-sync-changes-{crName}     # Agents → Controller (bidirectional change manifests)

Shared PVC structure (repository content only — no signaling files):
/repo/
├── services/           # Actual repo content
│   ├── site/
│   └── area/
├── shared/
│   ├── scripts/
│   ├── udts/
│   └── config/
└── ...
```

**Why ConfigMap-only (no PVC file-based signaling):**
- Eliminates PVC write contention — PVC is mounted read-only by agents, read-write only by controller for git operations
- ConfigMap watch is faster than inotify (~instant vs ~100ms)
- Status is visible via standard K8s tools (`kubectl get configmap`)
- No orphaned status files if agents crash
- Controller-agent communication stays on well-understood K8s primitives

---

## Multi-Repo Support

A single controller instance handles any number of `IgnitionSync` CRs across any number of namespaces. Each CR references its own git repository and has its own shared PVC.

```
Namespace: site1
  IgnitionSync/proveit-sync  →  repo: conf-proveit26-app.git    → PVC: ignition-sync-repo-proveit-sync
  IgnitionSync/modules-sync  →  repo: ignition-custom-modules.git → PVC: ignition-sync-repo-modules-sync

Namespace: site2
  IgnitionSync/proveit-sync  →  repo: conf-proveit26-app.git    → PVC: ignition-sync-repo-proveit-sync

Namespace: public-demo
  IgnitionSync/demo-sync     →  repo: publicdemo-all.git        → PVC: ignition-sync-repo-demo-sync
```

Pods reference a specific CR via `ignition-sync.io/cr-name`, so there's no ambiguity when multiple CRs exist in the same namespace. A gateway pod always syncs from exactly one repository.

---

## Helm Chart

The operator ships as a standard Helm chart:

```
charts/ignition-sync/
├── Chart.yaml                    # type: application
├── values.yaml
├── crds/
│   └── sync.ignition.io_ignitionsyncs.yaml
├── templates/
│   ├── deployment-controller.yaml
│   ├── deployment-webhook.yaml
│   ├── service-controller.yaml
│   ├── service-webhook.yaml
│   ├── serviceaccount.yaml
│   ├── clusterrole.yaml
│   ├── clusterrolebinding.yaml
│   ├── mutatingwebhookconfiguration.yaml
│   ├── certificate.yaml          # cert-manager Certificate for webhook TLS
│   ├── networkpolicy.yaml        # Default NetworkPolicy for webhook ingress
│   ├── poddisruptionbudget.yaml  # PDB for webhook (minAvailable: 1)
│   └── _helpers.tpl
└── README.md
```

### Install

```bash
helm repo add ia https://charts.ia.io
helm install ignition-sync ia/ignition-sync \
  --namespace ignition-sync-system \
  --create-namespace
```

### Minimal values.yaml

```yaml
controller:
  replicas: 2
  image:
    repository: ghcr.io/inductiveautomation/ignition-sync-controller
    tag: "1.0.0"
  # Restrict to specific namespaces (empty = all)
  watchNamespaces: []
  # Restrict to CRs with specific labels
  watchLabelSelector: ""

webhook:
  replicas: 2
  image:
    repository: ghcr.io/inductiveautomation/ignition-sync-controller
    tag: "1.0.0"
  # cert-manager issuer for webhook TLS
  certManager:
    issuerRef:
      name: selfsigned-issuer
      kind: Issuer

agent:
  image:
    repository: ghcr.io/inductiveautomation/ignition-sync-agent
    tag: "1.0.0"

# Global defaults applied to all IgnitionSync CRs (overridable per CR)
defaults:
  storage:
    storageClassName: ""
    size: "1Gi"
  polling:
    interval: 60s
```

---

## Integration Patterns

### With ArgoCD (Post-Sync Notification)

Instead of the current git-sync polling loop, ArgoCD notifies the operator after syncing:

```yaml
# argocd-notifications ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-notifications-cm
data:
  trigger.on-sync-succeeded: |
    - when: app.status.operationState.phase in ['Succeeded']
      send: [ignition-sync-webhook]
  template.ignition-sync-webhook: |
    webhook:
      ignition-sync:
        method: POST
        path: /webhook/{{.app.metadata.namespace}}/{{.app.metadata.annotations.sync_ignition_io/cr-name}}
        body: |
          {"ref": "{{.app.metadata.annotations.git_ref}}"}
  service.webhook.ignition-sync:
    url: http://ignition-sync-controller.ignition-sync-system.svc:8443
    headers:
      - name: Content-Type
        value: application/json
```

### With Kargo (Promotion Step)

Kargo can trigger the sync operator via webhook as a promotion step, or update `spec.git.ref` via its git-update step (which ArgoCD then reconciles):

```yaml
apiVersion: kargo.akuity.io/v1alpha1
kind: Stage
metadata:
  name: dev
spec:
  promotionTemplate:
    spec:
      steps:
        # Standard: update values file for ArgoCD
        - uses: git-update
          config:
            path: values/site-common/dev/values.yaml
            updates:
              - key: git.ref
                value: ${{ freight.commits[0].tag }}

        # New: directly update the IgnitionSync CR
        - uses: http
          config:
            method: POST
            url: http://ignition-sync-controller.ignition-sync-system.svc:8443/webhook/site1/proveit-sync
            body: '{"ref": "${{ freight.commits[0].tag }}"}'
```

### With GitHub Webhooks (Tag Push)

For simpler setups without ArgoCD/Kargo, a GitHub webhook can trigger sync directly on tag push:

```
GitHub repo settings → Webhooks → Add webhook
  Payload URL: https://sync.example.com/webhook/site1/my-sync
  Content type: application/json
  Secret: (HMAC key matching spec.webhook.secretRef)
  Events: Releases, Tags
```

### With the Ignition Helm Chart

The `ignition` chart at `charts.ia.io` doesn't need any changes. Users add annotations via the existing `podAnnotations` passthrough:

```yaml
# In any umbrella chart using the ignition chart
myGateway:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "my-sync"
      ignition-sync.io/service-path: "services/gateway"
```

The mutating webhook handles the rest. If the ignition chart later adds first-class `gitSync` values, it can generate these annotations from a friendlier schema — but the webhook-based approach means **any version of the ignition chart works today**.

---

## Worked Examples

### ProveIt 2026 (5 gateways, 1 site + 4 areas)

```yaml
# IgnitionSync CR
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: proveit-sync
  namespace: site1
spec:
  git:
    repo: "git@github.com:inductive-automation/conf-proveit26-app.git"
    ref: "2.0.0"
    auth:
      sshKey:
        secretRef:
          name: git-sync-secret
          key: ssh-privatekey
  storage:
    storageClassName: efs-sc
    size: 1Gi
  webhook:
    enabled: true
    port: 8443
    secretRef:
      name: sync-webhook-secret
      key: hmac-key
  polling:
    interval: 60s
  gateway:
    port: 8043
    tls: true
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
  siteNumber: "1"
  shared:
    externalResources:
      enabled: true
      source: "shared/ignition-gateway/config/resources/external"
      createFallback: true
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
  excludePatterns:
    - "**/.git/"
    - "**/.gitkeep"
  normalize:
    systemName: true
  bidirectional:
    enabled: false
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
```

```yaml
# Site chart values.yaml — annotations replace all git-sync ConfigMaps/scripts
site:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/site"
      ignition-sync.io/deployment-mode: "prd-cloud"
      ignition-sync.io/tag-provider: "default"
      ignition-sync.io/system-name-template: "site{{.SiteNumber}}"
      ignition-sync.io/exclude-patterns: "**/tag-*/MQTT Engine/,**/tag-*/System/"

area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/area"
      ignition-sync.io/tag-provider: "edge"
      ignition-sync.io/system-name-template: "site{{.SiteNumber}}-{{.GatewayName}}"

# area2, area3, area4 — identical to area1
```

What this replaces in the current site chart:
- `x-git-sync` init container anchor and all `initContainers` blocks
- `x-common` volumes anchor (git-secret, git-volume, sync-scripts, git-config)
- `git-sync-env-*` ConfigMaps (all 5)
- `sync-scripts` ConfigMap
- `git-sync-target-ref` ConfigMap
- `sync-files.sh` and `sync-entrypoint.sh` scripts
- `git-sync-configmap.yaml` template

### Public Demo (2 gateways, replicated frontend)

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: demo-sync
  namespace: public-demo
spec:
  git:
    repo: "git@github.com:inductive-automation/publicdemo-all.git"
    ref: "main"
    auth:
      sshKey:
        secretRef:
          name: git-sync-secret
          key: ssh-privatekey
  storage:
    storageClassName: efs-sc
    size: 1Gi
  polling:
    interval: 30s
  gateway:
    port: 8043
    tls: true
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
  shared:
    scripts:
      enabled: false
    udts:
      enabled: false
    externalResources:
      enabled: false
  normalize:
    systemName: false
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
```

```yaml
# Public demo chart values
frontend:
  gateway:
    replicas: 5
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "demo-sync"
      ignition-sync.io/service-path: "services/ignition-frontend"

backend:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "demo-sync"
      ignition-sync.io/service-path: "services/ignition-backend"
```

Note: With 5 frontend replicas, all 5 pods get the sync agent injected, all mount the same shared repo PVC read-only, and all sync from the same service path. The controller discovers all 5 and tracks each in `status.discoveredGateways`.

### Simple Single Gateway

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: my-sync
  namespace: default
spec:
  git:
    repo: "https://github.com/myorg/my-ignition-app.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token
          key: token
  storage:
    storageClassName: ""     # cluster default
    size: 500Mi
    accessMode: ReadWriteOnce  # only one gateway, RWO is fine
  polling:
    interval: 30s
  gateway:
    port: 8043
    tls: false
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
```

```yaml
# Simple ignition chart values
ignition:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "my-sync"
      ignition-sync.io/service-path: "."   # repo root IS the service
```

---

## Ignition-Aware Sync

The operator embeds deep knowledge of Ignition architecture to make sync safer and smarter.

### Gateway Health & Readiness

**Pre-Sync Health Check**
- Before syncing, agent checks gateway health: `GET /data/api/v1/status`
- Waits up to 5 seconds for gateway to become responsive
- If gateway is down or not ready, agent delays sync and retries (backoff: 5s, 10s, 30s, 60s)
- If gateway remains unavailable after 5 retries, agent logs warning but continues (prevents indefinite stall)

**Designer Session Detection**
- Agent queries `GET /data/api/v2/projects/{projectName}` to check if active design sessions exist
- If Designer is actively editing, agent can:
  - Option A: Wait for session to close (configurable timeout)
  - Option B: Proceed with sync and reload projects (may disconnect Designer)
  - Option C: Fail the sync with condition "DesignerSessionActive"
- User chooses behavior via `spec.ignition.designerSessionPolicy` (wait | proceed | fail)

**Scan API Semantics (Fire-and-Forget)**
- Ignition's scan API is fire-and-forget: `POST /data/api/v1/scan/projects` returns HTTP 200 immediately, confirming the scan was queued. The actual scan runs asynchronously inside the gateway's Java runtime.
- **Do not poll for scan completion** — there is no reliable `scan/status` endpoint in current Ignition versions. Accept 2xx as success.
- Order matters: `scan/projects` MUST be called before `scan/config`
- Agent uses HTTP retry logic (3 retries, exponential backoff) for connection failures
- **Initial sync exception:** On first sync after pod startup, skip scan API calls entirely. Ignition auto-scans its data directory on first boot. Calling the scan API during startup causes race conditions.
- If scan call returns non-2xx after retries, agent reports error condition with reason

### Module Awareness

**Installed Module Detection**
```
Agent queries GET /data/api/v2/modules to detect:
- MQTT Engine (tag provider exclusion logic)
- Sepasoft MES (reporting considerations)
- Transmission (schedule handling)
- Custom modules in user-lib/modules/
```

**MQTT-Managed Tag Providers**
- If MQTT Engine is installed, agent auto-detects MQTT-managed tag providers
- Excludes these from UDT sync (MQTT manages them dynamically)
- Annotation: `ignition-sync.io/exclude-tag-providers: "mqtt-engine,custom-mqtt"`
- Prevents conflicts between git-synced and MQTT-synced tags

**Module JAR Installation Support**
- If `spec.shared.modules` is configured, agent syncs JAR files to `user-lib/modules/`
- Modules are installed in order (dependencies first)
- After sync, agent triggers `POST /data/api/v1/restart` if new modules detected
- Gateway restarts with new modules loaded

### Tag Provider Hierarchy & Inheritance

**Ignition Tag Model**
```
HQ Gateway
├── Default Tag Provider (system-level)
│   └── All sites inherit from this
│
Site Gateway
├── Site Tag Provider (derives from HQ default)
│   └── All areas inherit from this
│
Area Gateway
├── Area Tag Provider (derives from site)
│   └── Leaf level (no children)
```

**Tag Inheritance Strategy Annotation**
```yaml
ignition-sync.io/tag-inheritance-strategy: "full"  # or "leaf-only"
```

- `full`: Sync all UDTs; assume this gateway is responsible for the full tag hierarchy
- `leaf-only`: Sync only leaf-level UDTs; skip inherited tags from parent gateways

Agent behavior:
- Query gateway tag provider hierarchy via API: `GET /data/api/v2/tag-providers`
- Detect parent/child relationships (site → area)
- If `leaf-only`, exclude UDTs that exist in parent gateway tag provider
- Prevents duplicate definitions and inheritance conflicts

**Tag Provider Normalization**
- Config files may hardcode tag provider names (e.g., "default", "mqtt-engine")
- Agent normalizes via annotation: `ignition-sync.io/tag-provider: "site-tags"`
- Replaces provider references in config.json from git default to gateway-specific provider

### Redundancy & Primary/Backup Coordination

**Primary/Backup Detection**
```yaml
ignition-sync.io/redundancy-role: "primary"  # or "backup"
ignition-sync.io/peer-gateway-name: "site2-backup"
```

- If backup, agent reads last-synced commit from primary gateway (via API)
- Backup waits for primary to sync before starting its own sync
- Prevents out-of-sync primaries and backups

**Sync Ordering for Redundancy**
- Primary syncs first, waits for scan completion
- Backup queries primary's last successful sync via API
- If backup's commit differs from primary's, backup syncs
- If same, backup skips sync (already in sync via replication)

**Failover Handling**
- If primary is down, backup automatically starts syncing
- Agent detects primary unavailability: `GET /data/api/v1/status` returns error
- After 2 failed health checks, backup promotes itself and begins independent syncing
- Logs failover event with severity "warning"

### Perspective Session Management

**Graceful Session Closure Before Sync**
```yaml
spec:
  ignition:
    perspectiveSessionPolicy: "graceful"  # or "immediate" or "none"
```

- `graceful`: Send close message to all connected Perspective clients; wait 10s for disconnect
- `immediate`: Force close all sessions (may appear as disconnect to users)
- `none`: Proceed with sync without closing sessions (users may see stale data briefly)

**Session Tracking**
- Agent queries `GET /data/api/v2/sessions` before sync
- Logs active session count and duration
- If sync disrupts sessions, logs which modules were affected

### Config Normalization Beyond systemName

**Advanced Field Mapping with Go Templates**

```yaml
spec:
  normalize:
    systemName: true
    # Template-based normalization
    templates:
      - jsonPath: ".gateways[0].hostname"
        valueTemplate: "ignition-{{.Namespace}}-{{.GatewayName}}.local"
      - jsonPath: ".defaultTimeZone"
        valueTemplate: "America/Chicago"  # Static, or {{.TimeZone}} from annotation
      - jsonPath: ".locale"
        valueTemplate: "{{.Locale}}"
      - jsonPath: ".instanceIdentifier"
        valueTemplate: "{{.SiteNumber}}-{{.GatewayName}}"
```

Context variables available to templates:
- `{{.Namespace}}` — pod's namespace
- `{{.GatewayName}}` — pod label `app.kubernetes.io/name`
- `{{.SiteNumber}}` — from CR spec.siteNumber
- `{{.ClusterName}}` — K8s cluster name (from kubeconfig context)
- `{{.Timestamp}}` — Unix timestamp of sync

**YAML Config Normalization**
- If YAML config files exist, agent can apply similar templates
- Uses Go's `text/template` package for consistency (same syntax as systemName template)

### Protection of .resources/ Directory

**Critical Safety Guardrail**
```
/ignition-data/
├── .resources/               ← NEVER SYNC THIS
│   ├── ...runtime caches...
│   └── ...temporary files...
├── config/                   ← Git-managed
├── projects/                 ← Git-managed
└── ...
```

The `.resources/` directory contains:
- Runtime caches (perspective resources compiled by gateway)
- Temporary files generated during operation
- State that should NEVER be version controlled or synced

**Agent Safeguards**

1. **Mandatory Exclude Pattern** — `.resources/` is always in the exclude list, enforced by the agent regardless of CRD config:
   ```yaml
   excludePatterns:
     - "**/.resources/**"  # Enforced by agent even if user omits it
   ```
   If missing from CRD, agent adds it automatically and warns in logs.

2. **Pre-Sync Check** — Agent verifies staging directory doesn't contain `.resources/`
   ```
   If staging/.resources/ exists → ERROR
   Fail sync with reason "StagingDirectoryContainsRuntimeFiles"
   ```

3. **Selective Merge (NOT Atomic Swap)** — The sync uses a merge strategy, not a directory swap:
   ```go
   // Walk staging/, copy each file to /ignition-data/
   // Then walk /ignition-data/ and delete files that:
   //   a) Do NOT exist in staging, AND
   //   b) Are NOT in the protected list (.resources/)
   // .resources/ is NEVER touched — not deleted, not overwritten, not listed
   ```
   A literal `mv staging/ /ignition-data/` would destroy `.resources/`. The merge approach
   preserves the directory entirely while replacing all git-managed content.

4. **Documentation** — Helm chart includes prominent note in README:
   ```
   CRITICAL: Never add .resources/ to git. It contains runtime-generated files
   that will cause conflicts and data corruption. The operator automatically
   prevents this, but manual git adds will corrupt your sync.
   ```

5. **Bidirectional Exclusion** — When watching for gateway changes (bidirectional mode), exclude `.resources/` entirely:
   - Agent's inotify watch excludes this directory
   - Changes in `.resources/` are never captured as "gateway changes" for PR creation

---

## Deployment Safety & Rollback

Safe, observable deployments are critical for production gateways. The operator provides multiple validation and rollback mechanisms.

### Pre-Sync Validation

**Dry-Run Mode**
```yaml
spec:
  validation:
    dryRunBefore: true  # Default: false
```

When enabled:
- Agent performs a dry-run copy without actually modifying `/ignition-data/`
- Reports what files would change, but doesn't apply them
- Useful before major updates (test in parallel gateway, then promote)
- Logs show "DRY_RUN: Would have changed {count} files"

**JSON Syntax Validation**
- Before touching any config.json, agent validates JSON syntax
- If invalid, sync fails with condition "ConfigSyntaxError"
- Prevents corrupted configs from reaching the gateway

**Pre-Sync Webhook (Optional Custom Validation)**
```yaml
spec:
  validation:
    webhook:
      url: "https://validate.example.com/ignition-sync"
      timeout: 10s
```

- Optional user-provided webhook for custom validation logic
- Receives request with: commit SHA, ref, list of changed files, gateway name
- Webhook can respond with approval or rejection
- Useful for: custom compliance checks, mandatory review gates, policy enforcement

### Pre-Sync Snapshots & Instant Rollback

**Snapshot Capture**
```yaml
spec:
  snapshots:
    enabled: true
    retentionCount: 5  # Keep last 5 snapshots per gateway
    storage:
      type: "pvc"  # or "s3", "gcs"
      s3:
        bucket: "my-ignition-backups"
        keyPrefix: "ignition-sync/"
```

Before every sync:
1. Agent creates tarball of entire `/ignition-data/` directory
2. Snapshots named: `{gatewayName}-{timestamp}.tar.gz` (e.g., `site-20260212-103005.tar.gz`)
3. Stored on PVC (local) or object storage (S3, GCS, Azure Blob)
4. Retention policy enforced: keep last N snapshots, delete older ones
5. Size reported to status: `"lastSnapshot": {"size": "256MB", "timestamp": "..."}`

**Instant Rollback**
```bash
# CLI tool or webhook endpoint
kubectl patch ignitionsync proveit-sync -n site1 -p \
  '{"spec":{"rollback":{"toSnapshot":"site-20260212-102000.tar.gz"}}}'
```

- Agent detects rollback request via CR status change
- Restores `/ignition-data/` from snapshot
- Triggers Ignition scan API to reload configs
- Rolls back takes ~5-30 seconds depending on snapshot size
- Logs rollback event with reason

### Canary Sync

**Staged Rollout**
```yaml
spec:
  deployment:
    strategy: "canary"
    stages:
      - name: "dev"
        gateways: ["dev-gateway"]
        postSyncWait: 30s
        healthCheckUrl: "GET https://dev-gateway:8043/status"
      - name: "staging"
        gateways: ["stage1", "stage2"]
        postSyncWait: 60s
      - name: "production"
        gateways: ["site", "area1", "area2", "area3", "area4"]
        postSyncWait: 120s
        requireApproval: true
```

Canary sync flow:
1. Sync stage 1 (dev gateway)
2. Wait 30s, check health endpoint
3. If health check fails: STOP, alert operators
4. If health check passes: proceed to stage 2
5. Repeat for each stage
6. If requireApproval: production stage waits for manual approval before starting

**Health Check Semantics**
- Agent performs HTTP GET to healthCheckUrl after sync
- Expects 200 response within 10s
- If failure: condition "CanaryStageFailed" with details
- Operators can investigate failed stage, fix root cause, then manually retrigger

### Auto-Rollback on Failure

**Scan API Failure Detection**
```yaml
spec:
  autoRollback:
    enabled: true
    triggers:
      - "scanFailure"
      - "projectLoadError"
      - "configError"
```

If post-sync scan fails:
1. Agent detects error from Ignition API response
2. Compares scan result against baseline (expected project count, config count)
3. If mismatch: restore from snapshot, logs "ScanFailureAutoRollback"
4. Notifies controller, which reports condition "AutoRollbackPerformed"

**Drift Detection**
- After successful sync, agent can periodically verify gateway is in expected state
- Compares file checksums on gateway against expected checksums
- If drift detected: logs warning "GatewayFilesModified" (may indicate manual changes or corruption)

### Sync History & Diff Reporting

**Per-Gateway Sync History**
```yaml
status:
  discoveredGateways:
    - name: site
      syncedCommit: "abc123f"
      syncedRef: "2.0.0"
      lastSyncTime: "2026-02-12T10:30:05Z"
      lastSyncDuration: "3.2s"
      agentVersion: "1.0.0"
      syncHistory:
        - timestamp: "2026-02-12T10:30:05Z"
          commit: "abc123f"
          ref: "2.0.0"
          filesChanged: 47
          projectsSynced: ["site", "area1"]
          duration: "3.2s"
          result: "success"
          snapshotId: "site-20260212-102959.tar.gz"
        - timestamp: "2026-02-12T10:20:00Z"
          # ... previous sync ...
```

**Sync Diff Report**
- Agent records which files changed between syncs
- Report stored in `/repo/.sync-status/{gatewayName}-diff-{timestamp}.json`:
  ```json
  {
    "fromCommit": "previous-abc",
    "toCommit": "abc123f",
    "filesAdded": 12,
    "filesModified": 47,
    "filesDeleted": 3,
    "projectsAffected": ["site", "area1"],
    "changes": [
      {
        "path": "projects/site/com.inductiveautomation.perspective/views/MainView/view.json",
        "action": "modified",
        "checksum": {"before": "sha256:...", "after": "sha256:..."}
      }
    ]
  }
  ```
- Controller aggregates into CR status (last 10 syncs per gateway)

### Dependency-Aware Sync Ordering

**Gateway Dependency Graph**
```yaml
spec:
  deployment:
    syncOrder:
      - name: "site"
        weight: 100  # Sync first
      - name: "area1"
        weight: 80
        dependsOn: ["site"]  # Wait for site to complete
      - name: "area2"
        weight: 80
        dependsOn: ["site"]
      - name: "area3"
        weight: 80
        dependsOn: ["site"]
```

Sync controller orchestrates:
1. Sync all weight-100 gateways in parallel
2. Wait for completion
3. Sync all weight-80 gateways (all depend on site), in parallel
4. Wait for completion
5. Proceed to next weight tier

Benefits:
- Respects tag provider hierarchy (HQ before sites, sites before areas)
- Parallelizes where possible (all areas can sync simultaneously after site)
- Prevents transient conflicts (child gateways sync after parent)
- Can model custom dependencies (e.g., area1 depends on area2 for shared resources)

---

## Observability

### Metrics (Prometheus)

The controller exposes `/metrics` on port 8080:

| Metric | Type | Description |
|---|---|---|
| `ignition_sync_reconcile_total` | Counter | Total reconciliations by CR and result |
| `ignition_sync_reconcile_duration_seconds` | Histogram | Time per reconciliation |
| `ignition_sync_git_fetch_duration_seconds` | Histogram | Time for git fetch operations |
| `ignition_sync_webhook_received_total` | Counter | Webhooks received by source type |
| `ignition_sync_gateways_discovered` | Gauge | Number of gateways per CR |
| `ignition_sync_gateways_synced` | Gauge | Number of synced gateways per CR |
| `ignition_sync_last_sync_timestamp` | Gauge | Unix timestamp of last successful sync |
| `ignition_sync_sync_duration_seconds` | Histogram | Time to complete sync per gateway |
| `ignition_sync_files_changed_total` | Counter | Total files changed per CR |
| `ignition_sync_bidirectional_prs_created` | Counter | PRs created for gateway changes |
| `ignition_sync_scan_api_duration_seconds` | Histogram | Time for Ignition scan API completion |
| `ignition_sync_rollback_triggered_total` | Counter | Number of auto-rollbacks performed |

### Events

The controller emits Kubernetes Events on the IgnitionSync CR:

```
Normal   RepoCloned      IgnitionSync/proveit-sync   Cloned git@github.com:.../conf-proveit26-app.git at ref 2.0.0
Normal   RefUpdated       IgnitionSync/proveit-sync   Updated ref from 1.9.0 to 2.0.0 (via webhook)
Normal   SyncCompleted    IgnitionSync/proveit-sync   All 5 gateways synced successfully
Warning  SyncFailed       IgnitionSync/proveit-sync   Gateway area2 failed to sync: rsync error code 23
Normal   PRCreated        IgnitionSync/proveit-sync   Created PR #42 for gateway changes on site
Warning  ConflictDetected IgnitionSync/proveit-sync   File config.json modified in both git and gateway site
```

### kubectl Integration

```bash
# List all syncs across the cluster
kubectl get ignitionsyncs -A

# NAMESPACE     NAME            REF     GATEWAYS   SYNCED   AGE
# site1         proveit-sync    2.0.0   5          5        2d
# site2         proveit-sync    2.0.0   5          5        2d
# public-demo   demo-sync       main    6          6        30d

# Describe for detailed status
kubectl describe ignitionsync proveit-sync -n site1

# Quick status check
kubectl get ignitionsync proveit-sync -n site1 -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# True
```

**Slack/PagerDuty Alerting Integration**
```yaml
spec:
  alerting:
    enabled: true
    webhooks:
      - type: slack
        url: https://hooks.slack.com/services/...
        on: ["SyncFailed", "ScanFailure", "AutoRollback"]
      - type: pagerduty
        integrationKey: "..."
        on: ["SyncFailed"]
```

- Controller sends webhook notifications on sync events
- Operators can react quickly to failures
- Integrates with on-call rotations

### Reference Grafana Dashboard

Helm chart includes a ConfigMap with Grafana dashboard JSON:
```yaml
kind: ConfigMap
metadata:
  name: ignition-sync-grafana-dashboard
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "Ignition Sync Operator",
        "panels": [
          {
            "title": "Sync Status by Gateway",
            "targets": [{"expr": "ignition_sync_gateways_synced / ignition_sync_gateways_discovered"}]
          },
          {
            "title": "Sync Duration Trend",
            "targets": [{"expr": "ignition_sync_sync_duration_seconds"}]
          },
          {
            "title": "Webhook Received Rate",
            "targets": [{"expr": "rate(ignition_sync_webhook_received_total[5m])"}]
          }
        ]
      }
    }
```

Users can import this dashboard directly into their Grafana instance.

### Sync Diff Reports

Agent generates structured diff reports after each sync:
```bash
# Find all diffs for a gateway
kubectl get igs proveit-sync -n site1 -o json | \
  jq '.status.discoveredGateways[] | select(.name=="site")'

# Output includes:
# - Files changed count
# - Projects affected
# - Last diff report timestamp
# - Can fetch detailed diff from /repo/.sync-status/{gatewayName}-diff-{timestamp}.json
```

The CRD includes `additionalPrinterColumns` for the kubectl table output:

```yaml
additionalPrinterColumns:
  - name: Ref
    type: string
    jsonPath: .spec.git.ref
  - name: Gateways
    type: string
    jsonPath: .status.conditions[?(@.type=="AllGatewaysSynced")].message
    description: "Gateway sync status (e.g., '4 of 5 synced')"
  - name: Synced
    type: string
    jsonPath: .status.conditions[?(@.type=="AllGatewaysSynced")].status
  - name: LastSync
    type: date
    jsonPath: .status.lastSyncTime
  - name: Ready
    type: string
    jsonPath: .status.conditions[?(@.type=="Ready")].status
  - name: Age
    type: date
    jsonPath: .metadata.creationTimestamp
```

---

## Scale Considerations

### Controller Performance

| CRs | Gateways | Configuration |
|-----|----------|---------------|
| 1-10 | 1-50 | Default settings (MaxConcurrentReconciles: 5) |
| 10-50 | 50-200 | Increase MaxConcurrentReconciles to 10-20, consider dedicated nodes |
| 50-100 | 200-500 | Use `--watch-namespaces` to limit scope, increase controller memory |
| 100+ | 500+ | Controller sharding (v1.1), dedicated controller per namespace group |

### go-git Memory

go-git (pure Go git library) loads objects into memory. For repos under 500MB, this is fine. For large repos (50+ gateways with frequent changes), memory usage can spike to 2-4x repo size during fetch operations. Mitigations:

- Set `resources.limits.memory` on the controller appropriately (512Mi for small repos, 2Gi+ for large)
- v1.1 will add optional native `git` CLI backend for memory-constrained environments
- v1.1 will add shared git clone cache for CRs referencing the same repo

### Extension Points (v1)

The operator is designed with future extensibility in mind. While these interfaces are not public in v1, the internal architecture separates concerns for later extraction:

- **Source interface** — git is the only implementation in v1, but the internal `Source` interface abstracts fetch/checkout operations. v1.1+ may add OCI registry and S3 sources.
- **Sync strategy interface** — the merge-based sync is one strategy. The interface allows alternative strategies (copy-on-write, bind mount) for specialized environments.
- **Notification interface** — webhook receiver is one trigger. The interface allows additional triggers (NATS, MQTT, AWS SNS) without controller changes.

---

## Enterprise & Scale

### Multi-Cluster Federation (Future)

While v1 focuses on single-cluster operation, the architecture is designed for future multi-cluster coordination:

```
Central Control Plane (e.g., dedicated cluster)
  └─ IgnitionSyncFederation CR
     └─ Specifies: 3x Production clusters, 2x DR clusters

Per Cluster (managed by local controller)
  └─ IgnitionSync CRs (watch for federation updates)
  └─ Local discovery of gateways
```

Design principle: Federation is opt-in. Single-cluster deployments work identically without federation.

### Air-Gapped Deployments

For environments without internet access:

**Local Git Mirror**
```yaml
spec:
  git:
    repo: "file:///mnt/git-mirror/conf-proveit26-app.git"  # Local mirror

    # Or: SSH to internal git server
    repo: "git@git-internal.local:inductive-automation/conf-proveit26-app.git"
```

Controller supports:
- File-based git repos (mirrored from external)
- Internal git servers (no internet required)
- Offline mode: no GitHub API calls for PR creation (use git push instead)

**Internal Container Registry**
```yaml
spec:
  agent:
    image:
      repository: git-internal.local:5000/ignition-sync-agent
      tag: "1.0.0"
      digest: "sha256:..."  # Pinned digest required in air-gap
```

**GPG-Signed Commits (Future)**
```yaml
spec:
  git:
    auth:
      gpgKey:
        secretRef:
          name: git-gpg-key
          key: publicKey
    # Agent verifies commits are signed before syncing
```

### Config Inheritance & Reusability

For deployments with 100+ gateways across multiple sites, reducing duplication is critical:

**Base CR Pattern**
```yaml
# Base configuration shared across sites
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSyncBase
metadata:
  name: proveit-base
  namespace: default
spec:
  shared:
    externalResources:
      enabled: true
      source: "shared/ignition-gateway/config/resources/external"
    scripts:
      enabled: true
      source: "shared/scripts"
    udts:
      enabled: true
      source: "shared/udts"
  excludePatterns:
    - "**/.git/"
    - "**/.resources/**"
  normalize:
    systemName: true
    templates:
      - jsonPath: ".defaultTimeZone"
        valueTemplate: "America/Chicago"

---
# Site-specific CR inherits from base
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: proveit-sync
  namespace: site1
spec:
  inheritsFrom:
    name: proveit-base
    namespace: default

  # Override or supplement base config
  git:
    repo: "git@github.com:inductive-automation/conf-proveit26-app.git"
    ref: "2.0.0"

  # Additional files specific to this site (merged with base)
  additionalFiles:
    - source: "shared/site-specific/site1.json"
      dest: "site1.json"
```

**Template CRD (Alternative)**
```yaml
# Kyverno or CEL-based template validation
apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sIgnitionSyncDefaults
metadata:
  name: apply-defaults
spec:
  match:
    kinds:
      - apiGroups: ["sync.ignition.io"]
        kinds: ["IgnitionSync"]
  parameters:
    excludePatterns:
      - "**/.git/"
      - "**/.resources/**"
```

### Rate Limiting & Concurrency Control

For large clusters with many gateways syncing simultaneously:

```yaml
spec:
  rateLimit:
    maxConcurrentSyncs: 5  # Default: number of gateways / 2
    maxSyncsPerMinute: 20
    burstLimit: 10         # Allow short bursts
```

Controller behavior:
- Never sync more than 5 gateways simultaneously (respect cluster load)
- Queue excess gateways; process in FIFO order
- If sync queue grows, emit warning condition
- Metrics track queue depth and throughput

### Approval Workflows

For production environments requiring change control:

```yaml
spec:
  approval:
    required: true
    timeout: 24h  # PR expires if not approved within 24h
    teams: ["platform-team"]  # GitHub teams that can approve

approval:
  webhook:
    url: "https://approval.example.com/approve"  # Custom approval system
```

**Approval Flow**
1. Webhook triggers ref update on CR
2. Controller detects new ref, creates temporary "PendingApproval" condition
3. Sync is paused until approval is granted
4. On approval: `kubectl annotate ignitionsync proveit-sync approved-by=alice approved-at="$(date -Iseconds)"`
5. Controller detects approval annotation, proceeds with sync

---

## Developer Experience

### Local Development Mode

CLI tool for live-syncing local git repo to dev gateway:

```bash
ignition-sync dev watch \
  --repo=/path/to/local/conf \
  --gateway=dev-gateway.local:8043 \
  --api-key=$IGNITION_API_KEY \
  --watch-path="services/gateway"
```

Continuously:
1. Watches local git for file changes
2. Performs incremental sync to dev gateway (no git operations, direct FS copy)
3. Triggers Ignition scan API
4. Reports errors in real-time

Useful for rapid iteration during development.

### Migration Tool

Auto-generate IgnitionSync CRs from existing git-sync ConfigMaps:

```bash
ignition-sync migrate \
  --from-configmap git-sync-env-site \
  --namespace site1 \
  --output proveit-sync-cr.yaml
```

Generates:
```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: proveit-sync
  namespace: site1
spec:
  git:
    repo: "..."
    auth:
      sshKey: ...  # Maps to existing secret
  storage:
    storageClassName: "..."
  # ... rest of spec derived from ConfigMap ...
```

### Decision Matrix: git-sync vs Operator vs Custom

| Scenario | Recommendation | Rationale |
|---|---|---|
| Single gateway, simple setup | Operator | Simpler than git-sync, no sidecar complexity |
| 5+ gateways, one repo | Operator | Multi-gateway discovery and status tracking |
| Custom sync logic (hooks, normalization) | Operator + hooks | Native support for pre/post-sync scripts |
| Air-gapped environment | Operator | File-based git mirrors supported |
| 100+ gateways, extreme scale | Operator + federation | Designed for scale |
| Ignition Module development | Custom | Need raw control over sync timing |

---

## Testing Strategy

### Unit Tests

- Go unit tests for all controller logic, webhook mutation, status calculation
- Table-driven tests for webhook payload parsing (ArgoCD, Kargo, GitHub, generic)
- Template tests for annotation parsing and env var generation

### Integration Tests (envtest)

- Controller reconciliation with fake CRs and pods
- Webhook injection with sample pod specs
- PVC creation and ownership
- Status condition transitions
- Multi-CR, multi-namespace scenarios

### End-to-End Tests

- Kind or k3d cluster with cert-manager
- Deploy operator, create CR, create annotated pods
- Verify sidecar injection, PVC creation, git clone, file sync
- Trigger webhook, verify ref update and sync propagation
- Bi-directional: modify gateway files, verify PR creation
- Upgrade/downgrade: CRD version migration

### Sync Agent Tests

- Go unit tests for file sync logic (delta sync, checksums, exclude patterns)
- Doublestar glob matching (** patterns against real directory trees)
- Config normalization: recursive config.json discovery, targeted JSON patching
- Selective merge integrity (interrupted sync recovery, .resources/ preservation)
- ConfigMap watch and status reporting
- inotify watcher for bi-directional
- Integration test: full sync flow against mock Ignition API

---

## Migration Path from Current git-sync

For existing deployments using the current git-sync sidecar pattern:

1. **Install the operator** — `helm install ignition-sync ia/ignition-sync`
2. **Create the `IgnitionSync` CR** in each namespace — maps directly from current values
3. **Add annotations** to existing gateway pods (via values.yaml update)
4. **Remove old git-sync configuration** — init containers, ConfigMaps, scripts, volumes
5. **Deploy** — ArgoCD syncs the changes; pods restart with injected sync agents instead of git-sync sidecars

The CR's `spec` fields map almost 1:1 to the current values.yaml git sync configuration. The migration is a values.yaml diff, not a rewrite.

**Current:**
```yaml
# 77 lines of YAML anchors, git-sync init containers, ConfigMap refs
x-git-sync:
  initContainer: &git-sync-init-container
    name: git-sync
    image: registry.k8s.io/git-sync/git-sync:v4.4.0
    # ... 30 lines ...
site:
  gateway:
    initContainers:
      - <<: *git-sync-init-container
        envFrom:
          - configMapRef:
              name: git-sync-env-site
    volumes: *common-volumes
    # ... plus 5 ConfigMap templates, 2 shell scripts ...
```

**After:**
```yaml
# 3 annotations per gateway, 1 IgnitionSync CR
site:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/service-path: "services/site"
```

---

## Security Architecture

### Supply Chain Security

**Container Image Signing & Verification**
- All operator images (controller, webhook, agent) are signed with Cosign (keyless signing via OIDC)
- Images include Software Bill of Materials (SBOM) in SPDX format
- Weekly vulnerability scanning via Trivy; critical CVEs trigger immediate rebuild
- Helm chart enforces pinned image digests (SHA256), not mutable tags
- Public key for signature verification published alongside release artifacts

Example Helm values enforcing digest pinning:
```yaml
controller:
  image:
    repository: ghcr.io/inductiveautomation/ignition-sync-controller
    tag: "1.0.0"
    digest: "sha256:abc123f..."  # Prevents tag mutability attacks
```

### Secrets Management

**Never Export Secrets to Environment**
- Git auth keys and Ignition API keys are mounted as volumes, never injected as env vars
- Prevents accidental secret leakage via container logs, crash dumps, or exec history

**External Secret Manager Integration**
- Support for HashiCorp Vault, AWS Secrets Manager, Azure Key Vault via external-secrets operator
- Example: Controller reads git key from Vault at reconciliation time, never storing it locally
- Secret rotation policy: operator can be configured to re-read secrets every N minutes
- Read-on-demand pattern: agent reads API key from mounted volume at sync time, discards after use

**Secret Rotation**
- Controller watches for Secret updates; if a referenced secret is modified, immediately reconcile
- No caching of secrets — always read fresh at reconciliation time
- Helm chart includes guidance on secret rotation lifecycle

### Network Security

**NetworkPolicy Examples for Restricted Environments**

```yaml
# Deny all ingress by default
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ignition-sync-default-deny
spec:
  podSelector: {}
  policyTypes:
    - Ingress

---
# Allow API server → webhook (for mutation requests)
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: webhook-allow-apiserver
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: ignition-sync-webhook
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector: {}  # From API server
      ports:
        - protocol: TCP
          port: 9443

---
# Allow controller → git remotes (egress)
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: controller-allow-egress
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: ignition-sync-controller
  policyTypes:
    - Egress
  egress:
    - to:
        - namespaceSelector: {}  # Any namespace (for local git servers)
    - to:
        - podSelector: {}  # Any pod (for external git remotes)
      ports:
        - protocol: TCP
          port: 22   # SSH for git
        - protocol: TCP
          port: 443  # HTTPS for git and GitHub API

---
# Webhook must be separate deployment (security isolation)
# Failures in webhook do not affect controller
# Agent failures do not affect webhook
```

**Webhook Receiver Security**
- HMAC validation uses `crypto/subtle.ConstantTimeCompare` to prevent timing oracle attacks
- HMAC is validated **before** any CR lookup to prevent namespace/CR name enumeration
- Invalid HMAC returns 401 with no information about whether the CR exists
- Rate limiting on the webhook receiver endpoint (configurable, default: 100 req/min)

**Webhook Isolation**
- Webhook is a separate Deployment in the same namespace, not embedded in controller
- If webhook fails, pod creation is denied (failurePolicy: Ignore prevents cascading failures)
- If controller fails, webhook continues to inject sidecars (independent HA)
- Webhook uses separate TLS certificate, separate RBAC, separate resource quotas

### Data Integrity

**Checksum Verification**
- Agent computes SHA256 checksums for all synced files before and after copy
- If checksum mismatch detected, sync fails with error (prevents silent data corruption)
- Checksums stored in `/repo/.sync-status/{gatewayName}-checksums.json` for delta sync detection

**Signed File Manifests (Future)**
- Optional: Controller can sign the git commit (GPG) and agent verifies signature before sync
- Prevents man-in-the-middle attacks on git remotes

**Tamper Detection**
- Agent periodically (every N syncs) re-verifies that gateway filesystem matches expected state
- If files were manually modified on gateway, agent reports drift condition
- Controller can optionally auto-remediate by re-syncing

### Runtime Security

**Non-Root Containers**
```dockerfile
# Sync agent — distroless image runs as nonroot by default
FROM gcr.io/distroless/static-debian12:nonroot
# No shell, no package manager, no user creation needed
# distroless:nonroot runs as uid 65534 automatically
```

**Read-Only Root Filesystem**
```yaml
# Pod security context in Helm chart
securityContext:
  runAsNonRoot: true
  runAsUser: 65534
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
```

**Linux Capabilities**
- All containers drop ALL capabilities
- Agent needs only basic filesystem access (no NET_ADMIN, CAP_SYS_ADMIN, etc.)
- Enforced via PodSecurityPolicy or Kubernetes Policy engine (Kyverno, OPA)

### Webhook Injection Safety

**Validation of Injection Targets**
- Webhook validates that pod labels indicate an actual Ignition gateway (e.g., `app=ignition`)
- Whitelist check: pod namespace must be in `spec.webhookNamespaces` in the IgnitionSync CR
- Prevents accidental injection into unrelated pods (e.g., honeypot pods or testing containers)

```yaml
# IgnitionSync CR webhook config
spec:
  webhook:
    enabled: true
    allowedNamespaces:
      - site1
      - site2
      - public-demo
    # Default: all namespaces
```

**Injection Logging**
- Every injection attempt is logged with pod name, namespace, CR name, and result (success/failure)
- Logs are immutable (written to Kubernetes Events or external audit system)
- Failed injection attempts trigger alerts (via sidecar injection failure conditions)

**Pod Label Validation**
- Agent verifies at runtime that it is running in the expected pod (via downward API)
- If labels don't match CR selector, agent refuses to sync (safety check)

### Emergency Stop

**Global Pause Mechanism**
- Controller reconciliation can be paused via ConfigMap or CR field
- Disables all sync operations cluster-wide (critical for incident response)

```yaml
# Option 1: Set spec.paused on IgnitionSync CR
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: proveit-sync
spec:
  paused: true  # All syncs paused until set to false
  # ... rest of spec ...

# Option 2: Update controller ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: ignition-sync-global-pause
  namespace: ignition-sync-system
data:
  paused: "true"
```

- Emergency procedure documented in runbook: `kubectl patch ignitionsync {crName} -p '{"spec":{"paused":true}}'`
- Alert sent to incident response team when pause is activated
- Clear procedure to resume operations: `kubectl patch ignitionsync {crName} -p '{"spec":{"paused":false}}'`

### Audit Trail

**Sync Event Logging**
- Every sync operation is logged with:
  - Who triggered it (controller, webhook, polling timer)
  - What changed (commit SHA, files modified, projects synced)
  - When it happened (timestamp to millisecond)
  - Result (success, failure reason, duration)
  - Which gateway synced (pod name, namespace)

**Log Format (JSON for parsing)**
```json
{
  "timestamp": "2026-02-12T10:30:05.123Z",
  "cr": "proveit-sync",
  "namespace": "site1",
  "gateway": "site",
  "podName": "site1-site-gateway-0",
  "trigger": "webhook",  // or "polling", "manual"
  "commit": "abc123f",
  "ref": "2.0.0",
  "filesChanged": 47,
  "projectsSynced": ["site", "area1"],
  "duration": "3.2s",
  "result": "success",
  "scanResult": "projects=200 config=200"
}
```

**Audit System Integration**
- Logs can be exported to Kubernetes audit system (via structured logging)
- Integration with ELK, Splunk, Datadog, or other SIEM systems
- Syslog integration for air-gapped environments
- Queryable via `kubectl logs` and cluster logging infrastructure

**Immutability**
- Audit events are written to Kubernetes Events (immutable after creation)
- Optional: store to external audit service (Falco, Auditbeat) for tamper-resistance

---

## Roadmap

### v1 (Current)
- Single-cluster gateway discovery and sync
- Webhook-driven updates via ArgoCD, Kargo, GitHub (annotation-based, not spec mutation)
- Finalizer-based cleanup for CR deletion
- ConfigMap-only controller-agent signaling
- Security: scoped RBAC, constant-time HMAC, NetworkPolicy, distroless images, webhook TLS
- Observability: metrics, events, kubectl integration
- Ignition-aware features: fire-and-forget scan API, INITIAL_SYNC_DONE flag, recursive config normalization, .resources/ merge protection
- Doublestar glob matching, targeted JSON patching, service-path validation

### v1.1 (Near-term)
- CRD short names (`igs` for `ignitionsyncs`)
- Shared git clone cache (dedup across CRs referencing same repo)
- Optional native git CLI backend (for repos where go-git memory usage is a concern)
- Controller sharding for 500+ CRs
- Source abstraction layer (git, OCI, S3)
- Approval workflows and change gates
- Multi-environment config inheritance (IgnitionSyncBase CRD)
- Canary sync and staged rollout
- Enhanced snapshot/rollback capabilities
- Grafana dashboard as first-class artifact

### v2 (Medium-term)
- **Multi-cluster federation** — central control plane managing syncs across clusters (Liqo, Admiralty, or custom federation pattern)
- **Ignition API integration** — if future Ignition versions expose project/config import APIs, eliminate filesystem-based sync
- **Native Ignition module** — Ignition module that registers with operator for tighter integration (tag change events, project save hooks)
- **Advanced approval workflows** — integration with external approval systems (ServiceNow, Jira, custom)

### v3+ (Long-term)
- **UI dashboard** — web UI for viewing sync status, history, diffs, and controls across all sites (integrated into Ignition Gateway or standalone)
- **OLM / OperatorHub** — publishing to OperatorHub for one-click installation on OpenShift and other OLM-enabled clusters
- **GitOps integrations** — first-class support for Flux, ArgoCD as sync triggers (beyond webhooks)
- **Observability ecosystem** — OpenTelemetry integration, distributed tracing across multi-cluster syncs

---

## Review Changelog (v2 → v3)

Changes incorporated from the 6-agent architecture review:

### Must Fix (v1) — 17 items

| Change | Agent | Section |
|--------|-------|---------|
| Added finalizer handling for CR deletion cleanup | Agent 1 (K8s Best Practices) | Reconciliation Loop |
| Webhook receiver annotates CR instead of mutating spec.git.ref | Agent 1 | Webhook Receiver |
| Added ConfigMap RBAC permissions | Agent 1 | RBAC |
| Added ignitionsyncs/finalizers subresource permission | Agent 1 | RBAC |
| Scoped Secret access with namespace-mode guidance | Agent 1, 3 | RBAC |
| Fixed printer column — condition message instead of array jsonPath | Agent 1 | kubectl Integration |
| Added watch predicates (GenerationChangedPredicate) | Agent 1 | Reconciliation Loop |
| Scan API: fire-and-forget semantics, projects-before-config order | Agent 2 | Scan API, Sync Flow |
| Added doublestar library for ** glob support | Agent 2, 6 | Sync Flow, Agent |
| .resources/ protection: merge-based, not atomic swap | Agent 2, 6 | .resources/ Protection, Sync Flow |
| Recursive config normalization (filepath.Walk for all config.json) | Agent 2, 6 | Config Normalization, Sync Flow |
| PVC access: controller=RW, agents=RO clarified | Agent 3 | Storage Strategy |
| Constant-time HMAC comparison (crypto/subtle) | Agent 3 | Webhook Receiver, Security |
| CRD simplification with kubebuilder defaults | Agent 4 | CRD |
| ConfigMap-only signaling (removed PVC file-based fallback) | Agent 5 | Communication, Sync Flow |
| Targeted JSON patching (no full re-serialization) | Agent 6 | Sync Flow, Agent |
| Service-path validation at webhook injection time | Agent 6 | Sidecar Injection |

### Should Add (v1) — 12 items

| Change | Agent | Section |
|--------|-------|---------|
| CRD versioning strategy (v1alpha1 → v1beta1 → v1) | Agent 1 | CRD |
| Namespace-scoped Role generation when watchNamespaces is set | Agent 1 | RBAC |
| Rate limiting on webhook receiver endpoint | Agent 1, 3 | Security |
| PodDisruptionBudget for webhook | Agent 1 | Helm Chart |
| INITIAL_SYNC_DONE flag — skip scan on first sync | Agent 2 | Sync Flow |
| External resources createFallback behavior in sync flow | Agent 2 | Sync Flow |
| Overlay always recomposed on top of core | Agent 2, 6 | Sync Flow |
| systemName template default ({{.GatewayName}}) | Agent 2 | Annotations |
| NetworkPolicy included in Helm chart | Agent 3 | Helm Chart |
| Bidirectional guardrails (maxFileSize, excludePatterns) | Agent 3 | CRD |
| Quick Start section | Agent 4 | Quick Start |
| Non-blocking git, concurrent reconciles, scale section | Agent 5 | Reconciliation Loop, Scale |

### Nice to Have (deferred to v1.1+) — 10 items

| Item | Agent | Rationale for deferral |
|------|-------|----------------------|
| CRD short names (`igs`) | Agent 1 | Convenience, not correctness |
| CRD split into multiple types | Agent 1 | v1 keeps single CRD for simplicity |
| Shared git clone cache | Agent 5 | Optimization for multi-CR same-repo |
| Native git CLI backend | Agent 5 | Only needed for very large repos |
| Controller sharding | Agent 5 | Only needed at 500+ CRs |
| Source abstraction layer (git, OCI, S3) | Agent 5 | git is sufficient for v1 |
| Auto-derive cr-name from namespace | Agent 4 | Partially addressed (auto-derive when 1 CR) |
| Move annotations to gatewayOverrides CRD | Agent 4 | Annotations work for v1, CRD section is v2 |
| distroless vs Alpine variant | Agent 3 | Default is distroless; Alpine variant deferred |
| go-git memory optimization | Agent 5 | Document limits, fix in v1.1 |

### Rejected — 1 item

| Item | Agent | Rejection reason |
|------|-------|-----------------|
| Remove `enabled: true/false` fields from shared sections | Agent 4 | These serve as useful Helm value overrides — `enabled: false` is clearer than removing the entire block |
