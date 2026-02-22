<!-- Part of: Stoker Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-stoker-agent.md, 09-security-testing-roadmap.md, 10-enterprise-examples.md -->

# Stoker — Deployment, Operations & Observability

## Helm Chart

The operator ships as a standard Helm chart:

```
charts/stoker/
├── Chart.yaml                    # type: application
├── values.yaml
├── crds/
│   └── stoker.io_stokers.yaml
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
helm install stoker ia/stoker \
  --namespace stoker-system \
  --create-namespace
```

### Minimal values.yaml

```yaml
controller:
  replicas: 2
  image:
    repository: ghcr.io/ia-eknorr/stoker-controller
    tag: "1.0.0"
  # Restrict to specific namespaces (empty = all)
  watchNamespaces: []
  # Restrict to CRs with specific labels
  watchLabelSelector: ""

webhook:
  replicas: 2
  image:
    repository: ghcr.io/ia-eknorr/stoker-controller
    tag: "1.0.0"
  # cert-manager issuer for webhook TLS
  certManager:
    issuerRef:
      name: selfsigned-issuer
      kind: Issuer

agent:
  image:
    repository: ghcr.io/ia-eknorr/stoker-agent
    tag: "1.0.0"

# Global defaults applied to all Stoker CRs (overridable per CR)
defaults:
  polling:
    interval: 60s
```

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
      url: "https://validate.example.com/stoker"
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
        keyPrefix: "stoker/"
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
kubectl patch stoker proveit-sync -n site1 -p \
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

### Ref Override Escape Valve

For the ~5% of cases where a dev or test gateway in a production namespace needs to run a different git ref (e.g., a feature branch or release candidate), a **pod-level annotation override** is available. This is intentionally narrow — it bypasses the standard ref resolution pipeline and generates a warning.

**Usage:**

```yaml
# On a dev/test gateway pod only
podAnnotations:
  stoker.io/inject: "true"
  stoker.io/cr-name: "proveit-sync"
  stoker.io/sync-profile: "proveit-site"
  stoker.io/ref-override: "v2.1.0-rc1"    # escape valve
```

**How it works:**

1. The **controller** does NOT read this annotation. It continues resolving `spec.git.ref` as normal and writing the resolved commit to the metadata ConfigMap.
2. The **agent** sidecar reads the `ref-override` annotation from its own pod (via the Kubernetes downward API or direct pod metadata lookup).
3. If present, the agent uses that ref instead of the metadata ConfigMap's ref when calling `CloneOrFetch`. The agent resolves the ref to a commit SHA independently via its own `ls-remote` call.
4. The agent writes `syncedRef: "v2.1.0-rc1"` (and the resolved commit) to the status ConfigMap, as it would for any sync.
5. The controller detects the skew during its next reconcile: gateway's `syncedRef` differs from `lastSyncRef`. It sets a `RefSkew` warning condition on the Stoker CR:

```text
Warning  RefSkew  Stoker/proveit-sync  Gateway dev-gw running v2.1.0-rc1, expected 2.0.0
```

**When to use:**

- Validating a release candidate on a dedicated dev/test gateway before updating the CR's ref for all gateways.
- Running a feature branch on a non-production gateway for integration testing.
- Hotfixing a single gateway during an incident (with explicit approval).

**When NOT to use:**

- Production canary rollouts — use `spec.deployment.strategy: canary` with stages instead.
- Permanent multi-version setups — use separate Stoker CRs.
- Multi-site rollouts — use separate namespaces with separate CRs.

**Removing the override:**

Delete the annotation (via Helm values change or `kubectl annotate pod ... stoker.io/ref-override-`). On the next sync cycle, the agent falls back to the metadata ConfigMap's ref, and the gateway converges to the CR's version. The `RefSkew` condition clears on the next reconcile.

**Interaction with deployment stages:**

The ref-override annotation bypasses deployment stage ordering. A pod with `ref-override` syncs immediately to the overridden ref regardless of which stage it belongs to. This is intentional — the escape valve is for exceptional cases where normal staging does not apply.

**Security and audit:**

- The annotation is visible in `kubectl describe pod` and in the Kubernetes audit log.
- The `RefSkew` warning condition ensures the skew is not silent — it appears in `kubectl get stk` and in any alerting configured on Stoker conditions.
- RBAC can restrict who can set pod annotations in production namespaces.

