<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-sync-agent.md, 08-deployment-operations.md, 09-security-testing-roadmap.md -->

# Ignition Sync Operator — Integration Patterns, Examples & Enterprise

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

> These examples use the [SyncProfile CRD](04-sync-profile.md) for file routing, separating
> infrastructure concerns (IgnitionSync) from file mappings (SyncProfile).

### ProveIt 2026 (5 gateways, 1 site + 4 areas)

```yaml
# IgnitionSync CR — pure infrastructure, no file routing
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
  excludePatterns:
    - "**/.git/"
    - "**/.gitkeep"
  bidirectional:
    enabled: false
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
```

```yaml
# SyncProfile: site gateway role
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
---
# SyncProfile: area gateway role (shared by all 4 areas)
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

```yaml
# Site chart values.yaml — 3 annotations per pod, profiles handle the rest
site:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/sync-profile: "proveit-site"

area1:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "proveit-sync"
      ignition-sync.io/sync-profile: "proveit-area"

# area2, area3, area4 — identical to area1
```

What this replaces in the current site chart:
- `x-git-sync` init container anchor and all `initContainers` blocks
- `x-common` volumes anchor (git-secret, git-volume, sync-scripts, git-config)
- `git-sync-env-*` ConfigMaps (all 5)
- `sync-scripts` ConfigMap
- `git-sync-target-ref` ConfigMap
- `sync-files.sh` and `sync-entrypoint.sh` scripts
- 6+ per-pod annotations (reduced to 3)
- `git-sync-configmap.yaml` template

### Public Demo (2 gateways, replicated frontend)

```yaml
# IgnitionSync CR
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
  polling:
    interval: 30s
  gateway:
    port: 8043
    tls: true
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
  agent:
    image:
      repository: ghcr.io/inductiveautomation/ignition-sync-agent
      tag: "1.0.0"
```

```yaml
# SyncProfiles for each gateway role
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: demo-frontend
  namespace: public-demo
spec:
  mappings:
    - source: "services/ignition-frontend/projects"
      destination: "projects"
    - source: "services/ignition-frontend/config"
      destination: "config"
---
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: demo-backend
  namespace: public-demo
spec:
  mappings:
    - source: "services/ignition-backend/projects"
      destination: "projects"
    - source: "services/ignition-backend/config"
      destination: "config"
```

```yaml
# Public demo chart values
frontend:
  gateway:
    replicas: 5
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "demo-sync"
      ignition-sync.io/sync-profile: "demo-frontend"

backend:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "demo-sync"
      ignition-sync.io/sync-profile: "demo-backend"
```

Note: With 5 frontend replicas, all 5 pods get the sync agent injected, all independently clone the repository to local emptyDir volumes, and all use the same `demo-frontend` SyncProfile. The controller discovers all 5 and tracks each in `status.discoveredGateways`.

### Simple Single Gateway

For a single gateway, SyncProfile is optional — use 2-tier mode with `service-path` annotation:

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
# Simple ignition chart values — 2-tier mode (no SyncProfile)
ignition:
  gateway:
    podAnnotations:
      ignition-sync.io/inject: "true"
      ignition-sync.io/cr-name: "my-sync"
      ignition-sync.io/service-path: "."   # repo root IS the service
```

Or with a minimal SyncProfile:

```yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: single-gw
  namespace: default
spec:
  mappings:
    - source: "."
      destination: "."
```

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

For deployments with 100+ gateways across multiple sites, reducing duplication is critical. SyncProfile already provides reusability at the gateway-role level (e.g., one `proveit-area` profile shared by 4 area gateways). For cross-site reuse, Kustomize overlays or Helm library charts are the recommended pattern:

**Kustomize Base + Overlay Pattern**
```yaml
# base/sync-profile-area.yaml — shared across all sites
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: area
spec:
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"

---
# overlays/site1/kustomization.yaml
resources:
  - ../../base
patchesStrategicMerge:
  - sync-profile-patch.yaml

# overlays/site1/sync-profile-patch.yaml
apiVersion: sync.ignition.io/v1alpha1
kind: SyncProfile
metadata:
  name: area
  namespace: site1
spec:
  excludePatterns:
    - "**/tag-*/System/"
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

