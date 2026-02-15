# Lab 02 — Controller Core

## Objective

Validate the controller's core reconciliation loop with a real Ignition gateway running in the cluster. We verify CRD behavior, PVC provisioning, git clone operations, finalizer lifecycle, ref tracking, metadata ConfigMap creation, and error recovery — all while confirming the operator doesn't interfere with Ignition gateway health.

**Prerequisite:** Complete [00 — Environment Setup](00-environment-setup.md).

---

## Lab 2.1: CRD Smoke Test

### Purpose
Verify the CRD is properly installed with expected schema, short names, and print columns.

### Steps

```bash
# Verify CRD exists and inspect its spec
kubectl get crd ignitionsyncs.sync.ignition.io -o yaml | head -30

# Verify short names
kubectl get isync -n lab
kubectl get igs -n lab

# Verify print columns show in kubectl output
kubectl get ignitionsyncs -n lab
```

### Expected Output
- Short names `isync` and `igs` both work (empty list is fine)
- Column headers include: `NAME`, `REF`, `SYNCED`, `GATEWAYS`, `READY`, `AGE`

### Edge Case: Invalid CR Rejection

```bash
# Attempt to create a CR missing the required `spec.git` field
cat <<'EOF' | kubectl apply -n lab -f - 2>&1
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: invalid-test
spec:
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### Expected
Either the API server rejects it (CRD validation) or the controller catches it and sets an error condition. Record which behavior you see — this informs whether we need tighter CRD validation markers.

### Cleanup
```bash
kubectl delete ignitionsync invalid-test -n lab --ignore-not-found
```

---

## Lab 2.2: Create First IgnitionSync CR

### Purpose
Create a valid CR pointing to the in-cluster git server and watch the full reconciliation cycle.

### Steps

```bash
# Create the CR
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: lab-sync
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### Observations (Watch in Real Time)

Open a second terminal and watch the CR status:
```bash
kubectl get ignitionsync lab-sync -n lab -w
```

In a third terminal, watch operator logs:
```bash
kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager -f --tail=50
```

### What to Verify

1. **Finalizer added** (within ~5s):
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.metadata.finalizers}'
   ```
   Expected: `["ignition-sync.io/finalizer"]`

2. **PVC created** (within ~10s):
   ```bash
   kubectl get pvc -n lab -l ignition-sync.io/cr-name=lab-sync
   ```
   Expected: PVC named `ignition-sync-repo-lab-sync` in `Bound` state.

3. **PVC owner reference** — confirm garbage collection will work:
   ```bash
   kubectl get pvc ignition-sync-repo-lab-sync -n lab \
     -o jsonpath='{.metadata.ownerReferences[0].kind}'
   ```
   Expected: `IgnitionSync`

4. **Git clone completes** (within ~30s):
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: `Cloned`

5. **RepoCloned condition**:
   ```bash
   kubectl get ignitionsync lab-sync -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="RepoCloned")].status}'
   ```
   Expected: `True`

6. **Commit SHA recorded**:
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}'
   ```
   Expected: Non-empty 40-char hex string

7. **Metadata ConfigMap created**:
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o yaml
   ```
   Expected: `data.commit`, `data.ref`, and `data.trigger` keys populated

8. **Ignition gateway still healthy** — the operator should not have affected it:
   ```bash
   kubectl get pod ignition-0 -n lab -o jsonpath='{.status.phase}'
   curl -s -o /dev/null -w '%{http_code}' http://localhost:8088/StatusPing
   ```
   Expected: `Running` and `200`

### Log Inspection

In the operator logs, you should see (in order):
1. `reconciling IgnitionSync` with namespace/name
2. `ensuring PVC`
3. `starting git operation` or `git clone`
4. `git operation completed`
5. `created metadata configmap`
6. `discovered gateways` with count

**Red flags to watch for:** Any `ERROR` lines, stack traces, or `failed to` messages.

---

## Lab 2.3: Inspect PVC Contents

### Purpose
Verify the git clone actually put files on the PVC with the expected Ignition project structure.

### Steps

```bash
# Launch a debug pod that mounts the same PVC
kubectl run pvc-inspector --rm -i --restart=Never -n lab \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "inspector",
        "image": "alpine:latest",
        "command": ["sh", "-c", "apk add --no-cache tree && tree /repo && echo --- && cat /repo/com.inductiveautomation.ignition/projects/MyProject/project.json"],
        "volumeMounts": [{
          "name": "repo",
          "mountPath": "/repo",
          "readOnly": true
        }]
      }],
      "volumes": [{
        "name": "repo",
        "persistentVolumeClaim": {
          "claimName": "ignition-sync-repo-lab-sync"
        }
      }]
    }
  }'