---

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
- Report stored locally on the agent's emptyDir at `/repo/.sync-status/{gatewayName}-diff-{timestamp}.json`:
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
| `stoker_reconcile_total` | Counter | Total reconciliations by CR and result |
| `stoker_reconcile_duration_seconds` | Histogram | Time per reconciliation |
| `stoker_ref_resolve_duration_seconds` | Histogram | Time for git ls-remote ref resolution |
| `stoker_agent_clone_duration_seconds` | Histogram | Time for agent git clone/fetch operations |
| `stoker_webhook_received_total` | Counter | Webhooks received by source type |
| `stoker_gateways_discovered` | Gauge | Number of gateways per CR |
| `stoker_gateways_synced` | Gauge | Number of synced gateways per CR |
| `stoker_last_sync_timestamp` | Gauge | Unix timestamp of last successful sync |
| `stoker_sync_duration_seconds` | Histogram | Time to complete sync per gateway |
| `stoker_files_changed_total` | Counter | Total files changed per CR |
| `stoker_bidirectional_prs_created` | Counter | PRs created for gateway changes |
| `stoker_scan_api_duration_seconds` | Histogram | Time for Ignition scan API completion |
| `stoker_rollback_triggered_total` | Counter | Number of auto-rollbacks performed |

### Events

The controller emits Kubernetes Events on the Stoker CR:

```
Normal   RefResolved      Stoker/proveit-sync   Resolved ref 2.0.0 to abc123f
Normal   RefUpdated       Stoker/proveit-sync   Updated ref from 1.9.0 to 2.0.0 (via webhook)
Normal   SyncCompleted    Stoker/proveit-sync   All 5 gateways synced successfully
Warning  SyncFailed       Stoker/proveit-sync   Gateway area2 failed to sync: rsync error code 23
Normal   PRCreated        Stoker/proveit-sync   Created PR #42 for gateway changes on site
Warning  ConflictDetected Stoker/proveit-sync   File config.json modified in both git and gateway site
```

### kubectl Integration

```bash
# List all syncs across the cluster
kubectl get stokers -A

# NAMESPACE     NAME            REF     GATEWAYS   SYNCED   AGE
# site1         proveit-sync    2.0.0   5          5        2d
# site2         proveit-sync    2.0.0   5          5        2d
# public-demo   demo-sync       main    6          6        30d

# Describe for detailed status
kubectl describe stoker proveit-sync -n site1

# Quick status check
kubectl get stoker proveit-sync -n site1 -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
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
  name: stoker-grafana-dashboard
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "Stoker",
        "panels": [
          {
            "title": "Sync Status by Gateway",
            "targets": [{"expr": "stoker_gateways_synced / stoker_gateways_discovered"}]
          },
          {
            "title": "Sync Duration Trend",
            "targets": [{"expr": "stoker_sync_duration_seconds"}]
          },
          {
            "title": "Webhook Received Rate",
            "targets": [{"expr": "rate(stoker_webhook_received_total[5m])"}]
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
kubectl get stk proveit-sync -n site1 -o json | \
  jq '.status.discoveredGateways[] | select(.name=="site")'

# Output includes:
# - Files changed count
# - Projects affected
# - Last diff report timestamp
# - Detailed diff stored locally on the agent's emptyDir at /repo/.sync-status/{gatewayName}-diff-{timestamp}.json
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

With the agent-based architecture, go-git memory is an agent concern, not a controller concern. The controller only runs `ls-remote` to resolve refs, which has negligible memory overhead. Each agent sidecar clones its repo independently into a local emptyDir, so memory usage is per-pod rather than centralized on the controller. This means:

- Controller memory stays flat regardless of repo size or count
- Agent memory scales with the size of the individual repo it clones (set `resources.limits.memory` on the agent container accordingly)
- No single point of memory pressure — large repos only affect the specific gateway pod running that agent
- v1.1 will add optional native `git` CLI backend for memory-constrained agent environments

### Extension Points (v1)

The operator is designed with future extensibility in mind. While these interfaces are not public in v1, the internal architecture separates concerns for later extraction:

- **Source interface** — git is the only implementation in v1, but the internal `Source` interface abstracts fetch/checkout operations. v1.1+ may add OCI registry and S3 sources.
- **Sync strategy interface** — the merge-based sync is one strategy. The interface allows alternative strategies (copy-on-write, bind mount) for specialized environments.
- **Notification interface** — webhook receiver is one trigger. The interface allows additional triggers (NATS, MQTT, AWS SNS) without controller changes.

---

