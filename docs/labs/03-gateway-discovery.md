# Lab 03 — Gateway Discovery & Status

## Objective

Validate gateway discovery against real Ignition gateway pods deployed via the official helm chart. Verify the controller correctly reads annotations, resolves gateway names, collects status from ConfigMaps, computes aggregate conditions (AllGatewaysSynced, Ready), and emits events. Test multi-gateway scenarios including redundancy (primary/backup) patterns.

**Prerequisite:** Complete [02 — Controller Core](02-controller-core.md). The `lab-sync` CR should exist and be in `Cloned` state.

---

## Lab 3.1: Discover the Ignition Gateway Pod

### Purpose
The Ignition gateway pod from environment setup already has `ignition-sync.io/cr-name: lab-sync` annotation. Verify the controller discovers it.

### Steps

```bash
# Verify annotation is on the Ignition pod
kubectl get pod ignition-0 -n lab -o jsonpath='{.metadata.annotations}' | jq '{
  "cr-name": .["ignition-sync.io/cr-name"],
  "gateway-name": .["ignition-sync.io/gateway-name"]
}'

# Check discovered gateways on the CR
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.discoveredGateways'
```

### Expected
- `discoveredGateways` contains one entry with `name: "lab-gateway"`, `podName: "ignition-0"`
- `syncStatus` is `"Pending"` (no agent reporting yet)

### What to Watch For
- If `discoveredGateways` is empty, check the pod's phase — it must be `Running`
- If the pod is `Running` but not discovered, check the annotation exactly matches the CR name

```bash
# Debug: verify pod phase
kubectl get pod ignition-0 -n lab -o jsonpath='{.status.phase}'
```

---

## Lab 3.2: Gateway Name Resolution — Annotation Priority

### Purpose
Verify the three-tier name resolution: annotation > label > pod name.

### Steps

**Test 1 — Annotation name takes priority:**
The Ignition pod has both `ignition-sync.io/gateway-name: lab-gateway` annotation AND `app.kubernetes.io/name: ignition` label. Verify annotation wins:

```bash
kubectl get ignitionsync lab-sync -n lab \
  -o jsonpath='{.status.discoveredGateways[0].name}'
```

Expected: `lab-gateway` (from annotation, not `ignition` from label).

**Test 2 — Remove annotation, label takes over:**

```bash
# Remove gateway-name annotation from StatefulSet template
kubectl patch statefulset ignition -n lab --type=json \
  -p='[{"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1gateway-name"}]'

# Wait for rolling restart
kubectl rollout status statefulset/ignition -n lab --timeout=300s
sleep 15

# Check name resolution
kubectl get ignitionsync lab-sync -n lab \
  -o jsonpath='{.status.discoveredGateways[0].name}'
```

Expected: `ignition` (from `app.kubernetes.io/name` label, set by helm chart).

**Restore annotation:**
```bash
kubectl patch statefulset ignition -n lab --type=json \
  -p='[{"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1gateway-name", "value": "lab-gateway"}]'
kubectl rollout status statefulset/ignition -n lab --timeout=300s
sleep 15
```

---

## Lab 3.3: Add a Second Gateway (Multi-Gateway Discovery)

### Purpose
Deploy a second Ignition gateway in the same namespace and verify the controller discovers both independently.

### Steps

```bash
# Install a second Ignition gateway via helm
helm upgrade --install ignition-secondary inductiveautomation/ignition \
  -n lab \
  --set image.tag=8.3.6 \
  --set commissioning.edition=standard \
  --set commissioning.acceptIgnitionEULA=true \
  --set gateway.replicas=1 \
  --set gateway.resourcesEnabled=true \
  --set gateway.resources.requests.cpu=500m \
  --set gateway.resources.requests.memory=1Gi \
  --set gateway.resources.limits.cpu=1 \
  --set gateway.resources.limits.memory=2Gi \
  --set gateway.dataVolumeStorageSize=3Gi \
  --set gateway.persistentVolumeClaimRetentionPolicy=Delete \
  --set service.type=ClusterIP \
  --set ingress.enabled=false \
  --set certManager.enabled=false

# Wait for second gateway
kubectl rollout status statefulset/ignition-secondary -n lab --timeout=300s

# Add annotations pointing to the same CR
kubectl patch statefulset ignition-secondary -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "lab-sync"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1gateway-name", "value": "secondary-gateway"}
]'
kubectl rollout status statefulset/ignition-secondary -n lab --timeout=300s
```

### What to Verify

```bash
# Wait for discovery (up to 30s)
sleep 20

# Check discovered gateways
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.discoveredGateways[] | {name, podName, syncStatus}]'
```

