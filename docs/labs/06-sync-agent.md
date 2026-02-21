# Lab 06 — Sync Agent

## Objective

Validate the sync agent binary end-to-end with a real Ignition gateway. The agent clones the git repository to a local emptyDir volume (`/repo`), syncs project files to the Ignition data directory, triggers the scan API, and reports status via ConfigMap. This is the first phase where we see **projects actually appear in the Ignition web UI**.

**Prerequisite:** Complete [05 — Webhook Receiver](05-webhook-receiver.md). The `lab-sync` CR should be `Ready` with `RefResolved=True` and 2 gateways discovered.

---

## Lab 6.1: Agent Binary Smoke Test

### Purpose
Verify the agent container image starts, clones the git repo to its local emptyDir, and enters its watch loop without crashing.

### Steps

Deploy the agent as a standalone pod (not yet injected as a sidecar) with the same mounts and env vars it would receive from the mutating webhook:

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: agent-test
  labels:
    app: agent-test
spec:
  serviceAccountName: default
  containers:
    - name: agent
      image: ignition-sync-operator:lab
      command: ["/agent"]
      env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: GATEWAY_NAME
          value: "lab-gateway"
        - name: CR_NAME
          value: "lab-sync"
        - name: CR_NAMESPACE
          value: "lab"
        - name: REPO_PATH
          value: "/repo"
        - name: DATA_PATH
          value: "/ignition-data"
        - name: GATEWAY_PORT
          value: "8088"
        - name: GATEWAY_TLS
          value: "false"
        - name: API_KEY_FILE
          value: "/secrets/apiKey"
        - name: SYNC_PERIOD
          value: "30"
        - name: GIT_TOKEN_FILE
          value: "/git-auth/token"
      volumeMounts:
        - name: repo
          mountPath: /repo
        - name: data
          mountPath: /ignition-data
        - name: api-key
          mountPath: /secrets
          readOnly: true
        - name: git-auth
          mountPath: /git-auth
          readOnly: true
      resources:
        requests:
          cpu: 50m
          memory: 64Mi
        limits:
          cpu: 200m
          memory: 128Mi
  volumes:
    - name: repo
      emptyDir: {}
    - name: data
      emptyDir: {}
    - name: api-key
      secret:
        secretName: ignition-api-key
    - name: git-auth
      secret:
        secretName: git-token-secret
EOF
```

### What to Verify

1. **Pod starts without crashing:**
   ```bash
   kubectl wait --for=condition=Ready pod/agent-test -n lab --timeout=120s
   kubectl get pod agent-test -n lab
   ```

2. **Agent logs show initialization and clone:**
   ```bash
   kubectl logs agent-test -n lab --tail=30
   ```
   Expected: Startup messages showing it loaded env vars, read the metadata ConfigMap, cloned the repo to `/repo`, and entered watch/poll mode.

3. **Agent cloned the repo to its local emptyDir:**
   ```bash
   kubectl exec agent-test -n lab -- ls /repo/com.inductiveautomation.ignition/projects/
   ```
   Expected: `MyProject` and `SharedScripts` directories.

### Cleanup (after testing)
```bash
kubectl delete pod agent-test -n lab
```

---

## Lab 6.2: File Sync to Ignition Data Directory

### Purpose
Verify the agent correctly syncs project files from its local `/repo` clone to `/ignition-data/projects/`, respecting the `.resources/` protection.

### Steps

Deploy agent with a shared volume that simulates the Ignition data directory:

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: agent-sync-test
  labels:
    app: agent-test
spec:
  containers:
    - name: agent
      image: ignition-sync-operator:lab
      command: ["/agent"]
      env:
        - name: POD_NAME
          value: "agent-sync-test"
        - name: POD_NAMESPACE
          value: "lab"
        - name: GATEWAY_NAME
          value: "test-sync-gateway"
        - name: CR_NAME
          value: "lab-sync"
        - name: CR_NAMESPACE
          value: "lab"
        - name: REPO_PATH
          value: "/repo"
        - name: DATA_PATH
          value: "/ignition-data"
        - name: GATEWAY_PORT
          value: "8088"
        - name: GATEWAY_TLS
          value: "false"
        - name: API_KEY_FILE
          value: "/secrets/apiKey"
        - name: SYNC_PERIOD
          value: "10"
        - name: GIT_TOKEN_FILE
          value: "/git-auth/token"
      volumeMounts:
        - name: repo
          mountPath: /repo
        - name: data
          mountPath: /ignition-data
        - name: api-key
          mountPath: /secrets
          readOnly: true
        - name: git-auth
          mountPath: /git-auth
          readOnly: true
    - name: inspector
      image: alpine:latest
      command: ["sleep", "3600"]
      volumeMounts:
        - name: data
          mountPath: /ignition-data
  volumes:
    - name: repo
      emptyDir: {}
    - name: data
      emptyDir: {}
    - name: api-key
      secret:
        secretName: ignition-api-key
    - name: git-auth
      secret:
        secretName: git-token-secret
EOF

kubectl wait --for=condition=Ready pod/agent-sync-test -n lab --timeout=120s
sleep 15
```