```

### Expected Output

A tree showing:
```
/repo
├── .resources
│   └── platform_state.json
├── com.inductiveautomation.ignition
│   └── projects
│       ├── MyProject
│       │   ├── com.inductiveautomation.perspective
│       │   │   └── views
│       │   │       ├── MainView
│       │   │       │   └── view.json
│       │   │       └── SecondView
│       │   │           └── view.json
│       │   └── project.json
│       └── SharedScripts
│           └── project.json
└── tags
    └── default
        └── tags.json
```

Plus the content of `project.json` showing valid JSON with `"title": "MyProject"`.

### What This Proves
The git clone wrote real files to the PVC in the correct Ignition project structure. When the agent (phase 5) mounts this PVC, it will have valid project data to sync.

---

## Lab 2.4: Ref Tracking — Tag Switch

### Purpose
Verify the controller detects spec.git.ref changes and fetches the new ref. The git server has two commits tagged v1.0.0 (initial, 1 view) and v2.0.0 (second commit, 2 views).

### Steps

```bash
# Record current commit
COMMIT_BEFORE=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
echo "Current commit: $COMMIT_BEFORE"

# Switch to v1.0.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"v1.0.0"}}}'

# Watch for commit change
kubectl get ignitionsync lab-sync -n lab -w
```

### What to Verify

1. **lastSyncRef updates** to `v1.0.0`:
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncRef}'
   ```

2. **lastSyncCommit changes** to a different SHA:
   ```bash
   COMMIT_V1=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
   echo "v1 commit: $COMMIT_V1"
   ```

3. **PVC contents reflect v1** — only 1 view (no SecondView):
   ```bash
   kubectl run pvc-check-v1 --rm -i --restart=Never -n lab \
     --overrides='{
       "spec": {
         "containers": [{
           "name": "check",
           "image": "alpine:latest",
           "command": ["ls", "-la", "/repo/com.inductiveautomation.ignition/projects/MyProject/com.inductiveautomation.perspective/views/"],
           "volumeMounts": [{"name": "repo", "mountPath": "/repo", "readOnly": true}]
         }],
         "volumes": [{"name": "repo", "persistentVolumeClaim": {"claimName": "ignition-sync-repo-lab-sync"}}]
       }
     }'
   ```
   Expected: Only `MainView` directory (no `SecondView`).

4. **Now switch to v2.0.0:**
   ```bash
   kubectl patch ignitionsync lab-sync -n lab --type=merge \
     -p '{"spec":{"git":{"ref":"v2.0.0"}}}'
   ```

5. **Verify SecondView now appears:**
   ```bash
   # Wait for reconcile (~10s)
   sleep 15
   kubectl run pvc-check-v2 --rm -i --restart=Never -n lab \
     --overrides='{
       "spec": {
         "containers": [{
           "name": "check",
           "image": "alpine:latest",
           "command": ["ls", "-la", "/repo/com.inductiveautomation.ignition/projects/MyProject/com.inductiveautomation.perspective/views/"],
           "volumeMounts": [{"name": "repo", "mountPath": "/repo", "readOnly": true}]
         }],
         "volumes": [{"name": "repo", "persistentVolumeClaim": {"claimName": "ignition-sync-repo-lab-sync"}}]
       }
     }'
   ```
   Expected: Both `MainView` and `SecondView` directories.

6. **Metadata ConfigMap updated:**
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.ref}'
   ```
   Expected: `v2.0.0`

### Restore
```bash
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'
```

---

## Lab 2.5: Error Recovery — Bad Repository URL

### Purpose
Verify the controller handles git clone failures gracefully: sets error conditions, doesn't crash, and recovers when the URL is corrected.

### Steps

```bash
# Create a CR with a bad repo URL
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: bad-repo-test
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/does-not-exist.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### What to Verify

1. **RepoCloned=False** (within ~30s):
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="RepoCloned")]}'  | jq .
   ```
   Expected: `status: "False"`, `reason: "CloneFailed"`

2. **repoCloneStatus is Error:**
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: `Error`

3. **Controller still running** (didn't crash):
   ```bash
   kubectl get pods -n ignition-sync-operator-system
   ```
   Expected: Controller pod Running with restart count `0`.

4. **Original CR unaffected:**
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: Still `Cloned`

5. **Fix the URL and verify recovery:**
   ```bash
   kubectl patch ignitionsync bad-repo-test -n lab --type=merge \
     -p '{"spec":{"git":{"repo":"git://test-git-server.lab.svc.cluster.local/test-repo.git"}}}'
   ```
   Wait ~30s, then:
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: `Cloned` — the controller recovered.

### Cleanup
```bash
kubectl delete ignitionsync bad-repo-test -n lab
```

---

## Lab 2.6: Error Recovery — Missing Secret

### Purpose
Verify behavior when the referenced API key secret doesn't exist, and that the controller recovers when it's created.

### Steps

```bash
# Create CR referencing a secret that doesn't exist
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: missing-secret-test
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: nonexistent-secret
      key: apiKey
