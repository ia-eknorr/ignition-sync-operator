# Lab 06 — Sidecar Injection

## Objective

Validate the mutating webhook that automatically injects the sync agent sidecar into Ignition gateway pods. After this phase, users no longer need to manually patch StatefulSets — they just add `ignition-sync.io/inject: "true"` to their pod template and the agent appears automatically.

**Prerequisite:** Complete [05 — Sync Agent](05-sync-agent.md). The agent binary must be proven to work. Remove any manual sidecar patches from the Ignition StatefulSet before starting.

---

## Pre-Lab: Clean Up Manual Sidecar

Remove the manually-added agent container from Lab 05:

```bash
# Revert to clean Ignition StatefulSet (re-install via helm)
helm upgrade --install ignition inductiveautomation/ignition \
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

## Lab 6.1: MutatingWebhookConfiguration Exists

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

## Lab 6.2: Injection — Pod With Annotation Gets Agent Sidecar

### Purpose
Add `ignition-sync.io/inject: "true"` to the Ignition StatefulSet and verify the agent container is automatically injected.

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

2. **Agent container has correct mounts:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name != "ignition") | {
     name,
     image,
     volumeMounts: [.volumeMounts[] | .mountPath]
   }]'
   ```
   Expected: `/repo` (read-only, from PVC) and `/usr/local/bin/ignition/data` (read-write, shared)

3. **Agent container has env vars from annotations:**
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name != "ignition") | .env[] | {(.name): .value}] | add'
   ```
   Expected: `GATEWAY_NAME`, `CR_NAME`, `CR_NAMESPACE`, etc. populated from annotations + CR spec

4. **Shared volume mount** — agent and ignition container share the data volume:
   ```bash
   kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | {name, mounts: [.volumeMounts[] | select(.mountPath | contains("ignition"))]}]'
   ```

5. **Agent is running and syncing:**
   ```bash
   kubectl logs ignition-0 -n lab -c sync-agent --tail=20
   ```

---

## Lab 6.3: No Injection — Pod Without Annotation

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

## Lab 6.4: Annotation Values Propagated to Agent Env Vars

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
kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name != "ignition") | .env[] | {(.name): .value}] | add'
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

## Lab 6.5: Injected Container Meets Pod Security Standards

### Purpose
Verify the injected sidecar container follows Kubernetes pod security standards (non-root, read-only root filesystem, no privilege escalation).

### Steps

```bash
kubectl get pod ignition-0 -n lab -o json | jq '.spec.containers[] | select(.name != "ignition") | .securityContext'
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

## Lab 6.6: Injection with auto-derived CR Name

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
kubectl get pod ignition-0 -n lab -o json | jq '[.spec.containers[] | select(.name != "ignition") | .env[] | select(.name=="CR_NAME")]'
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

## Lab 6.7: Full Round Trip — Injection + Sync + Scan + Ignition UI

### Purpose
The ultimate integration test. With injection enabled, change the git ref and verify projects update in the Ignition web UI without any manual intervention.

### Steps

```bash
# Start at v1.0.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"v1.0.0"}}}'
sleep 60

# Check what views exist in Ignition
kubectl exec ignition-0 -n lab -c sync-agent -- \
  ls /usr/local/bin/ignition/data/projects/MyProject/com.inductiveautomation.perspective/views/ 2>/dev/null
echo "^ Should only have MainView"

# Switch to v2.0.0
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"v2.0.0"}}}'
sleep 60

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
| Agent container has correct volume mounts (repo RO, data RW) | |
| All annotation values propagated to agent env vars | |
| Injected container meets pod security standards | |
| Auto-derived CR name when only one CR in namespace | |
| Full round trip: injection → sync → scan → Ignition UI shows projects | |
| Ref change propagates through injected agent to Ignition | |
| Ignition gateway healthy with injected sidecar | |
| Operator pod 0 restarts | |
