# Lab 02 — Controller Core

## Objective

Validate the controller's core reconciliation loop with a real Ignition gateway running in the cluster. We verify CRD behavior, git ref resolution via `git ls-remote`, metadata ConfigMap creation, finalizer lifecycle, ref tracking, and error recovery — all while confirming the operator doesn't interfere with Ignition gateway health.

The controller never clones a git repository. It resolves refs to commit SHAs using a single `git ls-remote` HTTP call and writes the result to a metadata ConfigMap. The agent sidecar (not the controller) is responsible for cloning repos.

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
Create a valid CR pointing to the GitHub repo and watch the full reconciliation cycle.

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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
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

2. **Ref resolved** (within ~10s):
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: `Resolved`

3. **RefResolved condition**:
   ```bash
   kubectl get ignitionsync lab-sync -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="RefResolved")].status}'
   ```
   Expected: `True`

4. **Commit SHA recorded**:
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}'
   ```
   Expected: Non-empty 40-char hex string

5. **Metadata ConfigMap created**:
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o json | jq '.data'
   ```
   Expected: `commit`, `ref`, and `trigger` keys populated

6. **Metadata ConfigMap has valid commit SHA**:
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab \
     -o jsonpath='{.data.commit}' | grep -qE '^[0-9a-f]{40}$' && echo "PASS" || echo "FAIL"
   ```
   Expected: `PASS`

7. **Ignition gateway still healthy** — the operator should not have affected it:
   ```bash
   kubectl get pod ignition-0 -n lab -o jsonpath='{.status.phase}'
   curl -s -o /dev/null -w '%{http_code}' http://localhost:8088/StatusPing
   ```
   Expected: `Running` and `200`

### Log Inspection

In the operator logs, you should see (in order):
1. `reconciling IgnitionSync` with namespace/name
2. `resolving git ref` or `git ls-remote`
3. `ref resolved` with commit SHA
4. `created metadata configmap`
5. `discovered gateways` with count

**Red flags to watch for:** Any `ERROR` lines, stack traces, or `failed to` messages.

---

## Lab 2.3: Ref Resolution Verification

### Purpose
Verify the controller's resolved commit SHA is correct by cross-referencing it against `git ls-remote` output, and confirm the metadata ConfigMap contains all expected keys.

### Steps

```bash
# Get resolved commit from CR status
RESOLVED_SHA=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
echo "Controller resolved SHA: $RESOLVED_SHA"

# Verify it's a valid 40-character hex string
echo "$RESOLVED_SHA" | grep -qE '^[0-9a-f]{40}$' && echo "PASS: Valid SHA format" || echo "FAIL: Invalid SHA format"
```

### Cross-Reference Against Remote

```bash
# Get the git token
GIT_TOKEN=$(kubectl get secret git-token-secret -n lab -o jsonpath='{.data.token}' | base64 -d)