EOF
```

### What to Verify

1. **Check conditions** (within ~15s):
   ```bash
   kubectl get ignitionsync missing-secret-test -n lab -o json | jq '.status.conditions'
   ```
   Look for Ready=False with a message mentioning the missing secret.

2. **Check operator logs** for the error:
   ```bash
   kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=20 | grep -i secret
   ```

3. **Controller still running:**
   ```bash
   kubectl get pods -n ignition-sync-operator-system -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}'
   ```
   Expected: `0`

4. **Create the secret and verify recovery:**
   ```bash
   kubectl create secret generic nonexistent-secret -n lab --from-literal=apiKey=test-key
   sleep 15
   kubectl get ignitionsync missing-secret-test -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: Eventually reaches `Cloned`.

### Cleanup
```bash
kubectl delete ignitionsync missing-secret-test -n lab
kubectl delete secret nonexistent-secret -n lab
```

---

## Lab 2.7: Paused CR

### Purpose
Verify `spec.paused: true` halts all operations — no PVC, no clone, no reconciliation side effects.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: paused-test
spec:
  paused: true
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### What to Verify (After ~20s)

1. **No PVC created:**
   ```bash
   kubectl get pvc -n lab -l ignition-sync.io/cr-name=paused-test
   ```
   Expected: Empty list.

2. **Ready=False with reason Paused:**
   ```bash
   kubectl get ignitionsync paused-test -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'
   ```
   Expected: `Paused`

3. **Unpause and verify it starts working:**
   ```bash
   kubectl patch ignitionsync paused-test -n lab --type=merge \
     -p '{"spec":{"paused":false}}'
   sleep 30
   kubectl get ignitionsync paused-test -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: `Cloned`

### Cleanup
```bash
kubectl delete ignitionsync paused-test -n lab
```

---

## Lab 2.8: Finalizer and Cleanup on Deletion

### Purpose
Verify the full cleanup chain when a CR is deleted: finalizer runs, metadata ConfigMap is deleted, PVC is garbage collected.

### Steps

```bash
# Create a fresh CR
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: cleanup-test
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# Wait for full reconciliation
sleep 30
kubectl get ignitionsync cleanup-test -n lab -o jsonpath='{.status.repoCloneStatus}'
# Should be: Cloned
```

### Record resources before deletion:
```bash
echo "=== Before Deletion ==="
kubectl get pvc ignition-sync-repo-cleanup-test -n lab 2>&1
kubectl get configmap ignition-sync-metadata-cleanup-test -n lab 2>&1
kubectl get ignitionsync cleanup-test -n lab -o jsonpath='{.metadata.finalizers}'
```

### Delete and observe:
```bash
kubectl delete ignitionsync cleanup-test -n lab &
# Watch in real-time
kubectl get ignitionsync,pvc,configmap -n lab -w
```

### What to Verify

1. **CR deletion completes** (not stuck on finalizer):
   ```bash
   kubectl get ignitionsync cleanup-test -n lab 2>&1
   ```
   Expected: `Error from server (NotFound)`

2. **Metadata ConfigMap deleted** (controller cleanup):
   ```bash
   kubectl get configmap ignition-sync-metadata-cleanup-test -n lab 2>&1
   ```
   Expected: `NotFound`

3. **PVC deleted** (owner reference garbage collection):
   ```bash
   # May take up to 60s for GC controller
   kubectl get pvc ignition-sync-repo-cleanup-test -n lab 2>&1
   ```
   Expected: `NotFound` (eventually)

4. **Operator logs show cleanup:**
   ```bash
   kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=20 | grep -i "cleanup\|finalizer\|deleting"
   ```

---

## Lab 2.9: Multiple CRs — Isolation

### Purpose
Verify two CRs in the same namespace don't interfere with each other. Each should have independent PVCs, ConfigMaps, and status.

### Steps

```bash
# Create two CRs pointing to different refs
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: multi-a
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "v1.0.0"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
---
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: multi-b
spec:
  git:
    repo: "git://test-git-server.lab.svc.cluster.local/test-repo.git"
    ref: "v2.0.0"
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# Wait for both to complete
sleep 45
```

### What to Verify

1. **Both CRs cloned successfully:**
   ```bash
   kubectl get ignitionsyncs -n lab
   ```
   Expected: Both show `REF` (v1.0.0 / v2.0.0) and status fields populated.

2. **Separate PVCs:**
   ```bash
   kubectl get pvc -n lab -l ignition-sync.io/cr-name
   ```
   Expected: `ignition-sync-repo-multi-a` and `ignition-sync-repo-multi-b`

3. **Different commits:**
   ```bash
   COMMIT_A=$(kubectl get ignitionsync multi-a -n lab -o jsonpath='{.status.lastSyncCommit}')
   COMMIT_B=$(kubectl get ignitionsync multi-b -n lab -o jsonpath='{.status.lastSyncCommit}')
   echo "A: $COMMIT_A"
   echo "B: $COMMIT_B"
   [ "$COMMIT_A" != "$COMMIT_B" ] && echo "PASS: Different commits" || echo "FAIL: Same commit"
   ```

4. **Delete one, verify other unaffected:**
   ```bash
   kubectl delete ignitionsync multi-a -n lab
   sleep 10
   kubectl get ignitionsync multi-b -n lab -o jsonpath='{.status.repoCloneStatus}'
   ```
   Expected: Still `Cloned`

### Cleanup
```bash
kubectl delete ignitionsync multi-a multi-b -n lab --ignore-not-found
```

---

## Lab 2.10: Stress — Rapid Ref Flipping

### Purpose
Verify the controller handles rapid spec changes without getting confused or leaking goroutines.

### Steps

```bash
# Ensure lab-sync exists and is cloned
kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.repoCloneStatus}'

