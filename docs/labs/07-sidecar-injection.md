# Lab 07 — Sidecar Injection

## Objective

Validate the mutating webhook that automatically injects the sync agent sidecar into Ignition gateway pods. After this phase, users no longer need to manually patch StatefulSets — they just add `ignition-sync.io/inject: "true"` to their pod template and the agent appears automatically.

The webhook injects:
- A `sync-agent` sidecar container
- An `emptyDir` volume (`sync-repo`) mounted at `/repo` for the agent to clone into
- A projected secret volume (`git-credentials`) from the git token secret referenced in the IgnitionSync CR
- Agent configuration via environment variables derived from pod annotations and CR spec

**Prerequisite:** Complete [06 — Sync Agent](06-sync-agent.md). The agent binary must be proven to work. Remove any manual sidecar patches from the Ignition StatefulSet before starting.

---

## Pre-Lab: Clean Up Manual Sidecar

Remove the manually-added agent container from Lab 05:

```bash
# Revert to clean Ignition StatefulSet (re-install via helm)
helm upgrade --install ignition inductiveautomation/ignition \
  -n lab \
  --set image.tag=8.3.3 \
  --set commissioning.edition=standard \
  --set commissioning.acceptIgnitionEULA=true \
  --set gateway.replicas=1 \
  --set gateway.resourcesEnabled=true \
  --set gateway.resources.requests.cpu=500m \
  --set gateway.resources.requests.memory=1Gi \
  --set gateway.resources.limits.cpu=1 \
  --set gateway.resources.limits.memory=2Gi \
  --set gateway.dataVolumeStorageSize=5Gi \
  --set gateway.persistentVolumeClaimRetentionPolicy=Delete \
  --set service.type=NodePort \
  --set service.nodePorts.http=30088 \
  --set service.nodePorts.https=30043 \
  --set ingress.enabled=false \
  --set certManager.enabled=false

kubectl rollout status statefulset/ignition -n lab --timeout=300s

# Re-add operator annotations
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "lab-sync"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1gateway-name", "value": "lab-gateway"}
]'
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

---

## Lab 7.1: MutatingWebhookConfiguration Exists

### Steps

```bash
kubectl get mutatingwebhookconfiguration -l app.kubernetes.io/name=ignition-sync-operator
```

### What to Verify

1. **Webhook configuration exists** with a rule matching Pod CREATE operations
2. **caBundle is populated** (not empty)
3. **Failure policy is Ignore** (not Fail — webhook outage shouldn't block pod creation)
4. **Namespace selector** is correct (should match namespaces with the webhook enabled)

```bash
kubectl get mutatingwebhookconfiguration -o json | jq '.items[] | {
  name: .metadata.name,
  rules: .webhooks[].rules,
  failurePolicy: .webhooks[].failurePolicy,
  caBundle: (.webhooks[].clientConfig.caBundle | length > 0)
}'
```

---

## Lab 7.2: Injection — Pod With Annotation Gets Agent Sidecar

### Purpose
Add `ignition-sync.io/inject: "true"` to the Ignition StatefulSet and verify the agent container is automatically injected with an emptyDir volume for repo clone and a projected git auth secret.

### Steps

```bash
# Add the inject annotation
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1inject", "value": "true"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1service-path", "value": ""}
]'

# This triggers a rolling restart
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

### What to Verify

1. **Pod has 2 containers** (ignition + sync-agent):
   ```bash
   kubectl get pod ignition-0 -n lab -o jsonpath='{.spec.containers[*].name}'
   ```
   Expected: `ignition sync-agent` (or similar)

2. **Injected volumes are emptyDir + secret** (NOT a PVC):
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.volumes[] | select(.name == "sync-repo" or .name == "git-credentials") | {
     name,
     type: (if .emptyDir then "emptyDir" elif .secret then "secret" else "unknown" end),
     detail: (.emptyDir // .secret)
   }]'
   ```
   Expected:
   - `sync-repo` — type `emptyDir`
   - `git-credentials` — type `secret` (projected from the git token secret in the IgnitionSync CR)

3. **Agent container has correct volume mounts:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name == "sync-agent") | {
     name,
     image,
     volumeMounts: [.volumeMounts[] | {mountPath, name, readOnly}]
   }]'
   ```
   Expected mounts:
   - `/repo` — from `sync-repo` emptyDir (agent clones the git repo here)
   - `/etc/git-credentials` — from `git-credentials` secret (readOnly)