### What to Verify

1. **Projects synced to /ignition-data:**
   ```bash
   kubectl exec agent-sync-test -c inspector -n lab -- ls -la /ignition-data/projects/ 2>/dev/null || \
   kubectl exec agent-sync-test -c inspector -n lab -- find /ignition-data -type f | head -30
   ```
   Expected: Project directories and files from the repo appear under `/ignition-data/`.

2. **project.json files have valid content:**
   ```bash
   kubectl exec agent-sync-test -c inspector -n lab -- \
     cat /ignition-data/projects/MyProject/project.json 2>/dev/null || echo "path may differ"
   ```

3. **.resources/ NOT synced** — this is critical:
   ```bash
   kubectl exec agent-sync-test -c inspector -n lab -- \
     ls /ignition-data/.resources/ 2>&1
   ```
   Expected: `No such file or directory` — the agent must never copy `.resources/`.

4. **Exclude patterns respected** (no `.git/` directory):
   ```bash
   kubectl exec agent-sync-test -c inspector -n lab -- \
     find /ignition-data -name ".git" -type d 2>/dev/null
   ```
   Expected: Empty output.

### Cleanup
```bash
kubectl delete pod agent-sync-test -n lab
```

---

## Lab 6.3: Agent Status Reporting via ConfigMap

### Purpose
Verify the agent writes its sync status to the `ignition-sync-status-{crName}` ConfigMap with the correct JSON structure.

### Steps

After lab 5.2 (or with a fresh agent pod), check the status ConfigMap:

```bash
kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | jq '.data'
```

### What to Verify

The ConfigMap should have a key matching the gateway name with JSON containing:

```bash
kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | \
  jq -r '.data["test-sync-gateway"] // .data["lab-gateway"]' | jq .
```

Expected fields:
- `syncStatus: "Synced"` (or `"Error"` with `errorMessage`)
- `syncedCommit:` matching the current CR commit
- `syncedRef:` matching `"main"`
- `lastSyncTime:` valid RFC3339 timestamp
- `lastSyncDuration:` e.g., `"1.2s"`
- `agentVersion:` non-empty
- `filesChanged:` integer
- `projectsSynced:` array of project names like `["MyProject", "SharedScripts"]`

---

## Lab 6.4: Re-Sync on Metadata ConfigMap Change

### Purpose
Verify the agent detects changes to the metadata ConfigMap (new commit SHA or trigger timestamp) and triggers a re-sync (git fetch + checkout).

### Steps

