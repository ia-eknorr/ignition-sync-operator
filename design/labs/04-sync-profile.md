# Lab 04 — SyncProfile CRD

## Objective

Validate the SyncProfile CRD: installation, validation, the 3-tier config precedence model (Stoker → SyncProfile → pod annotation), backward compatibility with 2-tier mode, and graceful degradation on profile deletion. This lab confirms the SyncProfile abstraction works correctly before the agent (Phase 6) relies on it for file routing.

**Prerequisite:** Complete [00 — Environment Setup](00-environment-setup.md) and [02 — Controller Core](02-controller-core.md).

---

## Lab 4.1: CRD Smoke Test

### Purpose
Verify the SyncProfile CRD is installed with expected schema, short names, and print columns.

### Steps

```bash
# Verify CRD exists
kubectl get crd syncprofiles.stoker.io -o yaml | head -30

# Verify short name
kubectl get sp -n lab

# Verify print columns show in kubectl output
kubectl get syncprofiles -n lab
```

### Expected Output
- Short name `sp` works (empty list is fine)
- Column headers include: `NAME`, `MODE`, `GATEWAYS`, `ACCEPTED`, `AGE`

---

## Lab 4.2: Create Valid SyncProfile

### Purpose
Create a valid SyncProfile and verify the controller sets the `Accepted=True` condition.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-site-profile
spec:
  mappings:
    - source: "services/site/projects"
      destination: "projects"
    - source: "services/site/config/resources/core"
      destination: "config/resources/core"
    - source: "shared/external-resources"
      destination: "config/resources/external"
  deploymentMode:
    name: "prd-cloud"
    source: "services/site/overlays/prd-cloud"
  excludePatterns:
    - "**/tag-*/MQTT Engine/"
  syncPeriod: 30
EOF
```

### What to Verify

1. **Accepted=True** (within ~5s):
   ```bash
   kubectl get syncprofile lab-site-profile -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

2. **observedGeneration matches**:
   ```bash
   kubectl get syncprofile lab-site-profile -n lab \
     -o jsonpath='{.status.observedGeneration}'
   ```
   Expected: `1`

3. **kubectl get shows columns**:
   ```bash
   kubectl get sp -n lab
   ```
   Expected: Row showing `lab-site-profile` with Mode=`prd-cloud`, Accepted=`True`

---

## Lab 4.3: Invalid SyncProfile — Path Traversal

### Purpose
Verify that SyncProfile with path traversal (`..`) is rejected with `Accepted=False`.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: bad-traversal
spec:
  mappings:
    - source: "../../../etc/passwd"
      destination: "config"
EOF
```

### What to Verify

1. **Accepted=False**:
   ```bash
   kubectl get syncprofile bad-traversal -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `False`

2. **Reason contains "traversal"**:
   ```bash
   kubectl get syncprofile bad-traversal -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].message}'
   ```
   Expected: Message mentions path traversal not allowed.

### Cleanup
```bash
kubectl delete syncprofile bad-traversal -n lab
```

---

## Lab 4.4: Invalid SyncProfile — Absolute Path

### Purpose
Verify absolute paths in mappings are rejected.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: bad-absolute
spec:
  mappings:
    - source: "/etc/passwd"
      destination: "config"
EOF
```

### What to Verify

```bash
kubectl get syncprofile bad-absolute -n lab \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")]}'  | jq .
```
Expected: `status: "False"`, message mentions absolute paths.

### Cleanup
```bash
kubectl delete syncprofile bad-absolute -n lab
```

---

## Lab 4.5: Pod References SyncProfile (3-Tier Mode)

### Purpose
Verify that a pod with `stoker.io/sync-profile` annotation is correctly associated with the referenced SyncProfile, and the profile's `gatewayCount` status is updated.

### Steps

```bash
# Ensure the Stoker CR exists (from Lab 02)
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

# Wait for ref resolution
sleep 30

# Create a pod with sync-profile annotation
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-profile-test
  labels:
    app.kubernetes.io/name: gateway-profile-test
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "lab-sync"
    stoker.io/sync-profile: "lab-site-profile"
    stoker.io/gateway-name: "profile-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF
