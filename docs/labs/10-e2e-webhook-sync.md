# Lab 10 — E2E Webhook Sync

## Objective

Validate the complete end-to-end flow with **automatic sidecar injection** via the mutating webhook: operator deployment → webhook injection → file sync → API verification → git ref update → re-sync. This lab proves that a user only needs `podAnnotations` in their Helm values — the webhook handles all sidecar configuration automatically.

Unlike Lab 06 (which manually patches StatefulSets with the sidecar), this lab uses the webhook to inject the `stoker-agent` as a native sidecar. This is the production-intended deployment path.

**Prerequisite:** Complete [00 — Environment Setup](00-environment-setup.md). The operator must be deployed with webhook and cert-manager enabled.

---

## Shared Variables

```bash
export E2E_NS=e2e-webhook
export OPERATOR_NS=stoker-system
export GIT_REPO_URL=https://github.com/ia-eknorr/test-ignition-project.git
export API_TOKEN="ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY"
```

---

## Lab 10.1: Prerequisites

### Purpose
Verify the kind cluster, operator, webhook, and cert-manager are all healthy before starting.

### Steps

```bash
# Verify kind cluster is running
kubectl cluster-info --context kind-dev

# Verify operator is running
kubectl get pods -n $OPERATOR_NS
```

Expected: Controller pod `1/1 Running` with 0 restarts.

```bash
# Verify webhook configuration exists
kubectl get mutatingwebhookconfiguration stoker-pod-injection
```

Expected: Webhook exists with `pod-inject.stoker.io` entry.

```bash
# Verify cert-manager issued the webhook certificate
kubectl get certificate -n $OPERATOR_NS
```

Expected: Certificate `stoker-webhook-cert` with `READY=True`.

```bash
# Verify CRDs are installed
kubectl get crd stokers.stoker.io syncprofiles.stoker.io
```

Expected: Both CRDs present.

### Required Tools

| Tool | Version | Check |
|------|---------|-------|
| `kind` | v0.20+ | `kind version` |
| `kubectl` | v1.29+ | `kubectl version --client` |
| `helm` | v3.14+ | `helm version --short` |
| `jq` | 1.7+ | `jq --version` |
| `curl` | any | `curl --version` |

---

## Lab 10.2: Create Namespace and Secrets

### Purpose
Create an isolated namespace with the injection label, git credentials, and API key secret.

### Steps

```bash
# Create namespace with injection label
kubectl create namespace $E2E_NS
kubectl label namespace $E2E_NS stoker.io/injection=enabled
```

Verify the label:
```bash
kubectl get namespace $E2E_NS --show-labels
```

Expected: Labels include `stoker.io/injection=enabled`.

```bash
# Create git token secret (for agent to clone the repo)
kubectl create secret generic git-token-secret -n $E2E_NS \
  --from-file=token=secrets/github-token

# Create API key secret (for agent → gateway API calls)
kubectl create secret generic ignition-api-key -n $E2E_NS \
  --from-literal=apiKey="ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY"

# Create API token config (for Ignition gateway to recognize the API key)
kubectl create configmap ignition-api-token-config -n $E2E_NS \
  --from-file=config.json=<(cat <<'JSONEOF'
{
  "profile": {
    "secureChannelRequired": false,
    "securityLevels": [
      {"children": [], "description": "Represents a user who has been authenticated by the system.", "name": "Authenticated"},
      {"children": [], "name": "ApiToken"}
    ],
    "timestamp": 1769044485311,
    "type": "basic-token"
  },
  "settings": {
    "tokenHash": "PnEG_dp5qpV20att_1x2wr7OWIsLZGzuMUggzjl4BOY"
  }
}
JSONEOF
) \
  --from-file=resource.json=<(cat <<'JSONEOF'
{
  "scope": "A",
  "description": "",
  "version": 1,
  "restricted": false,
  "overridable": true,
  "files": ["config.json"],
  "attributes": {
    "uuid": "371e6af8-d275-4923-af95-74362eb6662f",
    "enabled": true
  }
}
JSONEOF
)
```

Verify:
```bash
kubectl get secret,configmap -n $E2E_NS
```

Expected: `git-token-secret`, `ignition-api-key`, `ignition-api-token-config` all present.

---

## Lab 10.3: Deploy Stoker CR and SyncProfiles

