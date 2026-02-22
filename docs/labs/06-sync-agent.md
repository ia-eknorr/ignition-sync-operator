# Lab 06 — Sync Agent

## Objective

Validate the sync agent binary end-to-end with real Ignition gateways. The agent clones the git repository to a local emptyDir volume (`/repo`), syncs project files to the Ignition data directory using SyncProfile-driven ordered mappings, and reports status via ConfigMap. This lab covers **SyncProfile mode** (ordered mappings with deployment mode overlay, dry-run, paused) and **legacy mode** (SERVICE_PATH flat sync).

**Prerequisite:** Complete [05 — Webhook Receiver](05-webhook-receiver.md). The operator controller should be deployed to `ignition-sync-operator-system`.

---

## Build & Deploy

Before running any lab, build the image and load into kind:

```bash
docker build -t ignition-sync-operator:dev .
kind load docker-image ignition-sync-operator:dev --name dev

# Update the controller
kubectl set image deployment/ignition-sync-operator-controller-manager \
  manager=ignition-sync-operator:dev -n ignition-sync-operator-system
kubectl rollout status deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --timeout=60s
```

---

## Lab 6.1: Environment Setup

### Purpose
Deploy two Ignition gateways (blue and red) in the `ignition-test` namespace with the sync operator managing both via a single IgnitionSync CR and per-gateway SyncProfiles.

### Steps