```

### What to Verify

1. **Gateway discovered by Stoker controller**:
   ```bash
   kubectl get stoker lab-sync -n lab \
     -o jsonpath='{.status.discoveredGateways[?(@.name=="profile-gw")].name}'
   ```
   Expected: `profile-gw`

2. **SyncProfile gatewayCount updated**:
   ```bash
   kubectl get syncprofile lab-site-profile -n lab \
     -o jsonpath='{.status.gatewayCount}'
   ```
   Expected: `1` (or greater, if other pods reference it)

### Cleanup
```bash
kubectl delete pod gateway-profile-test -n lab
```

---

## Lab 4.6: Pod Without SyncProfile (2-Tier Backward Compatibility)

> **Outdated:** This lab references the removed `service-path`/`servicePath` 2-tier annotation mode, which was removed as dead code. SyncProfile is now the only sync path. This lab is preserved for historical reference but should not be executed.

---

## Lab 4.7: Multiple Gateways Share One Profile

### Purpose
Verify that multiple pods can reference the same SyncProfile and the `gatewayCount` reflects the total.

### Steps

```bash
# Create an area profile
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-area-profile
spec:
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
EOF

# Create 3 pods referencing it
for i in 1 2 3; do
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-area-${i}
  labels:
    app.kubernetes.io/name: gateway-area-${i}
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "lab-sync"
    stoker.io/sync-profile: "lab-area-profile"
    stoker.io/gateway-name: "area${i}"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF
done

sleep 15
```

### What to Verify

1. **All 3 gateways discovered**:
   ```bash
   kubectl get stoker lab-sync -n lab \
     -o json | jq '[.status.discoveredGateways[].name] | sort'
   ```
   Expected: List includes `area1`, `area2`, `area3`

2. **Profile gatewayCount is 3**:
   ```bash
   kubectl get syncprofile lab-area-profile -n lab \
     -o jsonpath='{.status.gatewayCount}'
   ```
   Expected: `3`

### Cleanup
```bash
kubectl delete pod gateway-area-1 gateway-area-2 gateway-area-3 -n lab
```

---

## Lab 4.8: Profile Update Triggers Re-Reconcile

### Purpose
Verify that updating a SyncProfile triggers the Stoker controller to re-reconcile affected gateways.

### Steps

```bash
# Create a pod using lab-site-profile
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-update-test
  labels:
    app.kubernetes.io/name: gateway-update-test
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "lab-sync"
    stoker.io/sync-profile: "lab-site-profile"
    stoker.io/gateway-name: "update-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

sleep 10

# Update the profile — add a new mapping
kubectl patch syncprofile lab-site-profile -n lab --type=merge \
  -p '{"spec":{"mappings":[{"source":"services/site/projects","destination":"projects"},{"source":"services/site/config/resources/core","destination":"config/resources/core"},{"source":"shared/external-resources","destination":"config/resources/external"},{"source":"shared/new-mapping","destination":"new-dest"}]}}'
```

### What to Verify

1. **Profile still Accepted=True**:
   ```bash
   kubectl get syncprofile lab-site-profile -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

2. **Operator logs show re-reconcile** triggered by profile change:
   ```bash
   kubectl logs -n stoker-system -l control-plane=controller-manager --tail=20 | grep -i "reconcil"
   ```
   Expected: Recent reconciliation log lines for `lab-sync`

### Cleanup
```bash
kubectl delete pod gateway-update-test -n lab
```

---

## Lab 4.9: Profile Deletion — Graceful Degradation

### Purpose
Verify that deleting a SyncProfile referenced by pods triggers a warning but doesn't crash the controller or break existing gateways.

### Steps

```bash
# Create a temporary profile
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: temp-profile
spec:
  mappings:
    - source: "services/temp"
      destination: "temp"
EOF

# Create a pod referencing it
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-temp
  labels:
    app.kubernetes.io/name: gateway-temp
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "lab-sync"
    stoker.io/sync-profile: "temp-profile"
    stoker.io/gateway-name: "temp-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

sleep 10

# Delete the profile
kubectl delete syncprofile temp-profile -n lab
sleep 10
```

### What to Verify

1. **Controller still running**:
   ```bash
   kubectl get pods -n stoker-system \
     -o jsonpath='{.items[0].status.phase}'
   ```
   Expected: `Running`

2. **Warning logged** about missing profile:
   ```bash
   kubectl logs -n stoker-system -l control-plane=controller-manager --tail=30 | grep -i "profile\|warning"
   ```
   Expected: Warning about profile `temp-profile` not found.

3. **Stoker CR still healthy**:
   ```bash
   kubectl get stoker lab-sync -n lab -o jsonpath='{.status.refResolutionStatus}'
   ```
   Expected: Still `Resolved`

### Cleanup
```bash
kubectl delete pod gateway-temp -n lab
```

---

## Lab 4.10: SyncProfile with Paused=true

### Purpose
Verify that a paused profile is still accepted but signals gateways to halt sync.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: paused-profile
spec:
  paused: true
  mappings:
    - source: "services/gateway"
      destination: "."
