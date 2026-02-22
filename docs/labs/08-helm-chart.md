# Lab 08 — Helm Chart

## Objective

Validate the operator's own Helm chart for installation, configuration, upgrades, and clean removal. Test that all resources are created correctly from Helm values, that the operator works identically when installed via Helm (vs. `make deploy`), and that upgrades preserve state.

**Prerequisite:** Complete [07 — Sidecar Injection](07-sidecar-injection.md). Before starting, undeploy the kustomize-based operator to test a clean Helm install.

---

## Pre-Lab: Remove Kustomize-Based Deployment

```bash
make undeploy ignore-not-found=true
make uninstall ignore-not-found=true
# Verify operator is gone
kubectl get pods -n stoker-system 2>&1
# Expected: namespace not found or no pods
```

---

## Lab 8.1: Helm Install with Defaults

### Steps

```bash
# Install the operator chart (adjust path to chart directory)
helm upgrade --install stoker-operator ./charts/stoker-operator \
  -n stoker-system --create-namespace \
  --set image.repository=stoker-operator \
  --set image.tag=lab \
  --set image.pullPolicy=Never
```

### What to Verify

1. **All expected resources created:**
   ```bash
   kubectl get all -n stoker-system
   ```
   Expected: Deployment, ReplicaSet, Pod, Service (if webhook), ServiceAccount.

2. **CRD installed:**
   ```bash
   kubectl get crd stokers.stoker.io
   ```

3. **RBAC configured:**
   ```bash
   kubectl get clusterrole -l app.kubernetes.io/name=stoker-operator
   kubectl get clusterrolebinding -l app.kubernetes.io/name=stoker-operator
   ```

4. **Controller running:**
   ```bash
   kubectl rollout status deployment/stoker-operator-controller-manager \
     -n stoker-system --timeout=120s
   ```

5. **Previous CRs still work** (if CRD was re-installed):
   ```bash
   kubectl get stokers -n lab
   ```
   If `lab-sync` still exists, verify it reconciles. If not, recreate it:
   ```bash
   cat <<EOF | kubectl apply -n lab -f -
   apiVersion: stoker.io/v1alpha1
   kind: Stoker
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
   sleep 30
   kubectl get stoker lab-sync -n lab
   ```

---

## Lab 8.2: Custom Helm Values

### Purpose
Verify chart values override defaults correctly.

### Steps

```bash
# Upgrade with custom values
helm upgrade stoker-operator ./charts/stoker-operator \
  -n stoker-system \
  --set image.repository=stoker-operator \
  --set image.tag=lab \
  --set image.pullPolicy=Never \
  --set replicaCount=1 \
  --set resources.limits.memory=256Mi \
  --set resources.requests.memory=128Mi \
  --set resources.limits.cpu=500m \
  --set resources.requests.cpu=50m \
  --set webhook.receiverPort=9443 \
  --set leaderElection.enabled=true

kubectl rollout status deployment/stoker-operator-controller-manager \
  -n stoker-system --timeout=120s
```

### What to Verify

1. **Resource limits applied:**
   ```bash
   kubectl get deployment stoker-operator-controller-manager \
     -n stoker-system \
     -o jsonpath='{.spec.template.spec.containers[0].resources}' | jq .
   ```
   Expected: Memory limits = 256Mi, requests = 128Mi.

2. **Leader election enabled:**
   ```bash
   kubectl get deployment stoker-operator-controller-manager \
     -n stoker-system \
     -o jsonpath='{.spec.template.spec.containers[0].args}'
   ```
   Expected: Contains `--leader-elect`.

3. **Webhook port configured:**
   Check container args or env for `--webhook-receiver-port=9443`.

---

## Lab 8.3: Helm Upgrade — No Downtime

### Purpose
Verify `helm upgrade` performs a rolling update without losing state.

### Steps

```bash
# Record current state
COMMIT_BEFORE=$(kubectl get stoker lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
echo "Commit before upgrade: $COMMIT_BEFORE"

# Perform upgrade (change a value to trigger new rollout)
helm upgrade stoker-operator ./charts/stoker-operator \
  -n stoker-system \
  --set image.repository=stoker-operator \
  --set image.tag=lab \
  --set image.pullPolicy=Never \
  --set resources.limits.memory=192Mi \
  --reuse-values

kubectl rollout status deployment/stoker-operator-controller-manager \
  -n stoker-system --timeout=120s
```