# Run git ls-remote to get the actual remote commit for refs/heads/main
REMOTE_SHA=$(git ls-remote https://${GIT_TOKEN}@github.com/ia-eknorr/test-ignition-project.git refs/heads/main | awk '{print $1}')
echo "Remote SHA: $REMOTE_SHA"

# Compare
[ "$RESOLVED_SHA" = "$REMOTE_SHA" ] && echo "PASS: SHAs match" || echo "FAIL: SHAs differ"
```

### Verify Metadata ConfigMap Contents

```bash
# Dump all data keys
kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o json | jq '.data'
```

Expected output should contain at minimum:
```json
{
  "commit": "<40-char hex SHA>",
  "ref": "main",
  "trigger": "<RFC3339 timestamp>"
}
```

### Verify Each Key Individually

```bash
# commit key
CM_COMMIT=$(kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.commit}')
echo "$CM_COMMIT" | grep -qE '^[0-9a-f]{40}$' && echo "PASS: commit is valid SHA" || echo "FAIL: commit invalid"

# ref key
CM_REF=$(kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.ref}')
[ "$CM_REF" = "main" ] && echo "PASS: ref is main" || echo "FAIL: ref is $CM_REF"

# trigger key
CM_TRIGGER=$(kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.trigger}')
[ -n "$CM_TRIGGER" ] && echo "PASS: trigger is $CM_TRIGGER" || echo "FAIL: trigger is empty"

# commit in ConfigMap matches CR status
[ "$CM_COMMIT" = "$RESOLVED_SHA" ] && echo "PASS: ConfigMap commit matches CR status" || echo "FAIL: mismatch"
```

### What This Proves
The controller correctly resolves a git ref to its commit SHA using `git ls-remote` (no clone), records the SHA in both the CR status and the metadata ConfigMap, and the ConfigMap contains all keys (`commit`, `ref`, `trigger`) the agent sidecar will need to perform its own clone.

---

## Lab 2.4: Ref Tracking — Tag Switch

### Purpose
Verify the controller detects `spec.git.ref` changes and resolves the new ref to a different commit SHA. The repo has two commits tagged `0.1.0` (initial, 1 view) and `0.2.0` (second commit, 2 views).

### Steps

```bash
# Record current commit
COMMIT_BEFORE=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
echo "Current commit: $COMMIT_BEFORE"

# Switch to 0.1.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"0.1.0"}}}'

# Watch for commit change
kubectl get ignitionsync lab-sync -n lab -w
```

### What to Verify

1. **lastSyncRef updates** to `0.1.0`:
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncRef}'
   ```

2. **lastSyncCommit changes** to a different SHA:
   ```bash
   COMMIT_V1=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
   echo "0.1.0 commit: $COMMIT_V1"
   [ "$COMMIT_V1" != "$COMMIT_BEFORE" ] && echo "PASS: Commit changed" || echo "FAIL: Commit unchanged"
   ```

3. **Metadata ConfigMap updated with new ref and commit:**
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o json | jq '.data'
   ```
   Expected: `ref` is `0.1.0`, `commit` matches `$COMMIT_V1`.

4. **RefResolved condition still True:**
   ```bash
   kubectl get ignitionsync lab-sync -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="RefResolved")].status}'
   ```
   Expected: `True`

5. **Now switch to 0.2.0:**
   ```bash
   kubectl patch ignitionsync lab-sync -n lab --type=merge \
     -p '{"spec":{"git":{"ref":"0.2.0"}}}'
   ```

6. **Verify new resolution:**
   ```bash
   # Wait for reconcile (~10s)
   sleep 15
   COMMIT_V2=$(kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
   echo "0.2.0 commit: $COMMIT_V2"
   [ "$COMMIT_V2" != "$COMMIT_V1" ] && echo "PASS: Commit changed for 0.2.0" || echo "FAIL: Commit unchanged"
   ```

7. **Metadata ConfigMap updated:**
   ```bash
   kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.ref}'
   ```
   Expected: `0.2.0`

### Restore
```bash
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'
```

---

## Lab 2.5: Error Recovery — Bad Repository URL

### Purpose
Verify the controller handles ref resolution failures gracefully: sets error conditions, doesn't crash, and recovers when the URL is corrected.

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
    repo: "https://github.com/ia-eknorr/nonexistent-repo-does-not-exist.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### What to Verify

1. **RefResolved=False** (within ~30s):
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="RefResolved")]}'  | jq .
   ```
   Expected: `status: "False"`, `reason: "RefResolutionFailed"`

2. **refResolutionStatus is Error:**
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: `Error`