```bash
# Deploy a fresh agent pod
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: agent-resync-test
  labels:
    app: agent-test
spec:
  serviceAccountName: default
  containers:
    - name: agent
      image: ignition-sync-operator:lab
      command: ["/agent"]
      env:
        - name: POD_NAME
          value: "agent-resync-test"
        - name: POD_NAMESPACE
          value: "lab"
        - name: GATEWAY_NAME
          value: "resync-gw"
        - name: CR_NAME
          value: "lab-sync"
        - name: CR_NAMESPACE
          value: "lab"
        - name: REPO_PATH
          value: "/repo"
        - name: DATA_PATH
          value: "/ignition-data"
        - name: GATEWAY_PORT
          value: "8088"
        - name: GATEWAY_TLS
          value: "false"
        - name: API_KEY_FILE
          value: "/secrets/apiKey"
        - name: SYNC_PERIOD
          value: "30"
        - name: GIT_TOKEN_FILE
          value: "/git-auth/token"
      volumeMounts:
        - name: repo
          mountPath: /repo
        - name: data
          mountPath: /ignition-data
        - name: api-key
          mountPath: /secrets
          readOnly: true
        - name: git-auth
          mountPath: /git-auth
          readOnly: true
  volumes:
    - name: repo
      emptyDir: {}
    - name: data
      emptyDir: {}
    - name: api-key
      secret:
        secretName: ignition-api-key
    - name: git-auth
      secret:
        secretName: git-token-secret
EOF

kubectl wait --for=condition=Ready pod/agent-resync-test -n lab --timeout=120s
sleep 15

# Record current agent logs
kubectl logs agent-resync-test -n lab --tail=5

# Update the metadata ConfigMap trigger timestamp
kubectl patch configmap ignition-sync-metadata-lab-sync -n lab --type=merge \
  -p "{\"data\":{\"trigger\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}}"

# Wait and check for re-sync in logs
sleep 15
kubectl logs agent-resync-test -n lab --tail=20 | grep -i "sync\|trigger\|changed\|fetch"
```

Expected: Log entries showing the agent detected the ConfigMap change and initiated a fetch + sync cycle.

### Cleanup
```bash
kubectl delete pod agent-resync-test -n lab
```

---

## Lab 6.5: Scan API Graceful Failure

### Purpose
When no real Ignition gateway API is reachable (wrong port, bad API key), the agent should report Error status but not crash. File sync should still succeed.

### Steps

Deploy an agent with a wrong gateway port:

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: agent-bad-port
  labels:
    app: agent-test
spec:
  containers:
    - name: agent
      image: ignition-sync-operator:lab
      command: ["/agent"]
      env:
        - name: POD_NAME
          value: "agent-bad-port"
        - name: POD_NAMESPACE
          value: "lab"
        - name: GATEWAY_NAME
          value: "bad-port-gw"
        - name: CR_NAME
          value: "lab-sync"
        - name: CR_NAMESPACE
          value: "lab"
        - name: REPO_PATH
          value: "/repo"
        - name: DATA_PATH
          value: "/data"
        - name: GATEWAY_PORT
          value: "9999"
        - name: GATEWAY_TLS
          value: "false"
        - name: API_KEY_FILE
          value: "/secrets/apiKey"
        - name: SYNC_PERIOD
          value: "10"
        - name: GIT_TOKEN_FILE
          value: "/git-auth/token"
      volumeMounts:
        - name: repo
          mountPath: /repo
        - name: data
          mountPath: /data
        - name: api-key
          mountPath: /secrets
          readOnly: true
        - name: git-auth
          mountPath: /git-auth
          readOnly: true
  volumes:
    - name: repo
      emptyDir: {}
    - name: data
      emptyDir: {}
    - name: api-key
      secret:
        secretName: ignition-api-key
    - name: git-auth
      secret:
        secretName: git-token-secret
EOF