Expected: Two entries — `lab-gateway` (pod `ignition-0`) and `secondary-gateway` (pod `ignition-secondary-0`).

```bash
# Verify kubectl output columns
kubectl get ignitionsync lab-sync -n lab
```

The `GATEWAYS` column should show `0/2 gateways synced` (neither has an agent yet).

---

## Lab 3.4: Status Collection from ConfigMap

### Purpose
Simulate agent status reporting by manually creating the status ConfigMap. Verify the controller enriches gateway status from it.

### Steps

```bash
# Get the current commit from the CR
COMMIT=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')

# Create status ConfigMap as if agents reported
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: ignition-sync-status-lab-sync
  namespace: lab
  labels:
    ignition-sync.io/cr-name: lab-sync
data:
  lab-gateway: |
    {
      "syncStatus": "Synced",
      "syncedCommit": "${COMMIT}",
      "syncedRef": "main",
      "lastSyncTime": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "lastSyncDuration": "1.5s",
      "agentVersion": "0.1.0",
      "filesChanged": 12,
      "projectsSynced": ["MyProject", "SharedScripts"]
    }
  secondary-gateway: |
    {
      "syncStatus": "Synced",
      "syncedCommit": "${COMMIT}",
      "syncedRef": "main",
      "lastSyncTime": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "lastSyncDuration": "2.1s",
      "agentVersion": "0.1.0",
      "filesChanged": 12,
      "projectsSynced": ["MyProject", "SharedScripts"]
    }
EOF
```

### What to Verify (After ~15s)

```bash
# Full gateway status
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.discoveredGateways[] | {
  name,
  syncStatus,
  syncedCommit,
  agentVersion,
  filesChanged,
  projectsSynced
}'
```

Expected: Both gateways show `syncStatus: "Synced"` with all fields populated.

---

## Lab 3.5: AllGatewaysSynced Condition Transitions

### Purpose
Verify the AllGatewaysSynced condition accurately reflects aggregate gateway state.

### Steps

**Both synced → AllGatewaysSynced=True:**

```bash
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | select(.type=="AllGatewaysSynced")'
```

Expected: `status: "True"`, `message: "2/2 gateways synced"`

**Set one to Error → AllGatewaysSynced=False:**

```bash
kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | \
  jq '.data["secondary-gateway"] = "{\"syncStatus\":\"Error\",\"errorMessage\":\"simulated failure\"}"' | \
  kubectl apply -n lab -f -

sleep 15

kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | select(.type=="AllGatewaysSynced")'
```

Expected: `status: "False"`, `message: "1/2 gateways synced"`

**Set to Syncing → still False:**

```bash
kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | \
  jq '.data["secondary-gateway"] = "{\"syncStatus\":\"Syncing\"}"' | \
  kubectl apply -n lab -f -

sleep 15

kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | select(.type=="AllGatewaysSynced")'
```

Expected: Still `False` — only `Synced` counts.

**Restore both to Synced:**

```bash
COMMIT=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | \
  jq --arg c "$COMMIT" '.data["secondary-gateway"] = "{\"syncStatus\":\"Synced\",\"syncedCommit\":\"" + $c + "\"}"' | \
  kubectl apply -n lab -f -
```

---

## Lab 3.6: Ready Condition — Full Stack

### Purpose
Ready=True requires BOTH RepoCloned=True AND AllGatewaysSynced=True.

### Steps

```bash
# With both conditions met
sleep 15
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.conditions[] | {type, status, reason, message}]'
```

### Expected

| Condition | Status | Reason |
|-----------|--------|--------|
| RepoCloned | True | CloneSucceeded |
| AllGatewaysSynced | True | SyncSucceeded |
| Ready | True | SyncSucceeded |

```bash
# Verify kubectl columns
kubectl get ignitionsync lab-sync -n lab
```

Expected: `SYNCED=True`, `READY=True`

---

## Lab 3.7: No Gateways Discovered

### Purpose
Verify behavior when no annotated pods exist for a CR.

### Steps

```bash
# Create a CR that no pods reference
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: orphan-sync
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

sleep 30
```

### What to Verify

```bash
kubectl get ignitionsync orphan-sync -n lab -o json | jq '{
  discoveredGateways: (.status.discoveredGateways | length),
  allGatewaysSynced: (.status.conditions[] | select(.type=="AllGatewaysSynced")),
  ready: (.status.conditions[] | select(.type=="Ready"))
}'
```

Expected:
- `discoveredGateways: 0`
- AllGatewaysSynced: `status: "False"`, `reason: "NoGatewaysDiscovered"`
- Ready: `status: "False"`

### Cleanup
```bash
kubectl delete ignitionsync orphan-sync -n lab
```