3. **Controller still running** (didn't crash):
   ```bash
   kubectl get pods -n ignition-sync-operator-system
   ```
   Expected: Controller pod Running with restart count `0`.

4. **Original CR unaffected:**
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: Still `Resolved`

5. **Fix the URL and verify recovery:**
   ```bash
   kubectl patch ignitionsync bad-repo-test -n lab --type=merge \
     -p '{"spec":{"git":{"repo":"https://github.com/ia-eknorr/test-ignition-project.git"}}}'
   ```
   Wait ~30s, then:
   ```bash
   kubectl get ignitionsync bad-repo-test -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: `Resolved` — the controller recovered.

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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
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
   kubectl get ignitionsync missing-secret-test -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: Eventually reaches `Resolved`.

### Cleanup
```bash
kubectl delete ignitionsync missing-secret-test -n lab
kubectl delete secret nonexistent-secret -n lab
```

---

## Lab 2.7: Paused CR

### Purpose
Verify `spec.paused: true` halts all operations — no ref resolution, no metadata ConfigMap, no reconciliation side effects.

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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF
```

### What to Verify (After ~20s)

1. **No metadata ConfigMap created:**
   ```bash
   kubectl get configmap ignition-sync-metadata-paused-test -n lab 2>&1
   ```
   Expected: `NotFound`

2. **refResolutionStatus is not Resolved:**
   ```bash
   kubectl get ignitionsync paused-test -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: Empty or not `Resolved`.

3. **Ready=False with reason Paused:**
   ```bash
   kubectl get ignitionsync paused-test -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'
   ```
   Expected: `Paused`

4. **Unpause and verify it starts working:**
   ```bash
   kubectl patch ignitionsync paused-test -n lab --type=merge \
     -p '{"spec":{"paused":false}}'
   sleep 30
   kubectl get ignitionsync paused-test -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: `Resolved`

5. **Metadata ConfigMap now exists:**
   ```bash
   kubectl get configmap ignition-sync-metadata-paused-test -n lab -o jsonpath='{.data.commit}'
   ```
   Expected: A valid 40-char hex SHA.

### Cleanup
```bash
kubectl delete ignitionsync paused-test -n lab
```

---

## Lab 2.8: Finalizer and Cleanup on Deletion

### Purpose
Verify the full cleanup chain when a CR is deleted: finalizer runs, metadata ConfigMap is deleted.

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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# Wait for full reconciliation
sleep 30
kubectl get ignitionsync cleanup-test -n lab -o jsonpath='{.status.refResolutionStatus}'
# Should be: Resolved
```

### Record resources before deletion:
```bash
echo "=== Before Deletion ==="
kubectl get configmap ignition-sync-metadata-cleanup-test -n lab 2>&1
kubectl get ignitionsync cleanup-test -n lab -o jsonpath='{.metadata.finalizers}'
```

### Delete and observe:
```bash
kubectl delete ignitionsync cleanup-test -n lab &
# Watch in real-time
kubectl get ignitionsync,configmap -n lab -w
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

3. **Operator logs show cleanup:**
   ```bash
   kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=20 | grep -i "cleanup\|finalizer\|deleting"
   ```

---

## Lab 2.9: Multiple CRs — Isolation

### Purpose
Verify two CRs in the same namespace don't interfere with each other. Each should have independent metadata ConfigMaps and status.

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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "0.1.0"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
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
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "0.2.0"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# Wait for both to complete
sleep 45
```

### What to Verify

1. **Both CRs resolved successfully:**
   ```bash
   kubectl get ignitionsyncs -n lab
   ```
   Expected: Both show `REF` (0.1.0 / 0.2.0) and status fields populated.

2. **Separate metadata ConfigMaps:**
   ```bash
   kubectl get configmap -n lab -l ignition-sync.io/cr-name
   ```
   Expected: `ignition-sync-metadata-multi-a` and `ignition-sync-metadata-multi-b`

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
   kubectl get ignitionsync multi-b -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: Still `Resolved`

5. **Deleted CR's ConfigMap cleaned up, other still exists:**
   ```bash
   kubectl get configmap ignition-sync-metadata-multi-a -n lab 2>&1
   kubectl get configmap ignition-sync-metadata-multi-b -n lab 2>&1
   ```
   Expected: multi-a `NotFound`, multi-b still exists.

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
# Ensure lab-sync exists and is resolved
kubectl get ignitionsync lab-sync -n lab -o jsonpath='{.status.refResolutionStatus}'

# Flip refs rapidly
for ref in 0.1.0 0.2.0 main 0.1.0 0.2.0 main; do
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
     refResolutionStatus: .status.refResolutionStatus,
     lastSyncCommit: .status.lastSyncCommit
   }'
   ```
   Expected: `lastSyncRef` matches `spec.git.ref` (which should be `main`), status is `Resolved`.

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
| Valid CR triggers full reconciliation (ref resolution, metadata ConfigMap) | |
| Ref resolution produces valid 40-char hex commit SHA | |
| Metadata ConfigMap has correct keys: commit, ref, trigger | |
| Resolved SHA matches `git ls-remote` output for the same ref | |
| Ref switching updates CR status and metadata ConfigMap | |
| Bad repo URL → RefResolved=False, controller survives | |
| Missing secret → Ready=False, controller recovers when secret created | |
| Paused CR → no metadata ConfigMap, Ready=False/Paused, unpause works | |
| CR deletion → finalizer runs, metadata ConfigMap deleted | |
| Multiple CRs isolated with separate metadata ConfigMaps | |
| Rapid ref changes → consistent final state | |
| Ignition gateway unaffected throughout all operations | |
| Operator pod has 0 restarts and no ERROR logs | |