sleep 30
```

### What to Verify

1. **Agent still running** (not crashed):
   ```bash
   kubectl get pod agent-bad-port -n lab -o jsonpath='{.status.phase}'
   ```
   Expected: `Running`

2. **Status reports Error:**
   ```bash
   kubectl get configmap ignition-sync-status-lab-sync -n lab -o json | \
     jq -r '.data["bad-port-gw"]' | jq .
   ```
   Expected: `syncStatus: "Error"` with an `errorMessage` about connection failure.

3. **Files still synced** (scan API failure shouldn't prevent file sync):
   ```bash
   kubectl exec agent-bad-port -n lab -- ls /data/projects/ 2>/dev/null || echo "check path"
   ```

### Cleanup
```bash
kubectl delete pod agent-bad-port -n lab
```

---

## Lab 6.6: Config Normalization

### Purpose
Verify `systemName` normalization rewrites `config.json` files with gateway-specific values.

### Steps

If the CR has normalization enabled:
```bash
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"normalize":{"systemName":true}}}'
sleep 30
```

Deploy an agent and check if config.json files in the synced output contain the gateway name:
```bash
# Use a fresh agent pod or the sidecar from Lab 6.7
kubectl exec ignition-0 -n lab -c sync-agent -- \
  find /usr/local/bin/ignition/data/projects -name "config.json" -exec cat {} \; 2>/dev/null || echo "Run after Lab 6.7"
```

Look for `systemName` field values matching the gateway name.

---

## Lab 6.7: Agent with Real Ignition Gateway — End-to-End

### Purpose
This is the critical test: deploy the agent as a sidecar alongside a real Ignition gateway and verify that projects appear in the Ignition web UI after sync + scan.

### Steps

**Step 1: Create a real Ignition API key.**

First, access the Ignition web UI:
```bash
kubectl port-forward svc/ignition -n lab 8088:8088 &
```

Navigate to `http://localhost:8088` and:
1. Go to **Config > System > Gateway Settings** (or complete commissioning if first time)
2. Go to **Config > Security > API Keys**
3. Create a new API key with appropriate permissions
4. Copy the key value

Update the secret:
```bash
kubectl delete secret ignition-api-key -n lab
kubectl create secret generic ignition-api-key -n lab \
  --from-literal=apiKey=YOUR_ACTUAL_API_KEY_HERE
kubectl label secret ignition-api-key -n lab app=lab-test
```

**Step 2: Deploy agent as sidecar with real Ignition data volume.**

The Ignition gateway stores data at `/usr/local/bin/ignition/data`. The agent clones the repo to a local emptyDir and syncs files to this path. This requires modifying the Ignition StatefulSet to add the agent container with the git auth secret:

```bash
# Patch the Ignition StatefulSet to add the agent sidecar
# This manually does what the mutating webhook (phase 6) will automate
kubectl patch statefulset ignition -n lab --type=json -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/containers/-",
    "value": {
      "name": "sync-agent",
      "image": "ignition-sync-operator:lab",
      "command": ["/agent"],
      "env": [
        {"name": "POD_NAME", "valueFrom": {"fieldRef": {"fieldPath": "metadata.name"}}},
        {"name": "POD_NAMESPACE", "valueFrom": {"fieldRef": {"fieldPath": "metadata.namespace"}}},
        {"name": "GATEWAY_NAME", "value": "lab-gateway"},
        {"name": "CR_NAME", "value": "lab-sync"},
        {"name": "CR_NAMESPACE", "value": "lab"},
        {"name": "REPO_PATH", "value": "/repo"},
        {"name": "DATA_PATH", "value": "/usr/local/bin/ignition/data"},
        {"name": "GATEWAY_PORT", "value": "8088"},
        {"name": "GATEWAY_TLS", "value": "false"},
        {"name": "API_KEY_FILE", "value": "/secrets/apiKey"},
        {"name": "SYNC_PERIOD", "value": "30"},
        {"name": "GIT_TOKEN_FILE", "value": "/git-auth/token"}
      ],
      "volumeMounts": [
        {"name": "repo", "mountPath": "/repo"},
        {"name": "data", "mountPath": "/usr/local/bin/ignition/data"},
        {"name": "api-key", "mountPath": "/secrets", "readOnly": true},
        {"name": "git-auth", "mountPath": "/git-auth", "readOnly": true}
      ],
      "resources": {
        "requests": {"cpu": "50m", "memory": "64Mi"},
        "limits": {"cpu": "200m", "memory": "128Mi"}
      }
    }
  },
  {
    "op": "add",
    "path": "/spec/template/spec/volumes/-",
    "value": {
      "name": "repo",
      "emptyDir": {}
    }
  },
  {
    "op": "add",
    "path": "/spec/template/spec/volumes/-",
    "value": {
      "name": "api-key",
      "secret": {
        "secretName": "ignition-api-key"
      }
    }
  },
  {
    "op": "add",
    "path": "/spec/template/spec/volumes/-",
    "value": {
      "name": "git-auth",
      "secret": {
        "secretName": "git-token-secret"
      }
    }
  }
]'

kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

**Step 3: Verify the agent cloned and synced files.**

```bash
# Check agent container logs
kubectl logs ignition-0 -n lab -c sync-agent --tail=30