### Purpose
Create the Stoker CR and per-gateway SyncProfiles. The controller should resolve the git ref before any gateways are deployed.

### Steps

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: e2e-sync
  namespace: e2e-webhook
spec:
  git:
    repo: https://github.com/ia-eknorr/test-ignition-project.git
    ref: main
    auth:
      token:
        secretRef:
          key: token
          name: git-token-secret
  gateway:
    apiKeySecretRef:
      key: apiKey
      name: ignition-api-key
    port: 8088
    tls: false
---
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: blue-profile
  namespace: e2e-webhook
spec:
  mappings:
    - source: "shared/config/resources/external"
      destination: "config/resources/external"
      type: dir
    - source: "services/ignition-blue/config/resources/core"
      destination: "config/resources/core"
      type: dir
      required: true
    - source: "services/ignition-blue/projects"
      destination: "projects"
      type: dir
      required: true
  deploymentMode:
    name: dev
    source: "services/ignition-blue/config/resources/dev"
  vars:
    environment: "test"
    gateway: "blue"
---
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: red-profile
  namespace: e2e-webhook
spec:
  mappings:
    - source: "shared/config/resources/external"
      destination: "config/resources/external"
      type: dir
    - source: "services/ignition-red/config/resources/core"
      destination: "config/resources/core"
      type: dir
      required: true
    - source: "services/ignition-red/projects"
      destination: "projects"
      type: dir
      required: true
  deploymentMode:
    name: dev
    source: "services/ignition-red/config/resources/dev"
  vars:
    environment: "test"
    gateway: "red"
EOF
```

### What to Verify

1. **Ref resolved:**
   ```bash
   kubectl get stoker e2e-sync -n $E2E_NS
   ```
   Expected: `REF=main`, `Ready` column populated (no gateways yet, so gateway-related fields may show `0/0`).

2. **Both SyncProfiles accepted:**
   ```bash
   kubectl get syncprofile -n $E2E_NS
   ```
   Expected: `blue-profile` and `red-profile` with `ACCEPTED=True`, `MODE=dev`.

3. **Metadata ConfigMap created:**
   ```bash
   kubectl get configmap stoker-metadata-e2e-sync -n $E2E_NS -o json | jq '.data'
   ```
   Expected: `commit` is a 40-char hex SHA, `ref` is `main`, `trigger` is an RFC3339 timestamp.

---

## Lab 10.4: Deploy Ignition Gateways with Webhook Injection

### Purpose
Deploy two Ignition gateways using **only `podAnnotations`** to trigger automatic sidecar injection. This is the key difference from Lab 06 — no manual StatefulSet patching.

### Key Deployment Requirement: Commissioning Workaround

The Ignition Helm chart runs a commissioning process on first boot that creates default security-properties **without** the `apiKeys` permission. This overwrites any agent-synced config.

Since the agent runs as a native sidecar (init container with `restartPolicy: Always`), it syncs all config files to disk **before** the gateway container starts. To preserve this synced config, we skip commissioning by pre-creating an empty `commissioning.json`:

```yaml
gateway:
  preconfigure:
    additionalCmds:
      - |
        [ -f "/data/commissioning.json" ] || echo "{}" > /data/commissioning.json
```

This tells Ignition "commissioning already happened" so it reads the agent-synced config as its initial state instead of running the default commissioning flow.

### Key Deployment Requirement: podAnnotations Location

The Ignition Helm chart uses **top-level** `podAnnotations:`, not `gateway.podAnnotations:`. This is where webhook injection annotations go.

### Key Deployment Requirement: UID Inheritance

The webhook intentionally omits `RunAsUser` on the injected sidecar. This means the agent inherits the pod-level UID set by the Helm chart (UID 2003 for Ignition). Files written to the shared data volume are owned by the same user as the gateway container, preventing permission errors.

### Steps

```bash
# Common helm args
HELM_COMMON=(
  --set commissioning.acceptIgnitionEULA=true
  --set commissioning.edition=standard
  --set certManager.enabled=false
  --set ingress.enabled=false
  --set gateway.replicas=1
  --set gateway.dataVolumeStorageSize=5Gi
  --set gateway.persistentVolumeClaimRetentionPolicy=Delete
  --set gateway.resourcesEnabled=true
  --set gateway.resources.requests.cpu=500m
  --set gateway.resources.requests.memory=1Gi
  --set gateway.resources.limits.cpu=1
  --set gateway.resources.limits.memory=2Gi
  --set 'gateway.volumes[0].name=api-token-config'
  --set 'gateway.volumes[0].configMap.name=ignition-api-token-config'
  --set 'gateway.preconfigureVolumeMounts[0].name=api-token-config'
  --set 'gateway.preconfigureVolumeMounts[0].mountPath=/api-token-config'
  --set 'gateway.preconfigureVolumeMounts[0].readOnly=true'
  --set 'gateway.preconfigure.additionalCmds[0]=mkdir -p /data/local/resources/core/ignition/api-token/ignition-api-key && cp /api-token-config/config.json /data/local/resources/core/ignition/api-token/ignition-api-key/config.json && cp /api-token-config/resource.json /data/local/resources/core/ignition/api-token/ignition-api-key/resource.json && echo "API token seeded"'
  --set 'gateway.preconfigure.additionalCmds[1]=[ -f "/data/commissioning.json" ] || echo "{}" > /data/commissioning.json'
)

