<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-sync-agent.md, 08-deployment-operations.md, 10-enterprise-examples.md -->

# Ignition Sync Operator — Security, Testing & Roadmap

## Testing Strategy

### Unit Tests

- Go unit tests for all controller logic, webhook mutation, status calculation
- Table-driven tests for webhook payload parsing (ArgoCD, Kargo, GitHub, generic)
- Template tests for annotation parsing and env var generation

### Integration Tests (envtest)

- Controller reconciliation with fake CRs and pods
- Webhook injection with sample pod specs
- Metadata ConfigMap creation and ownership
- Status condition transitions
- Multi-CR, multi-namespace scenarios

### SyncProfile Tests

- Unit: SyncProfile validation (empty mappings rejected, path traversal rejected, valid spec accepted)
- Unit: 3-tier precedence resolution (annotation > profile > IgnitionSync > defaults)
- Integration (envtest): SyncProfile create → Accepted condition set
- Integration: Pod with `sync-profile` annotation → profile resolved correctly
- Integration: Pod without `sync-profile` → falls back to `service-path` annotation (2-tier mode)
- Integration: Profile deletion → graceful degradation, warning logged
- Integration: Profile update → affected gateways re-synced
- Integration: Mapping order enforcement (later overlays earlier)
- Integration: Exclude pattern merging (profile + IgnitionSync global)

### End-to-End Tests

- Kind or k3d cluster with cert-manager
- Deploy operator, create CR + SyncProfile, create annotated pods
- Verify sidecar injection, ref resolution, metadata ConfigMap, agent clone, file sync
- Verify SyncProfile mappings applied correctly by agent
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
- Git auth secrets are injected into agent sidecar pods (not the controller), since agents perform the clone operations; the controller only runs ls-remote for ref resolution

**External Secret Manager Integration**
- Support for HashiCorp Vault, AWS Secrets Manager, Azure Key Vault via external-secrets operator
- Example: Agent reads git key from Vault-synced Secret at clone time, never storing it locally
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
# Allow controller → git remotes for ls-remote only (ref resolution, not full clone)
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
# Allow agent sidecar → git remotes (egress)
# Agents perform full git clone operations and need egress to git remotes
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: agent-allow-git-egress
spec:
  podSelector:
    matchLabels:
      ignition-sync.io/inject: "true"
  policyTypes:
    - Egress
  egress:
    - ports:
        - protocol: TCP
          port: 22   # SSH for git
        - protocol: TCP
          port: 443  # HTTPS for git

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
- **SyncProfile CRD** — ordered source→destination mappings, deployment mode overlays, 3-tier config precedence (see [04-sync-profile.md](04-sync-profile.md))
- Webhook-driven updates via ArgoCD, Kargo, GitHub (annotation-based, not spec mutation)
- Finalizer-based cleanup for CR deletion
- ConfigMap-only controller-agent signaling
- Security: scoped RBAC, constant-time HMAC, NetworkPolicy, distroless images, webhook TLS
- Observability: metrics, events, kubectl integration
- Ignition-aware features: fire-and-forget scan API, INITIAL_SYNC_DONE flag, .resources/ merge protection
- Doublestar glob matching, targeted JSON patching, service-path validation

### v1.1 (Near-term)
- Pre/post-sync hooks (replaces `normalize` and `siteNumber` functionality)
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
| PVC eliminated: agent clones to local emptyDir, controller uses ls-remote only | Agent 3 | Storage Strategy |
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