# Verify projects exist on the Ignition data volume
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/
```

Expected: `MyProject` and `SharedScripts` directories should appear.

**Step 4: Verify projects appear in Ignition.**

```bash
# Check the Ignition web UI
curl -s http://localhost:8088/data/api/v1/projects 2>/dev/null | jq . || \
  echo "API may require authentication — check web UI manually"
```

**Observation:** Open `http://localhost:8088` and navigate to **Config > System > Projects**. You should see `MyProject` and `SharedScripts` listed.

If projects don't appear automatically, the scan API needs to be triggered:
```bash
# Trigger project scan manually (agent should do this automatically)
curl -X POST http://localhost:8088/data/project-scan-endpoint/scan 2>/dev/null || \
curl -X POST http://localhost:8088/data/api/v1/scan/project 2>/dev/null || \
  echo "Scan API may need API key authentication"
```

---

## Lab 6.8: Ref Change End-to-End — Projects Update in Ignition

### Purpose
The crown jewel test: change the git ref and verify the agent fetches the new commit, syncs updated files, and the changes appear in the Ignition web UI.

### Steps

```bash
# Switch to 0.1.0 (only MainView exists)
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"0.1.0"}}}'

echo "Waiting for controller ref resolution + agent fetch..."
sleep 45

# Verify only MainView exists in Ignition
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/MyProject/com.inductiveautomation.perspective/views/ 2>/dev/null
# Expected: MainView only

# Now switch to 0.2.0 (adds SecondView)
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"0.2.0"}}}'

echo "Waiting for controller ref resolution + agent fetch..."
sleep 45

# Verify SecondView now exists
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/MyProject/com.inductiveautomation.perspective/views/ 2>/dev/null
# Expected: MainView AND SecondView
```

**Observation:** Open the Ignition web UI and navigate to the Perspective views. You should see SecondView appear after the 0.2.0 sync.

### Restore
```bash
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'
```

---

## Phase 5 Completion Checklist

| Check | Status |
|-------|--------|
| Agent binary starts and clones repo to local emptyDir | |
| Agent reads project files from local /repo clone | |
| Agent syncs files to /ignition-data (or equivalent) | |
| .resources/ never synced (critical protection) | |
| Exclude patterns respected (.git/, .gitkeep) | |
| Agent writes status JSON to ConfigMap | |
| Status includes syncedCommit, filesChanged, projectsSynced | |
| Agent detects metadata ConfigMap change and re-syncs | |
| Agent as sidecar with real Ignition gateway — files appear on data volume | |
| Projects visible in Ignition web UI after scan | |
| Config normalization rewrites systemName | |
| Scan API failure → Error status, agent doesn't crash | |
| Ref change → agent fetches new commit → updated files in Ignition | |
| Ignition gateway healthy with agent sidecar | |
| Operator pod 0 restarts | |