# Deploy blue gateway with webhook injection annotations
helm install ignition-blue inductiveautomation/ignition -n $E2E_NS \
  "${HELM_COMMON[@]}" \
  --set service.type=NodePort \
  --set service.nodePorts.http=30088 \
  --set service.nodePorts.https=30043 \
  --set 'podAnnotations.stoker\.io/inject=true' \
  --set 'podAnnotations.stoker\.io/sync-profile=blue-profile' \
  --set 'podAnnotations.stoker\.io/gateway-name=ignition-blue'

# Deploy red gateway with webhook injection annotations
helm install ignition-red inductiveautomation/ignition -n $E2E_NS \
  "${HELM_COMMON[@]}" \
  --set service.type=NodePort \
  --set service.nodePorts.http=30089 \
  --set service.nodePorts.https=30044 \
  --set 'podAnnotations.stoker\.io/inject=true' \
  --set 'podAnnotations.stoker\.io/sync-profile=red-profile' \
  --set 'podAnnotations.stoker\.io/gateway-name=ignition-red'
```

Wait for both pods to be ready (the startup probe on the sidecar gates readiness):

```bash
kubectl wait --for=condition=Ready pod/ignition-blue-gateway-0 -n $E2E_NS --timeout=300s
kubectl wait --for=condition=Ready pod/ignition-red-gateway-0 -n $E2E_NS --timeout=300s
```

Verify both pods show 2/2 containers (gateway + native sidecar):

```bash
kubectl get pods -n $E2E_NS
```

Expected:
```
NAME                        READY   STATUS    RESTARTS   AGE
ignition-blue-gateway-0     2/2     Running   0          ...
ignition-red-gateway-0      2/2     Running   0          ...
```

---

## Lab 10.5: Verify Sidecar Injection

### Purpose
Confirm the webhook injected the `stoker-agent` sidecar with correct configuration — env vars, volume mounts, security context, and annotations.

### Steps

1. **Check init containers for `stoker-agent`:**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o jsonpath='{.spec.initContainers[*].name}'
   ```
   Expected: Contains `stoker-agent`.

2. **Check `injected` annotation:**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o jsonpath='{.metadata.annotations.stoker\.io/injected}'
   ```
   Expected: `true`

3. **Verify env vars on the sidecar:**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o jsonpath='{.spec.initContainers[?(@.name=="stoker-agent")].env[*].name}' | tr ' ' '\n'
   ```
   Expected: Includes `POD_NAME`, `POD_NAMESPACE`, `CR_NAME`, `GATEWAY_NAME`, `SYNC_PROFILE`, `REPO_PATH`, `DATA_PATH`, `GATEWAY_PORT`, `GATEWAY_TLS`, `API_KEY_FILE`, `GIT_TOKEN_FILE`, `SYNC_PERIOD`.

4. **Verify CR_NAME was auto-derived (only 1 CR in namespace):**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o jsonpath='{.spec.initContainers[?(@.name=="stoker-agent")].env[?(@.name=="CR_NAME")].value}'
   ```
   Expected: `e2e-sync`

5. **Verify volume mounts:**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o jsonpath='{.spec.initContainers[?(@.name=="stoker-agent")].volumeMounts[*].name}' | tr ' ' '\n'
   ```
   Expected: Includes `sync-repo`, `git-credentials`, `api-key`, and the data volume (`data`).