4. **Agent container has env vars from annotations:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name == "sync-agent") | .env[] | {(.name): .value}] | add'
   ```
   Expected: `GATEWAY_NAME`, `CR_NAME`, `CR_NAMESPACE`, `GIT_AUTH_TOKEN_FILE`, etc. populated from annotations + CR spec

5. **Agent is running and cloning to /repo:**
   ```bash
   kubectl logs ignition-0 -n lab -c sync-agent --tail=20
   ```

---

## Lab 7.3: Verify Injected Volumes

### Purpose
Confirm the webhook injected an emptyDir (not a PVC) for repo storage, and that the git auth secret is correctly mounted.

### Steps

1. **Verify emptyDir volume exists on the pod:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '.spec.volumes[] | select(.name == "sync-repo")'
   ```
   Expected: `{"name": "sync-repo", "emptyDir": {}}`

2. **Verify git-credentials secret volume exists:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '.spec.volumes[] | select(.name == "git-credentials")'
   ```
   Expected: A secret volume projected from the git token secret referenced in the IgnitionSync CR.

3. **Verify the agent cloned the repo into the emptyDir:**
   ```bash
   kubectl exec ignition-0 -n lab -c sync-agent -- ls /repo/
   ```
   Expected: The contents of the git repository (project directories, etc.)

4. **Verify git credentials are mounted read-only:**
   ```bash
   kubectl exec ignition-0 -n lab -c sync-agent -- ls -la /etc/git-credentials/
   ```
   Expected: Token file(s) present, mounted read-only.

5. **No PVC was created by the webhook:**
   ```bash
   # Confirm no PVC named sync-repo exists — the volume is an emptyDir
   kubectl get pvc -n lab | grep sync-repo
   ```
   Expected: No results. The webhook injects emptyDir volumes, not PVCs.

---

## Lab 7.4: No Injection — Pod Without Annotation

### Purpose
Verify pods without the inject annotation are NOT modified by the webhook.

### Steps

```bash
# Deploy a plain pod without injection annotation
cat <<EOF | kubectl apply -n lab -f -
apiVersion: v1
kind: Pod
metadata:
  name: no-inject-test
  labels:
    app: no-inject-test
spec:
  containers:
    - name: main
      image: registry.k8s.io/pause:3.9
EOF

kubectl wait --for=condition=Ready pod/no-inject-test -n lab --timeout=30s
```

### What to Verify

```bash
kubectl get pod no-inject-test -n lab -o jsonpath='{.spec.containers[*].name}'
```

Expected: Only `main` — no injected sidecar container.

### Cleanup
```bash
kubectl delete pod no-inject-test -n lab
```

---

## Lab 7.5: Annotation Values Propagated to Agent Env Vars

### Purpose
Verify all supported pod annotations are correctly translated into agent environment variables.

### Steps

```bash
# Patch StatefulSet with all annotation values
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1deployment-mode", "value": "prd-cloud"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1tag-provider", "value": "my-provider"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1sync-period", "value": "15"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1exclude-patterns", "value": "**/*.bak,**/*.tmp"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1system-name", "value": "lab-system"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1system-name-template", "value": "{{.GatewayName}}-prod"}
]'

kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

### What to Verify

```bash
kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name == "sync-agent") | .env[] | {(.name): .value}] | add'
```

Expected env vars include:
- `DEPLOYMENT_MODE: "prd-cloud"`
- `TAG_PROVIDER: "my-provider"`
- `SYNC_PERIOD: "15"`
- `EXCLUDE_PATTERNS: "**/*.bak,**/*.tmp"`
- `SYSTEM_NAME: "lab-system"`
- `SYSTEM_NAME_TEMPLATE: "{{.GatewayName}}-prod"`

### Cleanup (reset annotations)

