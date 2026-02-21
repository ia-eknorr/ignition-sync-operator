# Ignition Sync Operator — Implementation Plan

## Table of Contents

1. [Development Environment Setup](#1-development-environment-setup)
2. [Project Structure](#2-project-structure)
3. [Implementation Phases](#3-implementation-phases)
4. [Phase 0: Scaffolding & Foundation](#phase-0-scaffolding--foundation)
5. [Phase 1: CRD Types & Validation](#phase-1-crd-types--validation)
6. [Phase 2: Controller Core — PVC, Git & Finalizer](#phase-2-controller-core--pvc-git--finalizer)
7. [Phase 3: Gateway Discovery & Status](#phase-3-gateway-discovery--status)
8. [Phase 3A: SyncProfile CRD](#phase-3a-syncprofile-crd)
9. [Phase 4: Mutating Admission Webhook](#phase-4-mutating-admission-webhook)
10. [Phase 5: Sync Agent Binary](#phase-5-sync-agent-binary)
11. [Phase 6: Webhook Receiver](#phase-6-webhook-receiver)
12. [Phase 7: Helm Chart](#phase-7-helm-chart)
13. [Phase 8: Observability & Metrics](#phase-8-observability--metrics)
14. [Phase 9: Advanced Features](#phase-9-advanced-features)
15. [Testing Strategy](#testing-strategy)
16. [Test Environment Setup](#test-environment-setup)

---

## 1. Development Environment Setup

### Prerequisites (already installed)

- kubectl, kind (v0.31.0), helm (v4.1.1), docker (29.2.0), vcluster, kustomize

### Tools to Install

```bash
# Go (required: 1.22+, recommend 1.23.x)
brew install go

# kubebuilder v4+
brew install kubebuilder

# Code quality
brew install golangci-lint

# Container image builder (fast, no Dockerfile needed for Go)
brew install ko

# Go tools (run after Go is in PATH)
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
go install golang.org/x/tools/gopls@latest
go install github.com/go-delve/delve/cmd/dlv@latest
```

### Environment Variables (~/.zshrc)

```bash
export GOPATH="$HOME/go"
export GOBIN="$GOPATH/bin"
export PATH="$GOBIN:$PATH"
```

### Available Test Clusters

| Context | Type | Use Case |
|---------|------|----------|
| `kind-dev` | Kind cluster | E2E testing, operator deployment |
| `vcluster-docker_vind-dev` | vcluster | Isolated testing sandbox |

### macOS ARM64 Gotchas

- kubebuilder binary may need `xattr -d com.apple.quarantine` if downloaded directly
- envtest binaries for K8s 1.29+ have native ARM64 support
- `ko` cross-compiles to linux/amd64 and linux/arm64 natively

---

## 2. Project Structure

```
ignition-sync-operator/
├── .github/
│   └── workflows/
│       ├── ci.yaml                          # Lint, test, build on PR
│       ├── release.yaml                     # Build/push images, publish chart on tag
│       └── e2e.yaml                         # E2E tests with kind
│
├── api/
│   └── v1alpha1/
│       ├── ignitionsync_types.go            # CRD spec, status, markers
│       ├── groupversion_info.go             # GVK: sync.ignition.io/v1alpha1
│       └── zz_generated.deepcopy.go         # Auto-generated
│
├── cmd/
│   ├── controller/
│   │   └── main.go                          # Controller manager entrypoint
│   ├── agent/
│   │   └── main.go                          # Sync agent sidecar entrypoint
│   └── webhook/
│       └── main.go                          # Admission webhook entrypoint (if separate binary)
│
├── internal/
│   ├── controller/
│   │   ├── ignitionsync_controller.go       # Main reconcile loop
│   │   ├── ignitionsync_controller_test.go  # Controller unit tests
│   │   └── pod_controller.go               # Pod watcher for gateway discovery
│   │
│   ├── webhook/
│   │   ├── pod_mutator.go                   # Sidecar injection logic
│   │   ├── pod_mutator_test.go              # Injection unit tests
│   │   ├── receiver.go                      # HTTP webhook receiver (ArgoCD/GitHub/Kargo)
│   │   ├── receiver_test.go                 # Receiver tests
│   │   └── hmac.go                          # HMAC signature validation
│   │
│   ├── agent/
│   │   ├── sync.go                          # Core file sync engine
│   │   ├── sync_test.go                     # Sync logic tests
│   │   ├── watcher.go                       # ConfigMap watch + polling timer fallback
│   │   ├── transforms.go                    # JSON/YAML normalization
│   │   ├── transforms_test.go              # Transform tests
│   │   ├── ignition_client.go               # Ignition Gateway API client
│   │   ├── ignition_client_test.go          # API client tests
│   │   └── status.go                        # ConfigMap status writer
│   │
│   ├── git/
│   │   ├── client.go                        # Git operations (go-git)
│   │   ├── client_test.go                   # Git tests
│   │   ├── auth.go                          # SSH/HTTPS/GitHub App auth
│   │   └── pr.go                            # PR creation via GitHub API
│   │
│   ├── storage/
│   │   ├── pvc.go                           # PVC creation/management
│   │   └── pvc_test.go                      # PVC tests
│   │
│   ├── injection/
│   │   ├── sidecar.go                       # Sidecar container spec builder
│   │   ├── sidecar_test.go                  # Sidecar builder tests
│   │   └── volumes.go                       # Volume/mount generation
│   │
│   └── metrics/
│       ├── controller.go                    # Controller Prometheus metrics
│       └── agent.go                         # Agent Prometheus metrics
│
├── pkg/
│   ├── types/
│   │   ├── sync_status.go                   # Shared sync status JSON schema
│   │   └── annotations.go                   # Annotation key constants
│   │
│   ├── conditions/
│   │   └── conditions.go                    # K8s condition helpers
│   │
│   └── version/
│       └── version.go                       # Build version info
│
├── charts/
│   └── ignition-sync/
│       ├── Chart.yaml
│       ├── values.yaml
│       ├── values.schema.json
│       ├── crds/
│       │   └── sync.ignition.io_ignitionsyncs.yaml
│       └── templates/
│           ├── _helpers.tpl
│           ├── controller-deployment.yaml
│           ├── controller-service.yaml
│           ├── controller-serviceaccount.yaml
│           ├── controller-clusterrole.yaml
│           ├── controller-clusterrolebinding.yaml
│           ├── webhook-deployment.yaml
│           ├── webhook-service.yaml
│           ├── webhook-mutating.yaml
│           ├── webhook-certificate.yaml
│           ├── leader-election-role.yaml
│           ├── networkpolicy.yaml
│           ├── poddisruptionbudget.yaml
│           └── servicemonitor.yaml
│
├── test/
│   ├── integration/
│   │   ├── suite_test.go                    # envtest setup
│   │   ├── controller_test.go               # Controller integration tests
│   │   └── webhook_test.go                  # Webhook integration tests
│   │
│   ├── e2e/
│   │   ├── suite_test.go                    # kind-based E2E setup
│   │   ├── basic_sync_test.go               # End-to-end sync test
│   │   ├── webhook_injection_test.go        # Sidecar injection E2E
│   │   └── fixtures/
│   │       ├── test-repo/                   # Sample git repo
│   │       └── manifests/                   # Test K8s manifests
│   │
│   └── testdata/
│       ├── ignition-projects/               # Sample Ignition project files
│       └── transform-samples/               # JSON/YAML transform fixtures
│
├── build/
│   ├── controller/
│   │   └── Dockerfile                       # Multi-stage distroless
│   └── agent/
│       └── Dockerfile                       # Multi-stage distroless
│
├── hack/
│   ├── tools.go                             # Go tool dependencies
│   ├── kind-with-certmanager.sh             # Create kind cluster + cert-manager
│   └── test-e2e.sh                          # Run full E2E suite
│
├── config/                                  # kubebuilder kustomize (dev/CI)
│   ├── crd/bases/
│   ├── rbac/
│   ├── manager/
│   ├── webhook/
│   ├── prometheus/
│   ├── samples/
│   └── default/
│
├── docs/
│   ├── ignition-sync-operator-architecture.md
│   └── implementation-plan.md               # This file
│
├── Makefile
├── PROJECT                                  # kubebuilder metadata
├── go.mod
├── go.sum
├── .golangci.yaml
└── .gitignore
```

### Key Architectural Decisions

- **Multi-binary layout**: `cmd/controller/`, `cmd/agent/`, `cmd/webhook/` — separate images, separate deployments, separate scaling
- **`internal/`** for operator-specific packages; **`pkg/`** for shared types between controller and agent
- **`go-git`** (pure Go) for git operations — works in distroless containers, no shelling out
- **Helm chart** as primary distribution (`charts/ignition-sync/`); kustomize (`config/`) for dev/CI
- **Standard Go `testing`** with `envtest` + `gomega` for async assertions (not full Ginkgo BDD)

---

## 3. Implementation Phases

Each phase is independently testable. Complete each phase's tests before moving to the next.

```
Phase 0:  Scaffolding         ─── make test passes, empty reconciler
Phase 1:  CRD Types           ─── CRD installs, validates, has printer columns
Phase 2:  Controller Core     ─── Finalizer, PVC created, non-blocking git, ConfigMap metadata
Phase 3:  Gateway Discovery   ─── Pods discovered, status.discoveredGateways populated
Phase 3A: SyncProfile CRD    ─── SyncProfile types, controller, IgnitionSync simplification
Phase 4:  Webhook             ─── Sidecar injected into annotated pods
Phase 5:  Sync Agent          ─── Files synced via profile mappings, Ignition API called
Phase 6:  Webhook Receiver    ─── HTTP POST annotates CR (annotation-based trigger)
Phase 7:  Helm Chart          ─── Full deployment via helm install
Phase 8:  Observability       ─── Prometheus metrics, events, kubectl columns
Phase 9:  Advanced Features   ─── Bidirectional, snapshots, canary (v1.1 scope)
```

---

## Phase 0: Scaffolding & Foundation

### Steps

1. Install Go and kubebuilder (see Section 1)
2. Initialize git repo
3. Initialize kubebuilder project:

```bash
cd /Users/eknorr/IA/code/personal/igntion-sync-operator

git init

kubebuilder init \
  --domain ignition.io \
  --repo github.com/inductiveautomation/ignition-sync-operator \
  --project-name ignition-sync-operator

kubebuilder create api \
  --group sync \
  --version v1alpha1 \
  --kind IgnitionSync \
  --resource --controller
```

4. Restructure `cmd/main.go` → `cmd/controller/main.go` (multi-binary layout)
5. Add `cmd/agent/main.go` stub
6. Create `pkg/types/annotations.go` with annotation constants
7. Create `pkg/conditions/conditions.go` with condition type constants

### Verify

```bash
make generate    # DeepCopy generated
make manifests   # CRD YAML generated
make test        # envtest passes (empty reconciler)
```

### Tests for Phase 0

- `make test` passes
- CRD YAML is syntactically valid
- `kubectl apply -f config/crd/bases/` succeeds in kind-dev

---

## Phase 1: CRD Types & Validation

### Files to Implement

**`api/v1alpha1/ignitionsync_types.go`** — Full CRD type definitions matching the architecture doc:

```go
// Key structs to define:
type IgnitionSyncSpec struct {
    Git             GitSpec             `json:"git"`
    Storage         StorageSpec         `json:"storage,omitempty"`
    Webhook         WebhookSpec         `json:"webhook,omitempty"`
    Polling         PollingSpec         `json:"polling,omitempty"`
    Gateway         GatewaySpec         `json:"gateway"`
    SiteNumber      string              `json:"siteNumber,omitempty"`
    Shared          SharedSpec          `json:"shared,omitempty"`
    AdditionalFiles []AdditionalFile    `json:"additionalFiles,omitempty"`
    ExcludePatterns []string            `json:"excludePatterns,omitempty"`
    Normalize       NormalizeSpec       `json:"normalize,omitempty"`
    Bidirectional   BidirectionalSpec   `json:"bidirectional,omitempty"`
    Validation      ValidationSpec      `json:"validation,omitempty"`
    Snapshots       SnapshotSpec        `json:"snapshots,omitempty"`
    Deployment      DeploymentSpec      `json:"deployment,omitempty"`
    Paused          bool                `json:"paused,omitempty"`
    Ignition        IgnitionSpec        `json:"ignition,omitempty"`
    Agent           AgentSpec           `json:"agent,omitempty"`
}

type IgnitionSyncStatus struct {
    ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
    LastSyncTime       *metav1.Time             `json:"lastSyncTime,omitempty"`
    LastSyncRef        string                   `json:"lastSyncRef,omitempty"`
    LastSyncCommit     string                   `json:"lastSyncCommit,omitempty"`
    RepoCloneStatus    string                   `json:"repoCloneStatus,omitempty"`
    DiscoveredGateways []DiscoveredGateway      `json:"discoveredGateways,omitempty"`
    Conditions         []metav1.Condition        `json:"conditions,omitempty"`
}
```

### kubebuilder Markers

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=isync;igs
// +kubebuilder:printcolumn:name="Ref",type="string",JSONPath=`.spec.git.ref`
// +kubebuilder:printcolumn:name="Synced",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].status`
// +kubebuilder:printcolumn:name="Gateways",type="string",JSONPath=`.status.conditions[?(@.type=="AllGatewaysSynced")].message`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
```

> **Note:** The Gateways column uses a condition *message* (e.g., `"3/3 synced"`) instead of a
> `jsonPath` into an array field — arrays don't render well as printer columns.

### Sensible Defaults via kubebuilder Markers

```go
// Storage defaults
// +kubebuilder:default:="1Gi"
Size string `json:"size,omitempty"`
// +kubebuilder:default:="ReadWriteMany"
AccessMode string `json:"accessMode,omitempty"`

// Webhook defaults
// +kubebuilder:default:=true
Enabled *bool `json:"enabled,omitempty"`

// Polling defaults
// +kubebuilder:default:="5m"
Interval string `json:"interval,omitempty"`

// Gateway defaults
// +kubebuilder:default:=8043
Port int32 `json:"port,omitempty"`
// +kubebuilder:default:=true
TLS *bool `json:"tls,omitempty"`
```

> Sensible defaults reduce the minimal CR to just `spec.git.repo`, `spec.git.ref`, and
> `spec.gateway.apiKeySecretRef`. Everything else has a production-ready default.

### Validation Markers

- `spec.git.repo`: `+kubebuilder:validation:Required`, `+kubebuilder:validation:MinLength=1`
- `spec.git.ref`: `+kubebuilder:validation:Required`
- `spec.storage.accessMode`: `+kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany`
- `spec.ignition.designerSessionPolicy`: `+kubebuilder:validation:Enum=wait;proceed;fail`
- `status.conditions`: `+listType=map`, `+listMapKey=type`

### Tests for Phase 1

| Test | Type | What It Validates |
|------|------|-------------------|
| CRD applies to cluster | E2E (kind-dev) | `kubectl apply -f config/crd/bases/` succeeds |
| Required field validation | Unit | Missing `spec.git.repo` → rejected |
| Enum validation | Unit | Invalid `accessMode` → rejected |
| DeepCopy generated | Build | `make generate` succeeds |
| Printer columns | E2E | `kubectl get isync` shows Ref, Synced, Ready, Age |
| Sample CR applies | E2E | `kubectl apply -f config/samples/` succeeds |

```bash
# Run in kind-dev
kubectl config use-context kind-dev
make install
kubectl apply -f config/samples/sync_v1alpha1_ignitionsync.yaml
kubectl get isync
```

---

## Phase 2: Controller Core — PVC, Git & Finalizer

### Files to Implement

1. **`internal/storage/pvc.go`** — PVC creation with owner references
2. **`internal/git/client.go`** — Git clone/fetch/checkout using `go-git`
3. **`internal/git/auth.go`** — SSH key, HTTPS token, GitHub App auth
4. **`internal/controller/ignitionsync_controller.go`** — Reconcile steps 0-4

### Reconcile Flow (This Phase)

```
0. Finalizer handling:
   - If CR is being deleted (deletionTimestamp set):
     a. Clean up owned ConfigMaps (metadata, status, changes)
     b. Signal agents to stop via ConfigMap
     c. Remove finalizer → allows CR deletion
   - If CR is not being deleted and finalizer missing:
     a. Add finalizer sync.ignition.io/finalizer
1. Validate CR spec (secrets exist, storage class valid)
2. Ensure repo PVC exists (create with ownerReference if missing)
3. Clone or update repo — NON-BLOCKING:
   - Launch git clone/fetch in a goroutine
   - Set condition GitSyncing=True while in progress
   - On completion, update ConfigMap metadata + set RepoCloned=True
   - On failure, set RepoCloned=False with error message
4. Create/update ConfigMap ignition-sync-metadata-{crName}
5. Set conditions: RepoCloned
6. Requeue after spec.polling.interval
```

> **Why non-blocking git?** A slow git clone (large repo, flaky network) should not block the
> reconcile loop. The controller can continue processing other CRs while git operations complete.

### Key Implementation Details

**Finalizer Pattern:**
```go
const finalizerName = "sync.ignition.io/finalizer"

func (r *IgnitionSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var isync syncv1alpha1.IgnitionSync
    if err := r.Get(ctx, req.NamespacedName, &isync); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Handle deletion
    if !isync.DeletionTimestamp.IsZero() {
        if controllerutil.ContainsFinalizer(&isync, finalizerName) {
            // Clean up ConfigMaps, signal agents
            if err := r.cleanupOwnedResources(ctx, &isync); err != nil {
                return ctrl.Result{}, err
            }
            controllerutil.RemoveFinalizer(&isync, finalizerName)
            return ctrl.Result{}, r.Update(ctx, &isync)
        }
        return ctrl.Result{}, nil
    }

    // Ensure finalizer present
    if !controllerutil.ContainsFinalizer(&isync, finalizerName) {
        controllerutil.AddFinalizer(&isync, finalizerName)
        return ctrl.Result{}, r.Update(ctx, &isync)
    }
    // ... rest of reconcile
}
```

**PVC with Owner Reference:**
```go
controllerutil.SetControllerReference(isync, pvc, r.Scheme)
// PVC gets garbage collected when IgnitionSync CR is deleted
```

**Non-blocking Git via goroutine:**
```go
import "github.com/go-git/go-git/v5"

// Launch git in background — don't block reconcile loop
go func() {
    if err := r.gitClient.CloneOrFetch(ctx, repo, ref, pvcPath); err != nil {
        r.setCondition(isync, "RepoCloned", metav1.ConditionFalse, "GitFailed", err.Error())
        return
    }
    r.setCondition(isync, "RepoCloned", metav1.ConditionTrue, "Cloned", commitSHA)
    r.updateMetadataConfigMap(ctx, isync, commitSHA, ref)
}()
```

**ConfigMap Metadata Signal (controller → agents):**
```go
// Controller writes to ConfigMap for agent to watch
cm := &corev1.ConfigMap{
    ObjectMeta: metav1.ObjectMeta{
        Name: fmt.Sprintf("ignition-sync-metadata-%s", cr.Name),
        Namespace: cr.Namespace,
    },
    Data: map[string]string{
        "commit":  commitSHA,
        "ref":     cr.Spec.Git.Ref,
        "trigger": time.Now().UTC().Format(time.RFC3339),
    },
}
```

### Tests for Phase 2

| Test | Type | What It Validates |
|------|------|-------------------|
| Finalizer added on create | Integration (envtest) | CR has `sync.ignition.io/finalizer` |
| CR deletion triggers cleanup | Integration | ConfigMaps deleted, finalizer removed |
| CR deletion blocked until cleanup done | Integration | Finalizer prevents premature GC |
| PVC created on CR create | Integration (envtest) | PVC exists with correct owner ref |
| PVC has correct labels | Integration | `sync.ignition.io/cr-name` label set |
| PVC storage class from spec | Integration | storageClassName matches |
| Git clone succeeds | Unit | go-git clones to temp dir |
| Git checkout ref | Unit | Specific tag/branch checked out |
| Git clone is non-blocking | Integration | Reconcile returns before clone completes |
| SSH auth works | Unit | SSH key loaded correctly |
| Token auth works | Unit | HTTPS token auth configured |
| RepoCloned condition set | Integration | Condition type=RepoCloned, status=True |
| ConfigMap metadata created | Integration | ConfigMap has commit, ref, trigger |
| Requeue after polling interval | Integration | Result.RequeueAfter matches spec |
| CR deletion cleans PVC | Integration | PVC garbage collected |

```bash
# Test git operations with a local bare repo
git init --bare /tmp/test-repo.git
cd /tmp && git clone test-repo.git test-working
cd test-working && echo "test" > file.txt && git add . && git commit -m "init" && git push
# Use file:///tmp/test-repo.git as the repo URL in tests
```

---

## Phase 3: Gateway Discovery & Status

### Files to Implement

1. **`internal/controller/ignitionsync_controller.go`** — Add steps 4-5, 7
2. **`pkg/types/sync_status.go`** — Shared status JSON schema
3. **`pkg/conditions/conditions.go`** — Condition constants and helpers

### Reconcile Flow (This Phase Adds)

```
4. Discover gateways — list pods with annotation ignition-sync.io/cr-name == crName
5. Read sync status — from ConfigMap ignition-sync-status-{crName} (ConfigMap-only, no PVC files)
6. Update status.discoveredGateways from pod list
7. Set conditions: Ready, AllGatewaysSynced
8. Emit K8s events for state changes
```

### Pod Watch Setup

```go
func (r *IgnitionSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&syncv1alpha1.IgnitionSync{},
            builder.WithPredicates(predicate.GenerationChangedPredicate{}),
        ).
        Owns(&corev1.PersistentVolumeClaim{}).
        Owns(&corev1.ConfigMap{}).
        Watches(&corev1.Pod{},
            handler.EnqueueRequestsFromMapFunc(r.findIgnitionSyncForPod),
            builder.WithPredicates(predicate.AnnotationChangedPredicate{}),
        ).
        WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
        Complete(r)
}

func (r *IgnitionSyncReconciler) findIgnitionSyncForPod(
    ctx context.Context, pod client.Object,
) []reconcile.Request {
    crName := pod.GetAnnotations()["ignition-sync.io/cr-name"]
    if crName == "" {
        return nil
    }
    return []reconcile.Request{{
        NamespacedName: types.NamespacedName{
            Name:      crName,
            Namespace: pod.GetNamespace(),
        },
    }}
}
```

### Condition Constants

```go
const (
    ConditionReady            = "Ready"
    ConditionRepoCloned       = "RepoCloned"
    ConditionAllGatewaysSynced = "AllGatewaysSynced"
    ConditionWebhookReady     = "WebhookReady"
    ConditionBidirectionalReady = "BidirectionalReady"
)
```

### Tests for Phase 3

| Test | Type | What It Validates |
|------|------|-------------------|
| Pod with cr-name annotation triggers reconcile | Integration | Controller sees pod events |
| discoveredGateways populated | Integration | Status shows pod details |
| Gateway removed when pod deleted | Integration | Entry removed from status |
| AllGatewaysSynced condition | Integration | False when some gateways not synced |
| Ready condition aggregates sub-conditions | Integration | Ready=True only when all met |
| K8s event emitted on sync complete | Integration | Event recorded for CR |
| Multi-CR same namespace | Integration | Pods associate with correct CR |

---

## Phase 3A: SyncProfile CRD

> Architecture doc: [docs/architecture/04-sync-profile.md](architecture/04-sync-profile.md)

### Motivation

Phase 3A extracts file routing concerns from IgnitionSync into a dedicated `SyncProfile` CRD. This replaces the opinionated `shared`, `additionalFiles`, `normalize`, and `siteNumber` fields with generic ordered source→destination mappings. The IgnitionSync CR becomes pure infrastructure (git, storage, gateway API), while SyncProfile handles what files go where.

### Dependencies

- **Depends on:** Phase 3 (gateway discovery, pod annotation infrastructure)
- **Feeds into:** Phase 5 (agent uses SyncProfile mappings for file routing)
- **Can parallel with:** Phases 4 and 6

### Files to Create

1. **`api/v1alpha1/syncprofile_types.go`** — SyncProfile CRD types (SyncProfileSpec, SyncMapping, DeploymentModeSpec, SyncProfileStatus)
2. **`internal/controller/syncprofile_controller.go`** — Validation-only controller (sets `Accepted` condition)
3. **`config/samples/sync_v1alpha1_syncprofile.yaml`** — Sample SyncProfile CR

### Files to Modify

1. **`api/v1alpha1/ignitionsync_types.go`** — Remove deprecated fields (`SharedSpec`, `AdditionalFile`, `NormalizeSpec`, `SiteNumber`)
2. **`pkg/types/annotations.go`** — Add `AnnotationSyncProfile = "ignition-sync.io/sync-profile"`
3. **`pkg/conditions/conditions.go`** — Add `ConditionAccepted = "Accepted"`
4. **`cmd/controller/main.go`** — Register SyncProfile controller
5. **`PROJECT`** — Add SyncProfile resource entry
6. **`internal/controller/ignitionsync_controller.go`** — Watch SyncProfiles, update `status.gatewayCount` on referenced profiles

### SyncProfile Controller Logic

The SyncProfile controller is intentionally lightweight:

```go
func (r *SyncProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var profile syncv1alpha1.SyncProfile
    if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Validate mappings
    if err := validateMappings(profile.Spec.Mappings); err != nil {
        meta.SetStatusCondition(&profile.Status.Conditions, metav1.Condition{
            Type:    "Accepted",
            Status:  metav1.ConditionFalse,
            Reason:  "ValidationFailed",
            Message: err.Error(),
        })
        return ctrl.Result{}, r.Status().Update(ctx, &profile)
    }

    // Valid — set Accepted=True
    meta.SetStatusCondition(&profile.Status.Conditions, metav1.Condition{
        Type:   "Accepted",
        Status: metav1.ConditionTrue,
        Reason: "Valid",
    })
    profile.Status.ObservedGeneration = profile.Generation
    return ctrl.Result{}, r.Status().Update(ctx, &profile)
}

func validateMappings(mappings []syncv1alpha1.SyncMapping) error {
    for _, m := range mappings {
        if strings.Contains(m.Source, "..") || strings.Contains(m.Destination, "..") {
            return fmt.Errorf("path traversal (..) not allowed: source=%q dest=%q", m.Source, m.Destination)
        }
        if filepath.IsAbs(m.Source) || filepath.IsAbs(m.Destination) {
            return fmt.Errorf("absolute paths not allowed: source=%q dest=%q", m.Source, m.Destination)
        }
    }
    return nil
}
```

### IgnitionSync Simplification

Remove these types from `ignitionsync_types.go`:

| Type | Replacement |
|------|-------------|
| `SharedSpec` | `SyncProfile.spec.mappings` |
| `ExternalResourcesSpec` | `SyncProfile.spec.mappings` |
| `ScriptsSpec` | `SyncProfile.spec.mappings` |
| `UDTsSpec` | `SyncProfile.spec.mappings` |
| `AdditionalFile` | `SyncProfile.spec.mappings` |
| `NormalizeSpec` | Future: pre/post-sync hooks |
| `FieldReplacement` | Future: pre/post-sync hooks |

Remove these fields from `IgnitionSyncSpec`:

```go
// REMOVE:
SiteNumber      string           `json:"siteNumber,omitempty"`
Shared          SharedSpec       `json:"shared,omitempty"`
AdditionalFiles []AdditionalFile `json:"additionalFiles,omitempty"`
Normalize       NormalizeSpec    `json:"normalize,omitempty"`
```

### Verify

```bash
make generate          # DeepCopy for SyncProfile types
make manifests         # SyncProfile CRD YAML generated
make build             # Compiles cleanly
make test              # All existing tests pass
kubectl apply -f config/crd/bases/   # Both CRDs install
kubectl apply -f config/samples/sync_v1alpha1_syncprofile.yaml   # Sample CR accepted
```

### Tests for Phase 3A

| Test | Type | What It Validates |
|------|------|-------------------|
| SyncProfile CRD installs | E2E (kind-dev) | `kubectl apply -f config/crd/bases/` succeeds |
| Empty mappings rejected | Unit | `MinItems=1` marker enforced |
| Path traversal rejected | Unit | Source or dest with `..` → Accepted=False |
| Absolute paths rejected | Unit | Source or dest starting with `/` → Accepted=False |
| Valid profile → Accepted=True | Integration (envtest) | Condition set correctly |
| Short name `sp` works | E2E | `kubectl get sp` succeeds |
| Printer columns show | E2E | Mappings, Mode, Gateways, Accepted, Age columns |
| IgnitionSync no longer has `shared` | Build | Removed fields cause compile error if referenced |
| Pod with `sync-profile` annotation discovered | Integration | Profile resolved by controller |
| Pod without `sync-profile` → 2-tier mode | Integration | Falls back to `service-path` annotation |
| Profile deletion → graceful degradation | Integration | Warning logged, gateway continues |
| Profile update → re-reconcile | Integration | Affected gateways re-synced |
| SyncProfile `gatewayCount` updated | Integration | Status reflects referencing pod count |

---

## Phase 4: Mutating Admission Webhook

### Files to Implement

1. **`internal/webhook/pod_mutator.go`** — Sidecar injection handler
2. **`internal/injection/sidecar.go`** — Build sidecar container spec
3. **`internal/injection/volumes.go`** — Build volume/mount specs
4. **`cmd/webhook/main.go`** — Webhook server entrypoint (or mode flag in controller)
5. **`config/webhook/`** — MutatingWebhookConfiguration, cert-manager Certificate

### Injection Logic

Following Istio/Vault Agent patterns:

1. Decode pod from admission request
2. Check `ignition-sync.io/inject: "true"` annotation
3. Look up IgnitionSync CR by `ignition-sync.io/cr-name`
   - Validate service-path annotation exists and is non-empty at injection time
   - Reject injection (with warning event) if service-path is missing
4. Build sidecar container:
   - Image from CR `spec.agent.image`
   - Env vars from annotations (service-path, deployment-mode, tag-provider, etc.)
   - Volume mounts: `/repo` (RO from PVC), `/ignition-data` (RW shared with gateway)
   - API key secret mount from CR `spec.gateway.apiKeySecretRef`
   - Startup probe on :8082 (blocks gateway until initial sync)
   - Resource limits from CR `spec.agent.resources`
5. Add sidecar to `pod.Spec.Containers`
6. Add volumes to `pod.Spec.Volumes`
7. Add label `ignition-sync.io/injected: "true"` (prevents re-injection)
8. Return JSON patch

### MutatingWebhookConfiguration

```yaml
failurePolicy: Ignore           # Never block pod creation
reinvocationPolicy: IfNeeded
namespaceSelector:
  matchExpressions:
    - key: ignition-sync.io/injection-enabled
      operator: In
      values: ["true"]
objectSelector:
  matchExpressions:
    - key: ignition-sync.io/injected
      operator: DoesNotExist
timeoutSeconds: 10
```

### cert-manager Integration

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: ignition-sync-webhook-cert
spec:
  secretName: ignition-sync-webhook-tls
  issuerRef:
    name: ignition-sync-selfsigned
    kind: Issuer
  dnsNames:
    - ignition-sync-webhook.ignition-sync-system.svc
    - ignition-sync-webhook.ignition-sync-system.svc.cluster.local
```

### Tests for Phase 4

| Test | Type | What It Validates |
|------|------|-------------------|
| Sidecar injected on annotated pod | Unit | Container added to pod spec |
| No injection without annotation | Unit | Pod unchanged |
| No injection if already injected | Unit | `injected` label prevents re-inject |
| Correct volumes added | Unit | repo PVC + ignition-data |
| Env vars from annotations | Unit | SERVICE_PATH, DEPLOYMENT_MODE set |
| API key secret mounted | Unit | Volume from CR spec |
| Startup probe configured | Unit | HTTP probe on :8082 |
| CR not found → allowed (no injection) | Unit | Pod passes through |
| MutatingWebhookConfig deploys | E2E (kind-dev) | cert-manager issues cert |
| Pod creation with webhook active | E2E | Sidecar appears in running pod |

```bash
# E2E test in kind-dev
kubectl config use-context kind-dev
# Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
# Deploy operator + webhook
helm install ignition-sync charts/ignition-sync/ -n ignition-sync-system --create-namespace
# Create test namespace with injection label
kubectl create ns test-inject
kubectl label ns test-inject ignition-sync.io/injection-enabled=true
# Create IgnitionSync CR + annotated pod → verify sidecar
```

---

## Phase 5: Sync Agent Binary

### Files to Implement

1. **`cmd/agent/main.go`** — Agent entrypoint
2. **`internal/agent/sync.go`** — Core sync engine
3. **`internal/agent/watcher.go`** — ConfigMap watch + polling timer fallback (no inotify — PVC is RO)
4. **`internal/agent/transforms.go`** — JSON/YAML normalization, recursive config discovery, targeted JSON patching
5. **`internal/agent/ignition_client.go`** — Ignition Gateway API client
6. **`internal/agent/status.go`** — ConfigMap status writer (no PVC .sync-status/ files)
7. **`build/agent/Dockerfile`** — Multi-stage distroless (NOT Alpine)

### Agent Sync Flow

```
Startup:
  1. Mount /repo (RO) and /ignition-data (RW)
  2. Read config from env vars (set by webhook injection)
  3. Establish K8s API connection for ConfigMap watch
  4. Perform initial sync (blocking — gateway waits via startup probe)
     → Set INITIAL_SYNC_DONE=false (skip scan API on first sync)
  5. Mark healthy on :8082 (startup probe passes, gateway starts)
     → Set INITIAL_SYNC_DONE=true
  6. Start ConfigMap watch (preferred) or polling timer fallback
     NOTE: inotify is NOT used — PVC is mounted RO by agents,
           inotify requires write access for watch descriptors
  7. Start periodic fallback timer

On trigger:
  1. Read ref + commit from ConfigMap ignition-sync-metadata-{crName}
  2. Compare against last-synced commit — skip if unchanged
  3. Compute file checksums (SHA256) for delta detection
  4. Create staging directory /ignition-data/.sync-staging/
  5. Sync files: service-path projects, config, shared resources
  6. Apply deployment mode overlay
     → Overlay ALWAYS recomposed on top of core, even if overlay itself hasn't changed
       (core changes without overlay changes still need overlay re-applied)
  7. Apply exclude patterns (uses github.com/bmatcuk/doublestar for ** glob support)
  8. Recursive config normalization:
     → filepath.Walk to find ALL config.json at any depth (not just top-level)
     → Targeted JSON patching: modify field values in-place without re-serializing
       entire file (prevents false diffs from key reordering/whitespace changes)
     → Apply systemName, Go templates
  9. Validate JSON syntax on all discovered config.json files
  10. Verify no .resources/ in staging (safety check)
  11. Selective merge: staging → /ignition-data/ (preserving .resources/)
      → Walk staging dir, copy each file to destination
      → Delete files in destination NOT in staging AND NOT in protected list
      → Protected: .resources/**, .sync-staging/**, any excludePattern matches
      NOTE: This replaces "atomic swap" — selective merge is safer for .resources/
  12. Health check: GET /data/api/v1/status
  13. If INITIAL_SYNC_DONE:
      → Fire-and-forget scan: POST /data/api/v1/scan/projects (MUST be first)
      → Then: POST /data/api/v1/scan/config
      → These return 200 immediately — there is NO poll endpoint
      → On first sync (INITIAL_SYNC_DONE=false): SKIP scan entirely
        (Ignition auto-scans on boot; calling scan during startup causes race conditions)
  14. Write status to ConfigMap ignition-sync-status-{crName} (ConfigMap-only, no PVC files)
  15. Clean up staging
```

### Ignition API Client

```go
type IgnitionClient struct {
    BaseURL string
    APIKey  string
    TLS     bool
    Client  *http.Client
}

func (c *IgnitionClient) GetStatus(ctx context.Context) error
func (c *IgnitionClient) ScanProjects(ctx context.Context) error
func (c *IgnitionClient) ScanConfig(ctx context.Context) error
func (c *IgnitionClient) GetSessions(ctx context.Context) ([]Session, error)
func (c *IgnitionClient) GetModules(ctx context.Context) ([]Module, error)
```

### .resources/ Protection

```go
// ALWAYS exclude .resources/ - never sync runtime caches
func validateStagingDir(stagingPath string) error {
    resourcesPath := filepath.Join(stagingPath, ".resources")
    if _, err := os.Stat(resourcesPath); err == nil {
        return fmt.Errorf("staging directory contains .resources/ — aborting sync")
    }
    return nil
}
```

### Agent Dockerfile (distroless)

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /sync-agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /sync-agent /usr/local/bin/sync-agent
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/sync-agent"]
```

> **Why distroless over Alpine?** Distroless has no shell, no package manager, no unnecessary
> binaries — minimal attack surface. Since go-git is pure Go (no git CLI needed) and the agent
> binary is statically compiled, there's nothing Alpine provides that we need. No `tini` needed
> either — Go handles signal forwarding natively.

### Tests for Phase 5

| Test | Type | What It Validates |
|------|------|-------------------|
| SyncDirectory copies files correctly | Unit | Files appear in dest |
| Delta sync skips unchanged files | Unit | Only changed files copied |
| Exclude patterns with ** globs | Unit | doublestar patterns match correctly |
| .resources/ never synced | Unit | .resources/ absent from staging |
| Recursive config discovery | Unit | Finds config.json at nested depths |
| Targeted JSON patching | Unit | Only specified fields modified, key order preserved |
| JSON normalization (systemName) | Unit | systemName replaced in all config.json |
| Go template rendering | Unit | `{{.SiteNumber}}-{{.GatewayName}}` rendered |
| Selective merge preserves .resources/ | Unit | .resources/ untouched after merge |
| Selective merge deletes removed files | Unit | Files not in staging are deleted |
| JSON syntax validation | Unit | Invalid JSON fails sync |
| Overlay always-recompose | Unit | Core-only change still triggers overlay re-apply |
| Checksum computation | Unit | SHA256 matches expected |
| Health endpoint responds | Unit | GET /healthz returns 200 |
| Scan API fire-and-forget | Unit (mock) | POST /scan/projects called, no polling |
| Scan skipped on initial sync | Unit | INITIAL_SYNC_DONE=false → no scan API call |
| Status written to ConfigMap | Integration | ConfigMap has correct status fields |
| ConfigMap watch triggers sync | Integration | ConfigMap update → sync fires |
| Initial sync blocks until complete | Integration | Startup probe fails then passes |

```bash
# Agent unit tests can run locally (no cluster needed)
go test ./internal/agent/... -v -count=1

# Agent E2E requires a mock Ignition API
# Use httptest.NewServer in tests to mock gateway responses
```

---

## Phase 6: Webhook Receiver

### Files to Implement

1. **`internal/webhook/receiver.go`** — HTTP endpoint for external triggers (annotation-based)
2. **`internal/webhook/hmac.go`** — HMAC-SHA256 constant-time signature validation

### Endpoint

```
POST /webhook/{namespace}/{crName}
Headers: X-Hub-Signature-256: sha256=...
Content-Type: application/json

Auto-detected payload formats:
1. Generic:  { "ref": "2.0.0" }
2. GitHub:   { "action": "published", "release": { "tag_name": "2.0.0" } }
3. ArgoCD:   { "app": { "metadata": { "annotations": { "git.ref": "2.0.0" } } } }
4. Kargo:    { "freight": { "commits": [{ "tag": "2.0.0" }] } }
```

### Request Handling Order

```
1. Read body + X-Hub-Signature-256 header
2. Validate HMAC FIRST (before CR lookup) — prevents enumeration attacks
3. Parse ref from payload
4. Look up CR by namespace/crName
5. ANNOTATE CR with sync.ignition.io/requested-ref (NOT spec.git.ref mutation)
6. Return 202 Accepted (async processing)
```

> **Why annotation instead of spec mutation?** Writing to `spec.git.ref` conflicts with GitOps
> tools (ArgoCD, Flux) that own the spec. Annotations are metadata-only — they trigger a reconcile
> without fighting the GitOps controller for spec ownership. The controller reads the annotation
> and processes the ref change.

### Constant-Time HMAC Validation

```go
import "crypto/subtle"

func ValidateHMAC(payload []byte, signature, secret string) error {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    // constant-time comparison — prevents timing oracle attacks
    if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
        return fmt.Errorf("HMAC validation failed")
    }
    return nil
}
```

> **Why constant-time?** Standard `==` or even `hmac.Equal` can leak information about how many
> bytes matched before failing. `crypto/subtle.ConstantTimeCompare` takes the same time regardless
> of where the mismatch occurs, preventing timing side-channel attacks.

### Tests for Phase 6

| Test | Type | What It Validates |
|------|------|-------------------|
| Generic payload parsed | Unit | ref extracted correctly |
| GitHub release payload parsed | Unit | tag_name extracted |
| ArgoCD payload parsed | Unit | annotation git.ref extracted |
| Kargo payload parsed | Unit | freight commits tag extracted |
| HMAC validated before CR lookup | Unit | Invalid HMAC → 401 without DB query |
| HMAC constant-time comparison | Unit | Uses subtle.ConstantTimeCompare |
| HMAC validation passes | Unit | Valid signature accepted |
| HMAC validation fails | Unit | Invalid signature → 401 |
| CR not found → 404 | Unit | Unknown CR name |
| Annotation set on CR | Integration | `sync.ignition.io/requested-ref` annotation written |
| Returns 202 Accepted | Unit | Response is 202, not 200 |
| Duplicate ref ignored | Unit | Same ref → no annotation update |

---

## Phase 7: Helm Chart

### Files to Implement

Full Helm chart under `charts/ignition-sync/` (see project structure above).

### Key Design Decisions

- CRD in `crds/` directory (installed automatically by helm install)
- Controller and webhook as separate Deployments
- cert-manager Certificate for webhook TLS
- Leader election RBAC for controller
- NetworkPolicy to restrict controller/webhook network access (deny-by-default)
- PodDisruptionBudget for controller availability during node drains
- ServiceMonitor for Prometheus (optional)
- values.schema.json for input validation

### Tests for Phase 7

| Test | Type | What It Validates |
|------|------|-------------------|
| `helm lint` passes | CI | Chart is valid |
| `helm template` renders | CI | No template errors |
| `helm install --dry-run` | CI | All resources valid |
| Full install in kind-dev | E2E | All pods running, CRD installed |
| Upgrade works | E2E | `helm upgrade` succeeds |
| Uninstall cleans up | E2E | All resources removed (except CRD) |
| values.schema.json validates | CI | Invalid values rejected |

```bash
# Test in kind-dev
kubectl config use-context kind-dev
helm install ignition-sync charts/ignition-sync/ \
  -n ignition-sync-system --create-namespace \
  --set controller.image.tag=dev \
  --set webhook.image.tag=dev
```

---

## Phase 8: Observability & Metrics

### Files to Implement

1. **`internal/metrics/controller.go`** — Controller metrics registration
2. **`internal/metrics/agent.go`** — Agent metrics registration

### Prometheus Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `ignition_sync_reconcile_total` | Counter | cr, namespace, result |
| `ignition_sync_reconcile_duration_seconds` | Histogram | cr, namespace |
| `ignition_sync_git_fetch_duration_seconds` | Histogram | cr |
| `ignition_sync_webhook_received_total` | Counter | source_type, result |
| `ignition_sync_gateways_discovered` | Gauge | cr, namespace |
| `ignition_sync_gateways_synced` | Gauge | cr, namespace |
| `ignition_sync_sync_duration_seconds` | Histogram | gateway, cr |
| `ignition_sync_files_changed_total` | Counter | gateway, cr |
| `ignition_sync_scan_api_duration_seconds` | Histogram | gateway |

### Tests for Phase 8

| Test | What It Validates |
|------|-------------------|
| Metrics endpoint responds | GET /metrics returns Prometheus format |
| Reconcile counter increments | Counter increases after reconcile |
| Gateway gauge accurate | Matches discoveredGateways count |

---

## Phase 9: Advanced Features (v1.1 Scope)

These features are designed in the architecture doc but should be implemented after the core v1 is solid:

1. **Bidirectional sync** — inotify watching on gateway, change manifests, PR creation
2. **Snapshots & rollback** — Pre-sync tarball, restore endpoint
3. **Canary sync** — Staged rollout with health checks
4. **Deployment ordering** — Dependency-aware sync (site before areas)
5. **Designer session detection** — Pause sync when Designer is active
6. **Approval workflows** — PendingApproval condition, manual gate

---

## Testing Strategy

### Four-Tier Testing Pyramid

```
                    ┌──────────┐
                    │   E2E    │  kind-dev cluster, full deployment
                   ┌┴──────────┴┐
                   │ Integration │  envtest (real etcd + apiserver)
                  ┌┴────────────┴┐
                  │  Unit Tests   │  Go testing, mocks, table-driven
                 ┌┴──────────────┴┐
                 │   Static Analysis │  golangci-lint, vet, CRD validation
                 └────────────────┘
```

### Tier 1: Unit Tests

**Coverage target: 80%+ on business logic packages**

```bash
go test ./internal/... ./pkg/... -v -coverprofile=cover.out
```

Key test files:
- `internal/agent/sync_test.go` — File sync logic, checksums, excludes
- `internal/agent/transforms_test.go` — JSON/YAML normalization
- `internal/agent/ignition_client_test.go` — API client (httptest mock)
- `internal/webhook/pod_mutator_test.go` — Sidecar injection patches
- `internal/webhook/receiver_test.go` — Payload parsing, HMAC validation
- `internal/git/client_test.go` — Clone, fetch, checkout
- `internal/injection/sidecar_test.go` — Container spec generation

### Tier 2: Integration Tests (envtest)

**What envtest provides:** Real etcd + kube-apiserver locally, no full cluster.

```bash
KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test ./test/integration/... -v
```

Key test files:
- `test/integration/controller_test.go`:
  - Create IgnitionSync → PVC created with owner ref
  - Delete IgnitionSync → PVC garbage collected
  - Update spec.git.ref → reconcile triggered
  - Create pod with annotation → discoveredGateways updated
  - Delete pod → gateway removed from status
  - Multiple CRs in same namespace → isolated
  - Paused CR → no sync, condition Paused
  - Status conditions transition correctly
- `test/integration/webhook_test.go`:
  - Pod with inject annotation → sidecar added
  - Pod without annotation → unchanged
  - Pod in non-labeled namespace → unchanged

### Tier 3: E2E Tests (kind-dev)

**Full cluster test with real deployments.**

```bash
# Setup
./hack/kind-with-certmanager.sh    # Create kind cluster + cert-manager
make docker-build IMG=ignition-sync:e2e
kind load docker-image ignition-sync:e2e --name dev
helm install ignition-sync charts/ignition-sync/ -n ignition-sync-system --create-namespace

# Run
go test ./test/e2e/... -v -timeout 600s
```

Key test cases:
- `test/e2e/basic_sync_test.go`:
  1. Create IgnitionSync CR pointing to test git repo
  2. Verify PVC created
  3. Verify repo cloned (check ConfigMap ignition-sync-metadata-{crName})
  4. Verify RepoCloned condition = True
- `test/e2e/webhook_injection_test.go`:
  1. Create IgnitionSync CR
  2. Create namespace with injection label
  3. Create pod with annotations
  4. Verify sidecar container present
  5. Verify volumes mounted
- `test/e2e/webhook_receiver_test.go`:
  1. Send POST /webhook/{ns}/{cr} with valid HMAC
  2. Verify `sync.ignition.io/requested-ref` annotation set on CR
  3. Verify reconcile triggered

### Tier 4: Agent E2E Tests

**Test agent in a pod with mock Ignition API.**

```bash
# Deploy mock Ignition API (simple HTTP server)
kubectl apply -f test/e2e/fixtures/mock-ignition-api.yaml

# Deploy agent pod with test PVC
kubectl apply -f test/e2e/fixtures/agent-test-pod.yaml

# Verify sync completed
kubectl exec agent-test-pod -c sync-agent -- cat /ignition-data/projects/site/view.json
```

### Mock Ignition Gateway API

```go
// test/mocks/ignition_server.go
func NewMockIgnitionServer() *httptest.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("/data/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]string{"state": "RUNNING"})
    })
    mux.HandleFunc("/data/api/v1/scan/projects", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    mux.HandleFunc("/data/api/v1/scan/config", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })
    return httptest.NewServer(mux)
}
```

### Mock Git Server (for E2E)

```bash
# In kind cluster, deploy gitea as test git server
helm install gitea gitea-charts/gitea \
  --set gitea.admin.username=test \
  --set gitea.admin.password=test \
  --set persistence.size=1Gi \
  -n test-infra --create-namespace

# Create test repo via Gitea API
curl -X POST http://gitea.test-infra.svc:3000/api/v1/user/repos \
  -H "Content-Type: application/json" \
  -u test:test \
  -d '{"name": "test-ignition-app", "auto_init": true}'
```

### CI Integration

```yaml
# .github/workflows/ci.yaml
name: CI
on: [push, pull_request]
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - uses: golangci/golangci-lint-action@v4

  unit-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: make test

  integration-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: make test-integration

  e2e-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - uses: helm/kind-action@v1
        with: { cluster_name: e2e }
      - run: make test-e2e
```

---

## Test Environment Setup

### kind-dev Cluster Preparation

```bash
#!/bin/bash
# hack/kind-with-certmanager.sh

# Use existing kind-dev cluster
kubectl config use-context kind-dev

# Install cert-manager (required for webhook TLS)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
kubectl wait --for=condition=Available deployment/cert-manager -n cert-manager --timeout=120s
kubectl wait --for=condition=Available deployment/cert-manager-webhook -n cert-manager --timeout=120s

# Create test namespace with injection label
kubectl create ns ignition-test || true
kubectl label ns ignition-test ignition-sync.io/injection-enabled=true --overwrite

# Install NFS provisioner for RWX PVCs (kind doesn't have RWX by default)
helm repo add nfs-ganesha-server-and-external-provisioner \
  https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner/
helm install nfs-provisioner nfs-ganesha-server-and-external-provisioner/nfs-server-provisioner \
  --set persistence.enabled=true \
  --set persistence.size=10Gi \
  -n kube-system

echo "kind-dev cluster ready for E2E testing"
```

### vcluster for Isolated Testing

```bash
# Create vcluster for isolated agent testing
vcluster create agent-test --connect=false
vcluster connect agent-test

# Deploy mock services
kubectl apply -f test/e2e/fixtures/mock-ignition-api.yaml
kubectl apply -f test/e2e/fixtures/test-pvc.yaml

# Run agent tests
go test ./test/e2e/agent_test.go -v

# Cleanup
vcluster disconnect
vcluster delete agent-test
```

---

## Makefile Targets Summary

```makefile
# Development
make generate          # Generate DeepCopy methods
make manifests         # Generate CRD, RBAC, Webhook YAML
make build             # Build controller binary
make build-agent       # Build agent binary
make run               # Run controller locally against cluster

# Testing
make test              # Unit + integration tests (envtest)
make test-unit         # Unit tests only
make test-integration  # Integration tests (envtest)
make test-e2e          # E2E tests (requires kind-dev)
make lint              # golangci-lint

# Docker
make docker-build      # Build controller image
make docker-build-agent # Build agent image
make docker-push       # Push images

# Deployment
make install           # Install CRDs into cluster
make uninstall         # Remove CRDs
make deploy            # Deploy operator via kustomize
make undeploy          # Remove operator

# Helm
make helm-lint         # Lint Helm chart
make helm-template     # Render templates
make helm-install      # Install via Helm
```

---

## Implementation Order Summary

| Order | Phase | Estimated Files | Key Deliverable |
|-------|-------|-----------------|-----------------|
| 1 | Phase 0: Scaffolding | ~10 | `make test` passes |
| 2 | Phase 1: CRD Types | ~3 | Full CRD with validation |
| 3 | Phase 2: PVC, Git & Finalizer | ~6 | Finalizer, PVC, non-blocking git, ConfigMap metadata |
| 4 | Phase 3: Discovery | ~4 | Pods discovered, ConfigMap status, watch predicates |
| 5 | Phase 3A: SyncProfile | ~5 | SyncProfile CRD, validation, IgnitionSync simplification |
| 6 | Phase 4: Webhook | ~8 | Sidecar injected |
| 7 | Phase 5: Agent | ~10 | Profile-driven sync, scan API, distroless |
| 8 | Phase 6: Receiver | ~4 | Annotation-based trigger, constant-time HMAC |
| 9 | Phase 7: Helm Chart | ~15 | `helm install` + NetworkPolicy + PDB |
| 10 | Phase 8: Metrics | ~4 | Prometheus scraping |
| 11 | Phase 9: Advanced | ~10+ | Bidirectional, snapshots |

### Critical Path

```
Phase 0 → Phase 1 → Phase 2 → Phase 3 → Phase 3A → Phase 5 (agent uses profile mappings)
                                    ↓
                               Phase 4 (webhook can parallel with 3A and 5)
                                    ↓
                               Phase 6 → Phase 7 → Phase 8
```

Phase 3A slots between gateway discovery (Phase 3) and the agent (Phase 5) because the agent needs SyncProfile to know what files to sync. Phases 4 and 3A can be developed in parallel since the webhook just injects the sidecar spec and doesn't depend on SyncProfile types. Phase 6 can also parallel with Phase 5.

---

## Key Go Dependencies

```
k8s.io/api                          # Core K8s types
k8s.io/apimachinery                 # Meta types, runtime
k8s.io/client-go                    # K8s client
sigs.k8s.io/controller-runtime      # Controller framework
github.com/go-git/go-git/v5         # Pure-Go git client
github.com/go-logr/zapr             # Structured logging (zap)
go.uber.org/zap                     # Logging backend
github.com/prometheus/client_golang  # Prometheus metrics
github.com/bmatcuk/doublestar/v4    # ** glob patterns (Go's filepath.Match lacks **)
github.com/stretchr/testify         # Test assertions
github.com/onsi/gomega              # Async test assertions (Eventually)
```