6. **Verify security context (restricted PSS, no RunAsUser):**
   ```bash
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o json | jq '.spec.initContainers[] | select(.name=="stoker-agent") | .securityContext'
   ```
   Expected: `runAsNonRoot: true`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`. No `runAsUser` field — inherits pod-level UID.

7. **Verify data volume mount path matches gateway container:**
   ```bash
   # Agent data path
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o json | jq -r '.spec.initContainers[] | select(.name=="stoker-agent") | .env[] | select(.name=="DATA_PATH") | .value'

   # Gateway data mount
   kubectl get pod ignition-blue-gateway-0 -n $E2E_NS \
     -o json | jq -r '.spec.containers[0].volumeMounts[] | select(.name=="data") | .mountPath'
   ```
   Expected: Both paths match (e.g., `/usr/local/bin/ignition/data`).

8. **Repeat for red gateway:**
   ```bash
   kubectl get pod ignition-red-gateway-0 -n $E2E_NS \
     -o jsonpath='{.metadata.annotations.stoker\.io/injected}'
   kubectl get pod ignition-red-gateway-0 -n $E2E_NS \
     -o jsonpath='{.spec.initContainers[?(@.name=="stoker-agent")].env[?(@.name=="SYNC_PROFILE")].value}'
   ```
   Expected: `injected=true`, `SYNC_PROFILE=red-profile`.

---

## Lab 10.6: Verify Agent Sync

### Purpose
Confirm the agent successfully cloned the repo, synced files, and reported status.

### Steps

1. **Check agent logs for initial sync:**
   ```bash
   kubectl logs ignition-blue-gateway-0 -n $E2E_NS -c stoker-agent --tail=50
   ```
   Look for: clone success, sync mappings applied, startup probe passed.

2. **Check agent logs on red:**
   ```bash
   kubectl logs ignition-red-gateway-0 -n $E2E_NS -c stoker-agent --tail=50
   ```

3. **Verify status ConfigMap shows sync complete:**
   ```bash
   kubectl get configmap stoker-status-e2e-sync -n $E2E_NS -o json | jq '.data'
   ```
   Look for per-gateway status entries showing `Synced`.

4. **Verify Stoker CR shows both gateways synced:**
   ```bash
   kubectl get stoker e2e-sync -n $E2E_NS
   ```
   Expected: `GATEWAYS: 2/2 gateways synced`, `SYNCED: True`, `READY: True`.

5. **Verify discovered gateways in CR status:**
   ```bash
   kubectl get stoker e2e-sync -n $E2E_NS -o json | \
     jq '.status.discoveredGateways[] | {name, syncStatus, syncProfile, syncedCommit}'
   ```
   Expected: Both gateways with `syncStatus: Synced`, correct `syncProfile`, and a valid `syncedCommit` SHA.

6. **Check CR conditions:**
   ```bash
   kubectl get stoker e2e-sync -n $E2E_NS -o json | \
     jq '.status.conditions[] | {type, status, reason}'
   ```
   Expected: `RefResolved: True`, `AllGatewaysSynced: True`, `Ready: True`.

---

## Lab 10.7: Kubernetes Events Verification

### Purpose
Verify the operator, webhook receiver, and agent emit K8s events for key state transitions. Events are visible via `kubectl describe` and `kubectl get events`, providing an operator-friendly troubleshooting signal.

### Steps

1. **Verify SyncCompleted events from agent:**
   ```bash
   kubectl get events -n $E2E_NS --field-selector reason=SyncCompleted
   ```
   Expected: At least one `Normal` event per gateway (two total), with messages like `Sync completed on ignition-blue: commit <sha>, N file(s) changed`.

2. **Verify GatewaysDiscovered event from controller:**
   ```bash
   kubectl get events -n $E2E_NS --field-selector reason=GatewaysDiscovered
   ```
   Expected: At least one `Normal` event showing `Discovered N gateway(s)`.

3. **Test WebhookReceived event — trigger via webhook:**
   ```bash
   # Port-forward the webhook receiver
   kubectl port-forward -n $OPERATOR_NS deploy/stoker-operator-controller-manager 9444:9444 &
   WH_PF_PID=$!
   sleep 2

   # Compute HMAC signature (skip if HMAC is not configured)
   HMAC_SECRET=$(kubectl get secret webhook-hmac -n $OPERATOR_NS \
     -o jsonpath='{.data.webhook-secret}' 2>/dev/null | base64 -d)
   BODY='{"ref":"v99.0.0-event-test"}'
   if [ -n "$HMAC_SECRET" ]; then
     SIG="sha256=$(echo -n "$BODY" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')"
     HMAC_HEADER="-H X-Hub-Signature-256:${SIG}"
   else
     HMAC_HEADER=""
   fi

   # Send webhook
   curl -s -X POST "http://localhost:9444/webhook/$E2E_NS/e2e-sync" \
     -H "Content-Type: application/json" \
     $HMAC_HEADER \
     -d "$BODY"
   # Expected: HTTP 202, {"accepted":true,"ref":"v99.0.0-event-test"}

   kill $WH_PF_PID 2>/dev/null
   ```

   Verify the event:
   ```bash
   kubectl get events -n $E2E_NS --field-selector reason=WebhookReceived
   ```
   Expected: `Normal` event with message `Webhook from generic, ref "v99.0.0-event-test"`.

4. **Test Paused event — pause and unpause the CR:**
   ```bash
   kubectl patch stoker e2e-sync -n $E2E_NS --type merge -p '{"spec":{"paused":true}}'
   sleep 5
   kubectl get events -n $E2E_NS --field-selector reason=Paused
   ```
   Expected: `Normal` event with message `Reconciliation paused`.

   Unpause:
   ```bash
   kubectl patch stoker e2e-sync -n $E2E_NS --type merge -p '{"spec":{"paused":false}}'
   ```

5. **Test RefResolutionFailed event — set an invalid ref:**
   ```bash
   kubectl annotate stoker e2e-sync -n $E2E_NS \
     stoker.io/requested-ref=nonexistent-tag-xyz --overwrite
   sleep 10
   kubectl get events -n $E2E_NS --field-selector reason=RefResolutionFailed
   ```
   Expected: `Warning` event with message `Ref resolution failed: ref "nonexistent-tag-xyz" not found...`.

   Clean up the invalid ref:
   ```bash
   kubectl annotate stoker e2e-sync -n $E2E_NS stoker.io/requested-ref-
   ```

6. **Test SyncProfile validation events — create invalid profiles:**
   ```bash
   # ValidationFailed: path traversal
   cat <<'EOF' | kubectl apply -f -
   apiVersion: stoker.io/v1alpha1
   kind: SyncProfile
   metadata:
     name: test-validation-fail
     namespace: e2e-webhook
   spec:
     mappings:
       - source: "../../etc/passwd"
         destination: "projects/test"
   EOF

   sleep 5
   kubectl get events -n $E2E_NS --field-selector reason=ValidationFailed
   ```
   Expected: `Warning` event with `path traversal (..) not allowed`.

   ```bash
   # CycleDetected + DependencyNotFound: circular dependency
   cat <<'EOF' | kubectl apply -f -
   apiVersion: stoker.io/v1alpha1
   kind: SyncProfile
   metadata:
     name: test-cycle-a
     namespace: e2e-webhook
   spec:
     mappings:
       - source: "projects/test"
         destination: "projects/test"
     dependsOn:
       - profileName: test-cycle-b
   ---
   apiVersion: stoker.io/v1alpha1
   kind: SyncProfile
   metadata:
     name: test-cycle-b
     namespace: e2e-webhook
   spec:
     mappings:
       - source: "projects/test"
         destination: "projects/test"
     dependsOn:
       - profileName: test-cycle-a
   EOF

   sleep 5
   kubectl get events -n $E2E_NS --field-selector reason=CycleDetected
   kubectl get events -n $E2E_NS --field-selector reason=DependencyNotFound
   ```
   Expected: `CycleDetected` warning on one profile, `DependencyNotFound` warning on the other.

7. **Clean up test profiles:**
   ```bash
   kubectl delete syncprofile test-validation-fail test-cycle-a test-cycle-b -n $E2E_NS 2>/dev/null
   ```

8. **Summary — all expected events:**
   ```bash
   kubectl get events -n $E2E_NS --field-selector involvedObject.kind=Stoker \
     --sort-by=.lastTimestamp
   kubectl get events -n $E2E_NS --field-selector involvedObject.kind=SyncProfile \
     --sort-by=.lastTimestamp
   ```

   | Reason | Type | Emitter | Verified |
   |--------|------|---------|----------|
   | `SyncCompleted` | Normal | Agent | |
   | `WebhookReceived` | Normal | Webhook | |
   | `Paused` | Normal | Controller | |
   | `RefResolutionFailed` | Warning | Controller | |
   | `GatewaysDiscovered` | Normal | Controller | |
   | `ValidationFailed` | Warning | SyncProfile Controller | |
   | `CycleDetected` | Warning | SyncProfile Controller | |
   | `DependencyNotFound` | Warning | SyncProfile Controller | |

   **Note:** `SyncFailed`, `CloneFailed`, and `DesignerSessionsBlocked` events are emitted by the agent on failure conditions. These are difficult to trigger in a healthy lab environment but share the same event emission path as `SyncCompleted`.

---

## Lab 10.8: API Verification

### Purpose
Verify the synced configuration is live on both gateways using the Ignition REST API.

### Steps

```bash
# Port-forward both gateways
kubectl port-forward pod/ignition-blue-gateway-0 8088:8088 -n $E2E_NS &
BLUE_PF_PID=$!
kubectl port-forward pod/ignition-red-gateway-0 8089:8088 -n $E2E_NS &
RED_PF_PID=$!
sleep 5
```

### Run the verification script

```bash
./scripts/verify-gateway.sh http://localhost:8088 ignition-blue blue "#00a3d7"
./scripts/verify-gateway.sh http://localhost:8089 ignition-red  red  "#ff4013"
```

Expected: All checks pass for both gateways.

### Manual spot checks

```bash
# Gateway identity
curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8088/data/api/v1/gateway-info | \
  jq '{name, deploymentMode, ignitionVersion}'

curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8089/data/api/v1/gateway-info | \
  jq '{name, deploymentMode, ignitionVersion}'

# Projects
curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8088/data/api/v1/projects/list | \
  jq '[.items[] | {name, enabled}]'

curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8089/data/api/v1/projects/list | \
  jq '[.items[] | {name, enabled}]'

# Cobranding (per-gateway uniqueness)
curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8088/data/api/v1/resources/singleton/ignition/cobranding | \
  jq -r .config.backgroundColor
# Expected: #00a3d7

curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8089/data/api/v1/resources/singleton/ignition/cobranding | \
  jq -r .config.backgroundColor
# Expected: #ff4013

# Tag providers
curl -s -H "X-Ignition-API-Token: $API_TOKEN" http://localhost:8088/data/api/v1/resources/list/ignition/tag-provider | \
  jq '[.items[] | {name, type: .config.profile.type}]'
```

### Cleanup port-forwards

```bash
kill $BLUE_PF_PID $RED_PF_PID 2>/dev/null
```

---

## Lab 10.9: Git Ref Update and Re-Sync

### Purpose
Push a change to the test repo, verify the controller detects the new commit, and the agent re-syncs the updated config to both gateways.

### Steps

1. **Record current state:**
   ```bash
   COMMIT_BEFORE=$(kubectl get stoker e2e-sync -n $E2E_NS \
     -o jsonpath='{.status.lastSyncCommit}')
   echo "Current commit: $COMMIT_BEFORE"
   ```

2. **Make a change in the test repo:**

   In a separate terminal, go to your local clone of `test-ignition-project` and make a visible change:

   ```bash
   cd /Users/eknorr/IA/code/personal/test-ignition-project

   # Change the blue cobranding color to a test value
   # (edit services/ignition-blue/config/resources/core/ignition/cobranding/config.json)
   # Change backgroundColor from "#00a3d7" to "#00ff00"
   git add -A && git commit -m "test: change blue cobranding for e2e" && git push
   ```

3. **Wait for controller to detect the new commit:**

   The controller polls on its configured interval. Watch for the commit to change:

   ```bash
   # Watch the CR for changes
   kubectl get stoker e2e-sync -n $E2E_NS -w
   ```

   Or poll manually:
   ```bash
   sleep 90  # Wait for poll cycle
   COMMIT_AFTER=$(kubectl get stoker e2e-sync -n $E2E_NS \
     -o jsonpath='{.status.lastSyncCommit}')
   echo "New commit: $COMMIT_AFTER"
   [ "$COMMIT_BEFORE" != "$COMMIT_AFTER" ] && echo "PASS: New commit detected" || echo "FAIL: Commit unchanged"
   ```

4. **Verify metadata ConfigMap updated:**
   ```bash
   kubectl get configmap stoker-metadata-e2e-sync -n $E2E_NS \
     -o jsonpath='{.data.commit}'
   ```
   Expected: Matches `$COMMIT_AFTER`.

5. **Verify agents re-synced:**
   ```bash
   kubectl logs ignition-blue-gateway-0 -n $E2E_NS -c stoker-agent --tail=20
   ```
   Look for: fetch, new commit detected, sync applied.

6. **Verify API reflects the change:**
   ```bash
   kubectl port-forward pod/ignition-blue-gateway-0 8088:8088 -n $E2E_NS &
   BLUE_PF_PID=$!
   sleep 3

   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/resources/singleton/ignition/cobranding | \
     jq -r .config.backgroundColor
   # Expected: #00ff00 (the updated color)

   kill $BLUE_PF_PID 2>/dev/null
   ```

7. **Revert the test change:**
   ```bash
   cd /Users/eknorr/IA/code/personal/test-ignition-project
   git revert HEAD --no-edit && git push
   ```

   Wait for the operator to pick up the revert and verify the original color is restored:
   ```bash
   sleep 90
   kubectl port-forward pod/ignition-blue-gateway-0 8088:8088 -n $E2E_NS &
   BLUE_PF_PID=$!
   sleep 3

   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/resources/singleton/ignition/cobranding | \
     jq -r .config.backgroundColor
   # Expected: #00a3d7 (original color restored)

   kill $BLUE_PF_PID 2>/dev/null
   ```

---

## Lab 10.10: Teardown

### Steps

```bash
# Uninstall gateways
helm uninstall ignition-blue ignition-red -n $E2E_NS

