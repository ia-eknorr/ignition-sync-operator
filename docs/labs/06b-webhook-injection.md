# Lab 06b — Mutating Webhook for Sidecar Injection

**Validates:** Automatic sync-agent sidecar injection via mutating admission webhook.

**Prerequisites:**
- `kind-dev` cluster running (see Lab 00)
- Ignition Sync Operator deployed with webhook enabled
- cert-manager installed (for webhook TLS)
- Namespace `ignition-test` created and labeled

---

## 6b.1 — Verify MutatingWebhookConfiguration

```bash
kubectl get mutatingwebhookconfigurations | grep ignition-sync
```

**Expected:** One MutatingWebhookConfiguration named `*-pod-injection` exists.

```bash
kubectl get mutatingwebhookconfigurations -o jsonpath='{.items[?(@.metadata.name=="ignition-sync-operator-pod-injection")].webhooks[0].failurePolicy}'
```

**Expected:** `Ignore` (webhook outage must never block pod creation).

---

## 6b.2 — Label Namespace and Deploy Clean Gateways

Label the namespace for injection:

```bash
kubectl label namespace ignition-test ignition-sync.io/injection=enabled
```

Verify the label:

```bash
kubectl get namespace ignition-test --show-labels | grep injection
```

**Expected:** `ignition-sync.io/injection=enabled` in labels.

Deploy Ignition gateways via Helm **without** any manual sidecar patching. In the Ignition Helm values, only set annotations:

```yaml
gateway:
  podAnnotations:
    ignition-sync.io/inject: "true"
    ignition-sync.io/cr-name: "proveit-sync"
    ignition-sync.io/sync-profile: "proveit-area"
```

---

## 6b.3 — Verify Agent Sidecar Injected

After deploying a gateway pod with the inject annotation:

```bash
kubectl get pod -n ignition-test -l app.kubernetes.io/name=ignition-gateway -o jsonpath='{.items[0].spec.initContainers[*].name}'
```

**Expected:** `sync-agent` appears in the list of initContainers.

```bash
kubectl get pod -n ignition-test -l app.kubernetes.io/name=ignition-gateway -o jsonpath='{.items[0].metadata.annotations.ignition-sync\.io/injected}'
```

**Expected:** `true`

---

## 6b.4 — Pod Without Annotation = No Injection

Deploy a pod without the inject annotation:

```bash
kubectl run test-no-inject -n ignition-test --image=nginx --restart=Never
kubectl get pod test-no-inject -n ignition-test -o jsonpath='{.spec.initContainers}'
```

**Expected:** No `sync-agent` initContainer. Clean up:

```bash
kubectl delete pod test-no-inject -n ignition-test
```

---

## 6b.5 — Verify Environment Variables

```bash
kubectl get pod -n ignition-test -l app.kubernetes.io/name=ignition-gateway \
  -o jsonpath='{.items[0].spec.initContainers[?(@.name=="sync-agent")].env}' | jq .
```

**Expected env vars present:**
- `POD_NAME` (fieldRef)
- `POD_NAMESPACE` (fieldRef)
- `CR_NAME` = `proveit-sync`
- `CR_NAMESPACE` (fieldRef)
- `SYNC_PROFILE` = `proveit-area`
- `REPO_PATH` = `/repo`
- `DATA_PATH` = `/ignition-data`
- `GATEWAY_PORT` = `8043`
- `GATEWAY_TLS` = `true`
- `API_KEY_FILE` (path to secret mount)
- `GIT_TOKEN_FILE` or `GIT_SSH_KEY_FILE` (depending on auth config)
- `SYNC_PERIOD` = `30`

---

## 6b.6 — Verify Security Context (Restricted PSS)

```bash
kubectl get pod -n ignition-test -l app.kubernetes.io/name=ignition-gateway \
  -o jsonpath='{.items[0].spec.initContainers[?(@.name=="sync-agent")].securityContext}' | jq .
```

**Expected:**
```json
{
  "allowPrivilegeEscalation": false,
  "capabilities": { "drop": ["ALL"] },
  "readOnlyRootFilesystem": true,
  "runAsNonRoot": true,
  "runAsUser": 65532,
  "seccompProfile": { "type": "RuntimeDefault" }
}
```

---

## 6b.7 — Auto-Derived CR Name

If only one IgnitionSync CR exists in the namespace, the `cr-name` annotation is optional.

Deploy a gateway with only:

```yaml
gateway:
  podAnnotations:
    ignition-sync.io/inject: "true"
    ignition-sync.io/sync-profile: "proveit-area"
```

```bash
kubectl get pod -n ignition-test -l app.kubernetes.io/name=ignition-gateway \
  -o jsonpath='{.items[0].spec.initContainers[?(@.name=="sync-agent")].env[?(@.name=="CR_NAME")].value}'
```

**Expected:** The CR name is auto-derived (matches the single IgnitionSync CR name).

---

## 6b.8 — Full Round Trip

After injection and agent startup, verify the agent actually synced configuration:

```bash
# Check IgnitionSync status shows the gateway
kubectl get ignitionsync proveit-sync -n ignition-test -o jsonpath='{.status.discoveredGateways}' | jq .

# Verify gateway-info via Ignition API
curl -sk https://ignition-blue.localtest.me/system/gwinfo \
  -H 'Accept: application/json' | jq .
```

**Expected:** Gateway appears in discovered gateways with `syncStatus: Synced` (or `Pending` if agent hasn't completed first sync yet).

---

## 6b.9 — Missing Sidecar Detection

Simulate a scenario where the webhook was unavailable during pod creation.

Scale down the operator (or temporarily remove the MutatingWebhookConfiguration), then create a new pod with the inject annotation:

```bash
# The pod should be created without the sidecar (failurePolicy: Ignore)
kubectl get pod -n ignition-test <pod-name> -o jsonpath='{.spec.initContainers}'
```

**Expected:** No `sync-agent` container.

Once the operator is back and reconciles:

```bash
kubectl get ignitionsync proveit-sync -n ignition-test -o jsonpath='{.status.conditions}' | jq '.[] | select(.type=="SidecarInjected")'
```

**Expected:** Condition `SidecarInjected=False` with reason `SidecarMissing`.

```bash
kubectl get events -n ignition-test --field-selector reason=MissingSidecar
```

**Expected:** Event warning about the pod missing its sidecar.

**Resolution:** Delete and recreate the affected pod. The webhook will inject the sidecar on recreation.

---

## Summary

| Check | Description | Status |
|-------|-------------|--------|
| 6b.1 | MutatingWebhookConfiguration exists with Ignore policy | |
| 6b.2 | Namespace labeled, gateways deployed without manual sidecar | |
| 6b.3 | sync-agent injected as initContainer | |
| 6b.4 | Non-annotated pods not modified | |
| 6b.5 | All env vars propagated correctly | |
| 6b.6 | Restricted PSS security context applied | |
| 6b.7 | CR name auto-derived with single CR | |
| 6b.8 | Full sync round trip after injection | |
| 6b.9 | Missing sidecar detected and reported | |
