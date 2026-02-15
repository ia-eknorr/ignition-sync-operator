<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 03-sync-agent.md, 04-deployment-operations.md, 05-enterprise-examples.md, 06-security-testing-roadmap.md -->

# Ignition Sync Operator — Controller Manager & Storage

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