# Wait for pods to terminate
kubectl wait --for=delete pod/ignition-blue-gateway-0 pod/ignition-red-gateway-0 \
  -n $E2E_NS --timeout=60s 2>/dev/null

# Delete PVCs (retained by default)
kubectl delete pvc --all -n $E2E_NS

# Delete Stoker CR and SyncProfiles
kubectl delete stoker,syncprofile --all -n $E2E_NS

# Delete namespace
kubectl delete namespace $E2E_NS
```

Or to **keep the environment** for further testing:
```bash
echo "Skipping teardown — namespace $E2E_NS retained for next iteration"
```

---

## Phase 10 Completion Checklist

| Check | Status |
|-------|--------|
| Operator running with webhook and cert-manager healthy | |
| Namespace labeled with `stoker.io/injection=enabled` | |
| Secrets and ConfigMaps created (git-token, api-key, api-token-config) | |
| Stoker CR resolves ref before gateways deployed | |
| Both SyncProfiles accepted with correct deployment mode | |
| Blue gateway pod has `stoker-agent` init container (webhook injected) | |
| Red gateway pod has `stoker-agent` init container (webhook injected) | |
| `injected: "true"` annotation present on both pods | |
| Agent env vars correct (CR_NAME, SYNC_PROFILE, GATEWAY_NAME, DATA_PATH) | |
| Agent security context is restricted PSS, no explicit RunAsUser | |
| Data volume mount path matches gateway container mount path | |
| Agent logs show successful clone and sync | |
| Status ConfigMap shows both gateways `Synced` | |
| CR shows `2/2 gateways synced`, `Ready: True` | |
| `SyncCompleted` events emitted by both agents | |
| `WebhookReceived` event emitted on webhook trigger | |
| `Paused` event emitted on CR pause transition | |
| `RefResolutionFailed` event emitted on invalid ref | |
| `ValidationFailed` event emitted on bad SyncProfile | |
| `CycleDetected` event emitted on dependency cycle | |
| `DependencyNotFound` event emitted on missing dependency | |
| `verify-gateway.sh` passes for blue (name, project, cobranding, db, tags) | |
| `verify-gateway.sh` passes for red (name, project, cobranding, db, tags) | |
| Git ref update detected by controller (new commit SHA) | |
| Agent re-syncs after ref update | |
| API reflects updated config after re-sync | |
| Revert propagates back to original config | |
| Teardown cleans up all resources | |
| Operator pod has 0 restarts throughout entire lab | |