---

## Lab 3.8: Pod Deletion — Gateway Removed from Status

### Purpose
When a gateway pod is deleted (simulating a pod restart or scale-down), verify it's removed from discoveredGateways within a reasonable time.

### Steps

```bash
# Record current gateway count
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.discoveredGateways | length'
# Expected: 2

# Scale down the secondary gateway
kubectl scale statefulset ignition-secondary -n lab --replicas=0

# Wait and check
sleep 30
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.discoveredGateways[] | .name]'
```

Expected: Only `["lab-gateway"]` remains.

```bash
# Scale back up
kubectl scale statefulset ignition-secondary -n lab --replicas=1
kubectl rollout status statefulset/ignition-secondary -n lab --timeout=300s
sleep 20

kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.discoveredGateways[] | .name]'
```

Expected: Both `lab-gateway` and `secondary-gateway` present again.

---

## Lab 3.9: GatewaysDiscovered Event

### Purpose
Verify Kubernetes events are emitted when gateway count changes.

### Steps

```bash
kubectl get events -n lab --field-selector reason=GatewaysDiscovered --sort-by=.lastTimestamp
```

### Expected
At least one event like:
```
<timestamp>  Normal  GatewaysDiscovered  ignitionsync/lab-sync  Discovered 2 gateway(s) (was 1)
```

---

## Lab 3.10: Multi-CR Isolation with Real Gateways

### Purpose
Verify that a second CR only discovers pods annotated for it, not pods for the first CR.

### Steps

```bash
# Create second CR
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: isolated-sync
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "v1.0.0"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

sleep 30
```

### What to Verify

```bash
# isolated-sync should see 0 gateways (no pods annotated for it)
kubectl get ignitionsync isolated-sync -n lab -o json | jq '.status.discoveredGateways | length'
# Expected: 0

# lab-sync should still see 2 gateways
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.discoveredGateways | length'
# Expected: 2
```

Now annotate the secondary gateway for the new CR instead:

```bash
kubectl patch statefulset ignition-secondary -n lab --type=json -p='[
  {"op": "replace", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "isolated-sync"}
]'
kubectl rollout status statefulset/ignition-secondary -n lab --timeout=300s
sleep 20
```

```bash
# lab-sync should now see 1 gateway
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.discoveredGateways[] | .name]'
# Expected: ["lab-gateway"]

# isolated-sync should now see 1 gateway
kubectl get ignitionsync isolated-sync -n lab -o json | jq '[.status.discoveredGateways[] | .name]'
# Expected: ["secondary-gateway"]
```

### Restore

```bash
# Point secondary back to lab-sync
kubectl patch statefulset ignition-secondary -n lab --type=json -p='[
  {"op": "replace", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "lab-sync"}
]'
kubectl rollout status statefulset/ignition-secondary -n lab --timeout=300s
kubectl delete ignitionsync isolated-sync -n lab
```

---

## Lab 3.11: Ignition Gateway Health Check

### Purpose
Confirm both Ignition gateways remained healthy throughout all discovery operations.

### Steps

```bash
# Both gateways healthy
for pod in ignition-0 ignition-secondary-0; do
  echo "=== $pod ==="
  kubectl get pod $pod -n lab -o json | jq '{
    phase: .status.phase,
    ready: (.status.conditions[] | select(.type=="Ready") | .status),
    restarts: .status.containerStatuses[0].restartCount
  }'
done

# Operator healthy
kubectl get pods -n ignition-sync-operator-system -o json | jq '.items[0] | {
  phase: .status.phase,
  restarts: .status.containerStatuses[0].restartCount
}'

# Check operator logs for any errors
kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=50 | grep -i error | head -10
```

---

## Phase 3 Completion Checklist

| Check | Status |
|-------|--------|
| Real Ignition pod discovered via cr-name annotation | |
| Gateway name resolved from annotation (priority over label) | |
| Gateway name falls back to label when annotation removed | |
| Second Ignition gateway discovered (2-gateway scenario) | |
| Status ConfigMap enriches gateway with sync details | |
| AllGatewaysSynced transitions: True → False (on error) → True (on recovery) | |
| Ready=True when both RepoCloned and AllGatewaysSynced are True | |
| No gateways → AllGatewaysSynced=False, reason=NoGatewaysDiscovered | |
| Pod deletion removes gateway from discoveredGateways | |
| Pod recreation re-adds gateway | |
| GatewaysDiscovered event emitted | |
| Multi-CR isolation — each CR sees only its own annotated pods | |
| Both Ignition gateways healthy with 0 restarts | |
| Operator pod healthy with 0 restarts | |
