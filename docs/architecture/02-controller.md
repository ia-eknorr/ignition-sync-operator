<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 03-sync-agent.md, 04-deployment-operations.md, 05-enterprise-examples.md, 06-security-testing-roadmap.md -->

# Ignition Sync Operator — Controller Manager & Ref Resolution

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
- `MaxConcurrentReconciles: 5` (configurable) to prevent a single slow ref resolution from blocking all CRs
- `GenerationChangedPredicate` on the primary watch — prevents reconciliation storms from status-only updates
- Pod watch filtered to only pods with `ignition-sync.io/cr-name` annotation
- Context timeout on all git operations (default: 30s)

```
Watch: IgnitionSync CRs (all namespaces, or filtered)
  Predicate: GenerationChangedPredicate (skip status-only updates)
Watch: Pods with annotation ignition-sync.io/cr-name (for gateway discovery)
  Predicate: filter to only annotated pods
Owns: ConfigMaps (for sync metadata)
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
│     - Referenced secrets exist in CR's namespace                 │
├──────────────────────────────────────────────────────────────────┤
│  2. Resolve git ref (non-blocking)                               │
│     - Call `git ls-remote` to resolve spec.git.ref to a commit   │
│       SHA. Single HTTP/SSH call, no clone.                       │
│     - If operation is still in progress, requeue after 5s        │
│     - Update condition: RefResolved = True                       │
│     - On failure: condition RefResolutionFailed, requeue backoff │
├──────────────────────────────────────────────────────────────────┤
│  3. Create/update metadata ConfigMap                             │
│     - Write resolved commit SHA, ref, and trigger timestamp to   │
│       ConfigMap ignition-sync-metadata-{crName}                  │
│     - Agents watch for changes and act on new commits            │
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
│     - Read change manifests from ConfigMap                       │
│       ignition-sync-changes-{crName} (written by agents)         │
│     - Apply conflict resolution strategy                         │
│     - git checkout -b {targetBranch}                             │
│     - Apply changes, commit, push                                │
│     - Create PR via GitHub API (if githubApp auth configured)    │
│     - Clean up processed change manifests                        │
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

## Controller-Agent Communication

The controller and agents communicate exclusively via Kubernetes ConfigMaps. There is no shared PVC — each agent clones the git repository independently to a local emptyDir.

### Ref Resolution

The controller resolves `spec.git.ref` to a commit SHA using `git ls-remote` — a single HTTP/SSH call that returns remote ref→SHA mappings without downloading any objects. This is fast (~100ms), requires no local storage, and uses minimal memory.

### Communication via ConfigMap

**Controller → Agents: Metadata ConfigMap**
- Controller creates ConfigMap `ignition-sync-metadata-{crName}` with resolved commit SHA, ref, and trigger timestamp
- Agents watch the ConfigMap via K8s informer (fast, event-driven)
- When agent sees a new commit, it fetches/checks out that commit from git

**Agents → Controller: Status ConfigMap**
- Agents write sync results to ConfigMap `ignition-sync-status-{crName}`
- Controller reads agent status from ConfigMap during reconciliation
- Each agent writes its status as a key (`{gatewayName}`) in the ConfigMap data

**Bidirectional: Change Manifests ConfigMap**
- When bidirectional sync is enabled, agents write change manifests to ConfigMap `ignition-sync-changes-{crName}`
- Controller reads and processes change manifests during reconciliation

```
ConfigMaps per IgnitionSync CR:
  ignition-sync-metadata-{crName}    # Controller → Agents (commit SHA, ref, trigger timestamp)
  ignition-sync-status-{crName}      # Agents → Controller (per-gateway sync results)
  ignition-sync-changes-{crName}     # Agents → Controller (bidirectional change manifests)
```

**Why no shared PVC:**
- Eliminates RWX storage requirement — works on any cluster, even single-node
- Each agent independently clones, avoiding cross-pod volume sharing issues
- No WaitForFirstConsumer binding problems
- Controller is lightweight — ref resolution via ls-remote, no local filesystem needed
- Agent autonomy — each agent manages its own repo lifecycle

---

## Multi-Repo Support

A single controller instance handles any number of `IgnitionSync` CRs across any number of namespaces. Each CR references its own git repository.

```
Namespace: site1
  IgnitionSync/proveit-sync  →  repo: conf-proveit26-app.git
  IgnitionSync/modules-sync  →  repo: ignition-custom-modules.git

Namespace: site2
  IgnitionSync/proveit-sync  →  repo: conf-proveit26-app.git

Namespace: public-demo
  IgnitionSync/demo-sync     →  repo: publicdemo-all.git
```

Pods reference a specific CR via `ignition-sync.io/cr-name`, so there's no ambiguity when multiple CRs exist in the same namespace. A gateway pod always syncs from exactly one repository.

---