# Flip refs rapidly
for ref in v1.0.0 v2.0.0 main v1.0.0 v2.0.0 main; do
  kubectl patch ignitionsync lab-sync -n lab --type=merge \
    -p "{\"spec\":{\"git\":{\"ref\":\"$ref\"}}}"
  sleep 2
done

# Wait for things to settle
sleep 30
```

### What to Verify

1. **Final state is consistent:**
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o json | jq '{
     ref: .spec.git.ref,
     lastSyncRef: .status.lastSyncRef,
     repoCloneStatus: .status.repoCloneStatus,
     lastSyncCommit: .status.lastSyncCommit
   }'
   ```
   Expected: `lastSyncRef` matches `spec.git.ref` (which should be `main`), status is `Cloned`.

2. **Controller pod healthy** (no restarts, no OOM):
   ```bash
   kubectl get pods -n ignition-sync-operator-system -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}'
   ```
   Expected: `0`

3. **No goroutine leaks** — check memory usage hasn't spiked:
   ```bash
   kubectl top pod -n ignition-sync-operator-system 2>/dev/null || echo "metrics-server not installed (skip)"
   ```

---

## Lab 2.11: Ignition Gateway Health During All Operations

### Purpose
Final sanity check that nothing we did in this entire phase affected the Ignition gateway's health.

### Steps

```bash
# Gateway pod health
kubectl get pod ignition-0 -n lab -o json | jq '{
  phase: .status.phase,
  ready: (.status.conditions[] | select(.type=="Ready") | .status),
  restarts: .status.containerStatuses[0].restartCount,
  age: .metadata.creationTimestamp
}'

# Gateway HTTP health
curl -s http://localhost:8088/StatusPing

# Check for any errors in Ignition logs that mention "sync" or "ignition-sync"
kubectl logs ignition-0 -n lab --tail=200 | grep -i "sync\|error\|exception" | head -20
```

### Expected
- Pod Running, Ready, 0 restarts
- StatusPing returns `200`
- No Ignition log entries mentioning "ignition-sync" (the operator hasn't pushed any files to the gateway yet — that's phase 5)

---

## Phase 2 Completion Checklist

| Check | Status |
|-------|--------|
| CRD installed with short names and print columns | |
| Invalid CR handled (rejected or error condition) | |
| Valid CR triggers full reconciliation (PVC, clone, ConfigMap) | |
| PVC has correct owner reference, labels, access mode | |
| Git clone puts real files on PVC | |
| Ref switching updates PVC contents and status | |
| Bad repo URL → RepoCloned=False, controller survives | |
| Missing secret → Ready=False, controller recovers when secret created | |
| Paused CR → no PVC, Ready=False/Paused, unpause works | |
| CR deletion → finalizer runs, ConfigMap deleted, PVC GC'd | |
| Multiple CRs isolated from each other | |
| Rapid ref changes → consistent final state | |
| Ignition gateway unaffected throughout all operations | |
| Operator pod has 0 restarts and no ERROR logs | |