### What to Verify

1. **Controller restarted cleanly:**
   ```bash
   kubectl get pods -n stoker-system
   ```
   Expected: New pod Running, old pod terminated.

2. **CR state preserved:**
   ```bash
   COMMIT_AFTER=$(kubectl get stoker lab-sync -n lab -o jsonpath='{.status.lastSyncCommit}')
   echo "Commit after upgrade: $COMMIT_AFTER"
   [ "$COMMIT_BEFORE" = "$COMMIT_AFTER" ] && echo "PASS: State preserved" || echo "INFO: State may have re-reconciled"
   ```

3. **Ignition gateway still discovered:**
   ```bash
   kubectl get stoker lab-sync -n lab -o json | jq '.status.discoveredGateways | length'
   ```
   Expected: Same count as before upgrade.

---

## Lab 8.4: Helm Uninstall — Clean Removal

### Steps

```bash
# Record what exists before
kubectl get all -n stoker-system

# Uninstall
helm uninstall stoker-operator -n stoker-system
```

### What to Verify

1. **All operator resources removed:**
   ```bash
   kubectl get all -n stoker-system 2>&1
   ```
   Expected: Empty or namespace not found.

2. **CRD behavior** — CRDs are typically NOT removed by Helm uninstall (by design):
   ```bash
   kubectl get crd stokers.stoker.io 2>&1
   ```
   Expected: CRD still exists (Helm convention — CRDs are not deleted to prevent data loss).

3. **CRs still exist** (orphaned but present):
   ```bash
   kubectl get stokers -n lab 2>&1
   ```
   Expected: `lab-sync` still exists. Without the controller, no reconciliation happens.

4. **Ignition gateway unaffected:**
   ```bash
   kubectl get pod ignition-0 -n lab -o jsonpath='{.status.phase}'
   ```
   Expected: Still `Running`.

### Re-install for remaining labs
```bash
# Re-install via kustomize for remaining labs (or re-install via Helm)
make install
make deploy IMG=stoker-operator:lab
kubectl rollout status deployment/stoker-operator-controller-manager \
  -n stoker-system --timeout=120s
```

---

## Lab 8.5: Helm Values — HMAC Secret from Kubernetes Secret

### Purpose
Verify the chart supports configuring the webhook HMAC secret from a Kubernetes Secret (not hardcoded in values).

### Steps

```bash
# Create HMAC secret
kubectl create secret generic webhook-hmac -n stoker-system \
  --from-literal=secret=my-hmac-secret-value

# Install with secret reference
helm upgrade --install stoker-operator ./charts/stoker-operator \
  -n stoker-system \
  --set image.repository=stoker-operator \
  --set image.tag=lab \
  --set image.pullPolicy=Never \
  --set webhook.hmacSecretRef.name=webhook-hmac \
  --set webhook.hmacSecretRef.key=secret

kubectl rollout status deployment/stoker-operator-controller-manager \
  -n stoker-system --timeout=120s
```

### What to Verify

```bash
kubectl get deployment stoker-operator-controller-manager \
  -n stoker-system -o json | \
  jq '.spec.template.spec.containers[0].env[] | select(.name=="WEBHOOK_HMAC_SECRET")'
```

Expected: env var sourced from the Secret via `secretKeyRef`, not a literal value.

---

## Phase 7 Completion Checklist

| Check | Status |
|-------|--------|
| Helm install creates all expected resources | |
| CRD installed by chart | |
| RBAC (ClusterRole, ClusterRoleBinding) configured | |
| Controller runs and reconciles CRs | |
| Custom values (resources, ports, leader election) applied | |
| Helm upgrade → rolling update, no state loss | |
| Helm uninstall → clean removal (CRD preserved by convention) | |
| CRs survive uninstall/reinstall cycle | |
| HMAC secret configurable via Kubernetes Secret reference | |
| Ignition gateway unaffected by operator install/upgrade/uninstall | |