Follow the complete setup in [Test Environment — kind Cluster Setup](../test-environment.md#6-kind-cluster-setup). This creates:

- Namespace `ignition-test` with secrets and API token config
- Two Ignition gateways via helm (`ignition-blue`, `ignition-red`)
- IgnitionSync CR `test-sync` and SyncProfile CRs (`blue-profile`, `red-profile`)
- Native sidecar on each gateway with startup probe gating

### What to Verify

1. **Both pods are running with 2/2 containers:**
   ```bash
   kubectl -n ignition-test get pods
   ```
   Expected: `ignition-blue-gateway-0` and `ignition-red-gateway-0` both `2/2 Running`.

2. **IgnitionSync CR shows both gateways synced:**
   ```bash
   kubectl -n ignition-test get ignitionsync test-sync
   ```
   Expected: `GATEWAYS: 2/2 gateways synced`, `READY: True`.

3. **Both SyncProfiles accepted:**
   ```bash
   kubectl -n ignition-test get syncprofile
   ```
   Expected: Both `blue-profile` and `red-profile` with `ACCEPTED=True`, `MODE=dev`.

---

## Lab 6.2: SyncProfile — Verify Ordered Mappings

### Purpose
Verify that the agent syncs files using SyncProfile-driven ordered mappings, with correct per-gateway config and deployment mode overlay.

### Steps

```bash
# Port-forward both gateways
kubectl -n ignition-test port-forward pod/ignition-blue-gateway-0 8088:8088 &
kubectl -n ignition-test port-forward pod/ignition-red-gateway-0 8089:8088 &
sleep 5

export API_TOKEN="ignition-api-key:<your-api-token>"
```

### What to Verify

1. **Native sidecar synced files before gateway started:**
   ```bash
   kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent | head -15
   ```
   Expected:
   - `starting agent`
   - `fetching SyncProfile  name=blue-profile`
   - `executing sync plan  mappings=4, dryRun=false`
   - `initial sync complete, startup probe now passing`
   - `gateway health check failed (expected on initial sync)` — gateway hadn't started yet

2. **Gateway started after startup probe passed:**
   ```bash
   kubectl -n ignition-test get pod ignition-blue-gateway-0 -o json | \
     jq '{initContainers: [.status.initContainerStatuses[] | {name, started, ready}],
          containers: [.status.containerStatuses[] | {name, ready}]}'
   ```
   Expected: `sync-agent` has `started: true, ready: true`; `gateway` has `ready: true`.

3. **Ignition API accessible with token (config loaded before boot):**
   ```bash
   curl -s -o /dev/null -w "%{http_code}" \
     -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/projects/list
   ```
   Expected: `200`

4. **Projects synced — each gateway has its own project:**
   ```bash
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/projects/list | jq '[.items[].name]'
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8089/data/api/v1/projects/list | jq '[.items[].name]'
   ```
   Expected: Blue has `["blue"]`, Red has `["red"]`.

5. **Cobranding per-gateway (core mapping):**
   ```bash
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/resources/singleton/ignition/cobranding | jq .config.backgroundColor
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8089/data/api/v1/resources/singleton/ignition/cobranding | jq .config.backgroundColor
   ```
   Expected: Blue = `"#00a3d7"`, Red = `"#ff4013"`.

6. **Dev overlay merged into core (deployment mode mapping):**
   ```bash
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8088/data/api/v1/resources/singleton/ignition/system-properties | \
     jq '{systemName: .config.systemName, homepageNotes: .config.homepageNotes}'
   curl -s -H "X-Ignition-API-Token: $API_TOKEN" \
     http://localhost:8089/data/api/v1/resources/singleton/ignition/system-properties | \
     jq '{systemName: .config.systemName, homepageNotes: .config.homepageNotes}'
   ```
   Expected: Blue = `ignition-blue` / `Ignition Blue - Dev`, Red = `ignition-red` / `Ignition Red - Dev`.

7. **Status ConfigMap includes both gateways with profile names:**
   ```bash
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | jq '.data | keys'
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
     jq -r '.data["ignition-blue"]' | jq '{syncStatus, syncProfileName, projectsSynced}'
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
     jq -r '.data["ignition-red"]' | jq '{syncStatus, syncProfileName, projectsSynced}'
   ```
   Expected: Both have `syncStatus: "Synced"`, correct `syncProfileName`, and different `projectsSynced`.

### Cleanup

```bash
# Kill port-forwards
lsof -ti:8088 -ti:8089 | xargs kill 2>/dev/null
```

---

## Lab 6.3: SyncProfile Dry-Run Mode

### Purpose
Verify that dry-run mode computes a diff without writing files to the live directory, and reports the diff in the status ConfigMap.

### Steps

```bash
# Enable dry-run on blue profile
kubectl -n ignition-test patch syncprofile blue-profile --type=merge \
  -p '{"spec":{"dryRun":true}}'

# Restart the blue pod to get a fresh sync
kubectl -n ignition-test delete cm ignition-sync-status-test-sync
kubectl -n ignition-test delete pod ignition-blue-gateway-0
kubectl -n ignition-test wait --for=condition=Ready pod/ignition-blue-gateway-0 --timeout=180s
sleep 15
```

### What to Verify

1. **Agent logs show dry-run execution:**
   ```bash
   kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent --tail=20
   ```
   Expected:
   - `executing sync plan  mappings=4, dryRun=true`
   - `dry-run mode, skipping scan API`

2. **Status ConfigMap includes dry-run diff:**
   ```bash
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
     jq -r '.data["ignition-blue"]' | jq '{dryRun, dryRunDiffAdded, dryRunDiffModified, dryRunDiffDeleted}'
   ```
   Expected: `dryRun: true` and one or more of `dryRunDiffAdded`, `dryRunDiffModified`, `dryRunDiffDeleted`.

3. **Disable dry-run:**
   ```bash
   kubectl -n ignition-test patch syncprofile blue-profile --type=merge \
     -p '{"spec":{"dryRun":false}}'
   ```

---

## Lab 6.4: SyncProfile Paused

### Purpose
Verify that pausing the SyncProfile causes the agent to skip sync entirely, returning zero changes.

### Steps

```bash
# Pause the blue profile
kubectl -n ignition-test patch syncprofile blue-profile --type=merge \
  -p '{"spec":{"paused":true}}'

# Restart the blue pod
kubectl -n ignition-test delete cm ignition-sync-status-test-sync
kubectl -n ignition-test delete pod ignition-blue-gateway-0
kubectl -n ignition-test wait --for=condition=Ready pod/ignition-blue-gateway-0 --timeout=180s
sleep 15
```

### What to Verify

1. **Agent logs show paused state:**
   ```bash
   kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent --tail=20
   ```
   Expected: `SyncProfile is paused, returning zero-change result`

2. **Zero files changed:**
   ```bash
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
     jq -r '.data["ignition-blue"]' | jq .filesChanged
   ```
   Expected: `0`

3. **Unpause:**
   ```bash
   kubectl -n ignition-test patch syncprofile blue-profile --type=merge \
     -p '{"spec":{"paused":false}}'
   ```

---

## Lab 6.5: Agent Status Reporting via ConfigMap

### Purpose
Verify the agent writes its sync status to the `ignition-sync-status-{crName}` ConfigMap with the correct JSON structure for both gateways.

### Steps

Restart both pods for a fresh sync:
```bash
kubectl -n ignition-test delete cm ignition-sync-status-test-sync 2>/dev/null
kubectl -n ignition-test delete pod ignition-blue-gateway-0 ignition-red-gateway-0
kubectl -n ignition-test wait --for=condition=Ready pod/ignition-blue-gateway-0 --timeout=180s
kubectl -n ignition-test wait --for=condition=Ready pod/ignition-red-gateway-0 --timeout=180s
sleep 15
```

### What to Verify

```bash
# Both gateways present in status ConfigMap
kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
  jq -r '.data["ignition-blue"]' | jq .

kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
  jq -r '.data["ignition-red"]' | jq .
```

Expected fields (per gateway):
- `syncStatus: "Synced"`
- `syncedCommit:` matching the current metadata commit
- `syncedRef:` matching `"main"`
- `lastSyncTime:` valid RFC3339 timestamp
- `lastSyncDuration:` e.g., `"500ms"`
- `agentVersion:` `"0.1.0"`
- `filesChanged:` integer
- `projectsSynced:` array (blue: `[".resources", "blue"]`, red: `[".resources", "red"]`)
- `syncProfileName:` `"blue-profile"` or `"red-profile"`

---

## Lab 6.6: Re-Sync on Metadata ConfigMap Change

### Purpose
Verify the agent detects changes to the metadata ConfigMap and triggers a re-sync.

### Steps

```bash
# Record current logs
kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent --tail=5

# Update the metadata ConfigMap trigger timestamp
kubectl -n ignition-test patch configmap ignition-sync-metadata-test-sync --type=merge \
  -p "{\"data\":{\"trigger\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}}"

# Wait and check logs
sleep 15
kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent --tail=20
```

Expected: `metadata ConfigMap changed` → `sync triggered`. Since the commit hasn't changed, the agent logs `commit unchanged, skipping sync` (which is correct — no work needed).

---

## Lab 6.7: Scan API Graceful Failure

### Purpose
When the Ignition gateway API is unreachable, the agent should report the health check failure but not crash. File sync should still succeed.

### Steps

The initial sync always attempts a health check. Since the native sidecar syncs before the gateway starts, the health check fails (connection refused). Check the agent logs after a fresh pod start:

```bash
kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent | head -15
```

Expected: `gateway health check failed (expected on initial sync)` with `error: ...connection refused`, followed by `status written to ConfigMap  status=Synced`.

The pod should remain running and ready:
```bash
kubectl -n ignition-test get pod ignition-blue-gateway-0 \
  -o jsonpath='{.status.initContainerStatuses[?(@.name=="sync-agent")].ready}'
```
Expected: `true`

---

## Lab 6.8: Backward Compatibility — Legacy Mode

### Purpose
Verify that removing the `SYNC_PROFILE` env var causes the agent to fall back to the legacy `SERVICE_PATH` flat sync, confirming backward compatibility.

### Steps

Switch from `SYNC_PROFILE` to `SERVICE_PATH` on the blue sync-agent init container:

```bash
# Find the SYNC_PROFILE env var index on the sync-agent init container (index 1)
kubectl -n ignition-test get statefulset ignition-blue-gateway -o json | \
  jq '.spec.template.spec.initContainers[1].env | to_entries[] | select(.value.name == "SYNC_PROFILE") | .key'

# Replace SYNC_PROFILE with SERVICE_PATH (env index 7)
kubectl -n ignition-test patch statefulset ignition-blue-gateway --type=json -p='[
  {"op": "replace", "path": "/spec/template/spec/initContainers/1/env/7",
   "value": {"name": "SERVICE_PATH", "value": "services/ignition-blue"}}
]'

kubectl -n ignition-test delete cm ignition-sync-status-test-sync
kubectl -n ignition-test rollout status statefulset/ignition-blue-gateway --timeout=180s
sleep 15
```

### What to Verify

1. **Agent logs show legacy mode (no profile):**
   ```bash
   kubectl -n ignition-test logs ignition-blue-gateway-0 -c sync-agent --tail=20
   ```
   Expected: `files synced` with `profile: ""` (empty string — no profile fetch, no plan execution).

2. **Status ConfigMap has no profile fields:**
   ```bash
   kubectl -n ignition-test get cm ignition-sync-status-test-sync -o json | \
     jq -r '.data["ignition-blue"]' | jq .
   ```
   Expected: No `syncProfileName` or `dryRun` fields in JSON.

3. **Restore profile mode:**
   ```bash
   kubectl -n ignition-test patch statefulset ignition-blue-gateway --type=json -p='[
     {"op": "replace", "path": "/spec/template/spec/initContainers/1/env/7",
      "value": {"name": "SYNC_PROFILE", "value": "blue-profile"}}
   ]'
   kubectl -n ignition-test delete cm ignition-sync-status-test-sync
   kubectl -n ignition-test rollout status statefulset/ignition-blue-gateway --timeout=180s
   ```

---

## Phase 6 Completion Checklist

| Check | Status |
|-------|--------|
| Agent binary starts and clones repo to local emptyDir | |
| Native sidecar: syncs files before gateway starts (startup probe gates) | |
| Native sidecar: Ignition API token accessible at gateway startup | |
| Native sidecar: continues watching after initial sync | |
| Two gateways (blue + red): each uses its own SyncProfile | |
| Blue project synced to blue gateway, red project to red gateway | |
| Cobranding per-gateway: blue=#00a3d7, red=#ff4013 | |
| SyncProfile: agent fetches profile CR | |
| SyncProfile: ordered mappings applied (external → core → projects) | |
| SyncProfile: deployment mode overlay merged into core | |
| SyncProfile: status includes syncProfileName | |
| SyncProfile: dry-run mode produces diff without writing | |
| SyncProfile: dry-run status includes dryRunDiff* fields | |
| SyncProfile: paused profile returns zero changes | |
| SyncProfile: scan API skipped in dry-run mode | |
| Exclude patterns respected (.git/, .gitkeep, .resources/) | |
| Agent writes status JSON to ConfigMap (both gateways) | |
| Agent detects metadata ConfigMap change and re-syncs | |
| Scan API failure → non-fatal warning, agent stays running | |
| Backward compat: removing SYNC_PROFILE falls back to legacy mode | |
| IgnitionSync CR shows 2/2 gateways synced, Ready=True | |