EOF
```

### What to Verify

1. **Accepted=True** (paused doesn't affect validation):
   ```bash
   kubectl get syncprofile paused-profile -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

2. **Paused field preserved**:
   ```bash
   kubectl get syncprofile paused-profile -n lab \
     -o jsonpath='{.spec.paused}'
   ```
   Expected: `true`

### Cleanup
```bash
kubectl delete syncprofile paused-profile -n lab
```

---

## Lab 4.12: Profile with `dependsOn`

### Purpose
Verify the CRD accepts the `dependsOn` field for profile dependency ordering (e.g., area profile depends on site profile being synced first).

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-depends-on
spec:
  dependsOn:
    - profileName: "lab-site-profile"
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
EOF
```

### What to Verify

1. **CRD accepts the field** (no validation error on apply)

2. **Field roundtrips**:
   ```bash
   kubectl get syncprofile lab-depends-on -n lab \
     -o jsonpath='{.spec.dependsOn[0].profileName}'
   ```
   Expected: `lab-site-profile`

3. **Accepted=True** (dependency declaration doesn't invalidate spec):
   ```bash
   kubectl get syncprofile lab-depends-on -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

> **Note:** Behavioral testing (agent waits for dependency to be Synced before proceeding) is covered in Phase 5 agent tests.

### Cleanup
```bash
kubectl delete syncprofile lab-depends-on -n lab
```

---

## Lab 4.13: Profile with `vars`

### Purpose
Verify the CRD accepts the `vars` map for template variables resolved by the agent at sync time. This replaces the removed `siteNumber` and `normalize` fields.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-with-vars
spec:
  vars:
    siteNumber: "1"
    region: "us-east"
  mappings:
    - source: "services/site/projects"
      destination: "projects"
EOF
```

### What to Verify

1. **Vars roundtrip**:
   ```bash
   kubectl get syncprofile lab-with-vars -n lab -o json | jq '.spec.vars'
   ```
   Expected: `{"siteNumber": "1", "region": "us-east"}`

2. **Accepted=True**:
   ```bash
   kubectl get syncprofile lab-with-vars -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

> **Note:** Template variable resolution (`{{.Vars.siteNumber}}` in destination paths) is tested in Phase 5 agent tests.

### Cleanup
```bash
kubectl delete syncprofile lab-with-vars -n lab
```

---

## Lab 4.14a: Profile with pod label routing

### Purpose
Verify the agent resolves `{{.Labels.key}}` from the gateway pod's labels at sync time. This enables per-gateway file routing without per-gateway SyncProfiles — one profile serves many gateways, each pulling files by their own labels.

### Available Variables

| Variable | Source | Description |
|----------|--------|-------------|
| `{{.GatewayName}}` | Pod annotation/label | Gateway identity |
| `{{.CRName}}` | Stoker CR | Name of the Stoker CR that owns this sync |
| `{{.Labels.key}}` | Pod labels | Any label on the gateway pod |
| `{{.Namespace}}` | Pod metadata | Pod namespace |
| `{{.Ref}}`, `{{.Commit}}` | Metadata ConfigMap | Resolved git ref and commit SHA |
| `{{.Vars.key}}` | SyncProfile | Custom variable from `spec.vars` |

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-label-routing
spec:
  mappings:
    - source: "services/{{.Labels.site}}/projects"
      destination: "projects"
      type: dir
      required: true
    - source: "services/{{.Labels.site}}/config"
      destination: "config"
      type: dir
EOF
```

### What to Verify

1. **Accepted=True** (template syntax is valid):
   ```bash
   kubectl get syncprofile lab-label-routing -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

2. **Agent resolves label** — deploy a gateway pod with `site: ignition-blue` label and check agent logs:
   ```bash
   kubectl logs -n lab -l app.kubernetes.io/name=ignition -c stoker-agent --tail=10
   ```
   Expected: `"projects":["blue"]` — the agent resolved `{{.Labels.site}}` → `ignition-blue` and synced from `services/ignition-blue/projects/`.

3. **Different label, different files** — deploy a second gateway with `site: ignition-red`. The same SyncProfile should sync the `red` project instead.

### Cleanup
```bash
kubectl delete syncprofile lab-label-routing -n lab
```

---

## Lab 4.14: Profile with `dryRun`

### Purpose
Verify the CRD accepts the `dryRun` boolean field. When true, the agent syncs to a staging directory but doesn't copy to `/ignition-data/`.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-dryrun
spec:
  dryRun: true
  mappings:
    - source: "services/site/projects"
      destination: "projects"