```bash
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1deployment-mode"},
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1tag-provider"},
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1sync-period"},
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1exclude-patterns"},
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1system-name"},
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1system-name-template"}
]'
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

---

## Lab 7.6: Injected Container Meets Pod Security Standards

### Purpose
Verify the injected sidecar container follows Kubernetes pod security standards (non-root, read-only root filesystem, no privilege escalation).

### Steps

```bash
kubectl get pod ignition-0 -n lab -o json | jq '.spec.containers[] | select(.name == "sync-agent") | .securityContext'
```

### Expected

```json
{
  "runAsNonRoot": true,
  "readOnlyRootFilesystem": true,
  "allowPrivilegeEscalation": false,
  "capabilities": { "drop": ["ALL"] }
}
```

---

## Lab 7.7: Injection with auto-derived CR Name

### Purpose
When only one IgnitionSync CR exists in the namespace, `ignition-sync.io/cr-name` annotation should be optional — the webhook auto-derives it.

### Steps

```bash
# Ensure only one CR exists
kubectl get ignitionsyncs -n lab
# Should show only lab-sync

# Remove cr-name annotation
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "remove", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name"}
]'
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

### What to Verify

```bash
# Agent container should still have CR_NAME set (auto-derived)
kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name == "sync-agent") | .env[] | select(.name=="CR_NAME")]'
```

Expected: `CR_NAME: "lab-sync"` (auto-derived from the only CR in namespace).

### Restore
```bash
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "lab-sync"}
]'
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

---

## Lab 7.8: Full Round Trip — Injection + Sync + Scan + Ignition UI

### Purpose
The ultimate integration test. With injection enabled, change the git ref and verify:
1. The agent resolves the new ref
2. The agent clones/updates the repo in the local emptyDir at `/repo`
3. Projects update in the Ignition web UI without any manual intervention

### How It Works

When you update the IgnitionSync CR with a new git ref:
1. **Controller resolves new ref** via `git ls-remote` -> updates the metadata ConfigMap with commit hash
2. **Agent detects ConfigMap change** via K8s watch -> clones/fetches the new commit to local emptyDir at `/repo`
3. **Agent syncs updated files** to `/usr/local/bin/ignition/data/projects/` -> triggers Ignition scan API call
4. **Ignition reloads projects** and displays updated resources in the web UI

### Steps

```bash
# Start at v1.0.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"v1.0.0"}}}'
sleep 60

# Verify ref was resolved
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | select(.type=="RefResolved")'
# Expected: status "True", reason "Resolved"

# Check repo contents on the agent's local emptyDir
kubectl exec ignition-0 -n lab -c sync-agent -- ls /repo/
echo "^ Repo should be cloned at the v1.0.0 ref"

# Check what views exist in Ignition
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/MyProject/com.inductiveautomation.perspective/views/ 2>/dev/null
echo "^ Should only have MainView"

# Switch to v2.0.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"v2.0.0"}}}'
sleep 60

# Verify ref resolved again
kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | select(.type=="RefResolved")'
# Expected: status "True", reason "Resolved"

# Check again
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/MyProject/com.inductiveautomation.perspective/views/ 2>/dev/null
echo "^ Should now have MainView AND SecondView"
```

**Observation:** Verify in the Ignition web UI that the project changes are reflected.

### Restore
```bash
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'
```

---

## Phase 6 Completion Checklist

| Check | Status |
|-------|--------|
| MutatingWebhookConfiguration exists with valid caBundle | |
| Failure policy is Ignore | |
| Pod with inject annotation gets agent sidecar | |
| Pod without inject annotation is untouched | |
| emptyDir volume (`sync-repo`) mounted at `/repo` for agent repo clone | |
| git auth secret (`git-credentials`) injected and mounted read-only | |
| Agent clones repo to local emptyDir at `/repo` | |
| No PVC created by the webhook — only emptyDir volumes | |
| All annotation values propagated to agent env vars | |
| Injected container meets pod security standards | |
| Auto-derived CR name when only one CR in namespace | |
| RefResolved condition shows status "True", reason "Resolved" after ref change | |
| Full round trip: injection -> sync -> scan -> Ignition UI shows projects | |
| Ref change propagates through injected agent to Ignition | |
| Ignition gateway healthy with injected sidecar | |
| Operator pod 0 restarts | |