EOF
```

### What to Verify

1. **Field roundtrips**:
   ```bash
   kubectl get syncprofile lab-dryrun -n lab \
     -o jsonpath='{.spec.dryRun}'
   ```
   Expected: `true`

2. **Accepted=True** (dryRun doesn't invalidate spec):
   ```bash
   kubectl get syncprofile lab-dryrun -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

> **Note:** Behavioral testing (agent produces diff without applying) is covered in Phase 5 agent tests.

### Cleanup
```bash
kubectl delete syncprofile lab-dryrun -n lab
```

---

## Lab 4.15: Mapping with `required` Field

### Purpose
Verify the CRD accepts the `required` boolean on individual mappings. When `required: true`, the agent fails sync if the source path doesn't exist in the repo.

### Steps

```bash
cat <<EOF | kubectl apply -n lab -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: lab-required
spec:
  mappings:
    - source: "services/site/projects"
      destination: "projects"
      required: true
    - source: "shared/optional-extras"
      destination: "extras"
      required: false
EOF
```

### What to Verify

1. **Required field roundtrips**:
   ```bash
   kubectl get syncprofile lab-required -n lab -o json | \
     jq '[.spec.mappings[] | {source, required}]'
   ```
   Expected: First mapping `required: true`, second `required: false`

2. **Accepted=True**:
   ```bash
   kubectl get syncprofile lab-required -n lab \
     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
   ```
   Expected: `True`

> **Note:** Behavioral testing (agent fails on missing required source) is covered in Phase 5 agent tests.

### Cleanup
```bash
kubectl delete syncprofile lab-required -n lab
```

---

## Lab 4.16: Pod with `ref-override` Annotation

### Purpose
Verify that a pod with the `stoker.io/ref-override` annotation is still discovered by the controller and the annotation is preserved. The actual ref override behavior (agent-side) is tested in Phase 5.

### Steps

```bash
# Ensure Stoker CR exists (from Lab 4.5)
# Create a pod with ref-override
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-ref-override
  labels:
    app.kubernetes.io/name: gateway-ref-override
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "lab-sync"
    stoker.io/sync-profile: "lab-site-profile"
    stoker.io/gateway-name: "override-gw"
    stoker.io/ref-override: "v1.0.0-rc1"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF
```

### What to Verify

1. **Gateway discovered by controller**:
   ```bash
   kubectl get stoker lab-sync -n lab \
     -o jsonpath='{.status.discoveredGateways[?(@.name=="override-gw")].name}'
   ```
   Expected: `override-gw`

2. **Annotation preserved on pod**:
   ```bash
   kubectl get pod gateway-ref-override -n lab \
     -o jsonpath='{.metadata.annotations.stoker\.io/ref-override}'
   ```
   Expected: `v1.0.0-rc1`

> **Note:** The agent reads this annotation and uses it instead of the metadata ConfigMap's ref. The controller detects skew and sets a `RefSkew` warning condition. This behavioral flow is tested in Phase 5.

### Cleanup
```bash
kubectl delete pod gateway-ref-override -n lab
```

---

## Lab 4.17: Ignition Gateway Health Check

### Purpose
Confirm nothing in this phase affected the Ignition gateway.

### Steps

```bash
# Gateway pod health
kubectl get pod -n lab -l app.kubernetes.io/name=ignition -o json | jq '{
  phase: .items[0].status.phase,
  ready: (.items[0].status.conditions[] | select(.type=="Ready") | .status),
  restarts: .items[0].status.containerStatuses[0].restartCount
}'

# Gateway HTTP health
curl -s http://localhost:8088/StatusPing
```

### Expected
- Pod Running, Ready, 0 restarts
- StatusPing returns `200`

---

## Phase 3A Completion Checklist

| Check | Status |
|-------|--------|
| SyncProfile CRD installed with short name `sp` and print columns | |
| Valid SyncProfile → Accepted=True, observedGeneration set | |
| Path traversal (`..`) → Accepted=False | |
| Absolute paths → Accepted=False | |
| Pod with `sync-profile` annotation discovered by controller | |
| Pod without `sync-profile` works in 2-tier mode | |
| Multiple pods share one profile, gatewayCount accurate | |
| Profile update triggers re-reconcile of affected gateways | |
| Profile deletion → graceful degradation, warning logged | |
| Paused profile → still Accepted, paused flag preserved | |
| `dependsOn` field accepted and roundtrips | |
| `vars` map accepted and roundtrips | |
| `dryRun` field accepted and roundtrips | |
| `required` field on mapping accepted and roundtrips | |
| Pod with `ref-override` annotation discovered, annotation preserved | |
| Ignition gateway unaffected | |
| Operator pod has 0 restarts and no ERROR logs | |
