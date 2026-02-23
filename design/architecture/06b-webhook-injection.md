<!-- Part of: Stoker Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-stoker-agent.md, 06a-agent-development-plan.md -->

# Stoker — Mutating Webhook for Sidecar Injection

## Overview

The mutating admission webhook automatically injects the sync agent sidecar into Ignition gateway pods. When a pod is created with the annotation `stoker.io/inject: "true"`, the webhook intercepts the admission request and patches the pod spec with the agent container, volumes, and environment variables.

This replaces manual sidecar configuration in Helm values files. Users annotate their pods; the operator handles everything else.

```
Pod CREATE request
  ↓
MutatingWebhookConfiguration (namespaceSelector: label stoker.io/injection=enabled)
  ↓
Webhook handler (/mutate-v1-pod)
  ├─ Check annotation stoker.io/inject == "true"
  │   └─ No? → return Allowed (no-op, <1ms)
  ├─ Read annotations (cr-name, sync-profile, gateway-name)
  ├─ Fetch Stoker CR from same namespace
  ├─ Fetch SyncProfile if specified
  ├─ Build agent container spec (image, env, volumes, security)
  ├─ Inject as native sidecar (initContainer + restartPolicy: Always)
  └─ Return JSON patch
  ↓
Pod created with agent sidecar
```

---

## Expert Review Summary

Five expert reviews informed this design. Key consensus points and disagreements are documented below so future readers understand _why_ each decision was made.

### Consensus (all 5 agents agreed)

| Decision | Rationale |
|----------|-----------|
| Same binary as controller | Avoids separate deployment, follows cert-manager/Istio pattern. Webhook server already scaffolded in `cmd/controller/main.go`. |
| `failurePolicy: Ignore` | Webhook outage must never block pod creation. Compensate with controller-side missing-sidecar detection. |
| Native sidecar (`initContainer` + `restartPolicy: Always`) | K8s 1.29+ GA feature. Ensures agent starts before gateway, survives restarts, gates readiness. |
| cert-manager for TLS | Standard K8s pattern. Kustomize sections already commented out and ready to enable. |
| `namespaceSelector` for scope control | Opt-in model: namespace needs label `stoker.io/injection: enabled`. Prevents accidental injection. |
| Annotation-based injection trigger | Follows Istio/OTel/Datadog pattern: `namespaceSelector` for coarse scoping, annotation checked in handler with early return. No `objectSelector` — keeps all config in `podAnnotations`. |
| `admission.Handler` (not `webhook.Defaulter`) | Raw handler gives full control over JSON patch construction and error messaging. |
| Auto-derive CR name | When exactly 1 Stoker CR exists in namespace, `cr-name` annotation is optional. |
| `AgentSpec` needed on CRD | Image, resources, and security context must be configurable per-CR, not hardcoded. |

### Disagreements & Resolutions

| Topic | Position A | Position B | Resolution |
|-------|-----------|-----------|------------|
| **ServiceAccount** | Security: replace pod SA with dedicated agent SA | K8s: bind agent role to gateway's SA via ClusterRoleBinding | **Keep existing ClusterRoleBinding** (`system:serviceaccounts` in `agent_role.yaml`). K8s doesn't support multiple SAs per pod. The agent's ConfigMap/CR permissions are read-only except status writes. |
| **Error behavior** | DX: Deny with helpful error messages | K8s/Scale: always Allow, never block | **Deny only when webhook IS reached** (missing CR, paused CR, missing SyncProfile). Since `failurePolicy: Ignore`, pods still create if webhook is down. When the webhook runs, it should fail loudly with actionable messages. |
| **fsGroup for UID** | Security: set `fsGroup: 2003` (Ignition UID) | Implementation: webhook can't set pod-level securityContext | **Not set by webhook.** Ignition Helm chart already manages pod-level security context. Agent container omits `RunAsUser` so it inherits the pod-level UID (e.g., 2003 for Ignition), ensuring shared volume files have correct ownership. |
| **Image versioning** | 3-tier: annotation > CR spec > Helm default | 2-tier: CR spec > Helm default | **3-tier adopted.** Annotation override (`stoker.io/agent-image`) enables per-pod debugging without CR changes. Rarely used but valuable for incident response. |

---

## Webhook Configuration

### MutatingWebhookConfiguration

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: stoker-pod-injection
  annotations:
    cert-manager.io/inject-ca-from: stoker-system/stoker-webhook-cert
webhooks:
  - name: pod-inject.stoker.io
    admissionReviewVersions: ["v1"]
    clientConfig:
      service:
        name: stoker-controller-manager-webhook
        namespace: stoker-system
        path: /mutate-v1-pod
    failurePolicy: Ignore
    matchPolicy: Equivalent
    reinvocationPolicy: IfNeeded
    namespaceSelector:
      matchExpressions:
        - key: stoker.io/injection
          operator: In
          values: ["enabled"]
    rules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE"]
        resources: ["pods"]
        scope: Namespaced
    sideEffects: None
    timeoutSeconds: 10
```

**Filtering model (Istio/OTel/Datadog pattern):**

1. **`namespaceSelector`** — namespace must have label `stoker.io/injection: enabled`. This is the safety perimeter. Namespaces without the label never see webhook traffic.

2. **Annotation check in handler** — the handler checks `stoker.io/inject: "true"` annotation and returns `Allowed` immediately (~1ms) for non-annotated pods. No `objectSelector` is used — this keeps all pod configuration in `podAnnotations`, matching the Istio/OTel/Datadog convention.

**Why no `objectSelector`?**

- `objectSelector` only supports labels, which would force users to configure both `podLabels` and `podAnnotations` in Helm — an unnecessary split
- The handler's early return for non-annotated pods is sub-millisecond, so performance is not a concern
- Istio handles this pattern at massive scale with no issues

### Namespace Opt-In

Administrators enable injection per-namespace:

```bash
kubectl label namespace site1 stoker.io/injection=enabled
```

The Helm chart can automate this for target namespaces via a values flag.

### cert-manager Certificate

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: stoker-webhook-cert
  namespace: stoker-system
spec:
  secretName: stoker-webhook-cert
  dnsNames:
    - stoker-controller-manager-webhook.stoker-system.svc
    - stoker-controller-manager-webhook.stoker-system.svc.cluster.local
  issuerRef:
    name: selfsigned-issuer
    kind: Issuer
```

---

## Annotation Model

All injection configuration lives in `podAnnotations` — no labels required on the pod itself.

### Required Annotations (on pod template)

| Annotation | Required? | Description |
| ---------- | --------- | ----------- |
| `stoker.io/inject` | Yes | Set to `"true"` to trigger sidecar injection. Checked by handler with early return. |
| `stoker.io/cr-name` | No | Name of the Stoker CR. **Auto-derived** if exactly 1 CR exists in namespace. |
| `stoker.io/sync-profile` | Yes* | Name of the SyncProfile to use. *Required for 3-tier mode (recommended). |

### Optional Annotations

| Annotation | Default | Description |
|------------|---------|-------------|
| `stoker.io/gateway-name` | pod label `app.kubernetes.io/name`, then pod name | Override gateway identity for status reporting. |
| `stoker.io/ref-override` | _(none)_ | Override git ref for this pod only. Dev/test use. |
| `stoker.io/agent-image` | _(from CR spec)_ | Override agent image for this pod. Debugging use. |
| `stoker.io/exclude-patterns` | _(none)_ | Comma-separated additional exclude globs. |

### Ignition Helm Chart Example

```yaml
gateway:
  podAnnotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "proveit-sync"
    stoker.io/sync-profile: "proveit-area"
```

Minimal (single CR in namespace, auto-derived):

```yaml
gateway:
  podAnnotations:
    stoker.io/inject: "true"
    stoker.io/sync-profile: "proveit-area"
```

---

## CRD Changes: AgentSpec

The webhook reads agent configuration from the Stoker CR. A new `spec.agent` field provides image, resources, and security defaults.

```go
// AgentSpec configures the sync agent sidecar injected by the webhook.
type AgentSpec struct {
    // image configures the agent container image.
    // +optional
    Image AgentImageSpec `json:"image,omitempty"`

    // resources configures the agent container resource requirements.
    // +optional
    Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AgentImageSpec configures the agent container image.
type AgentImageSpec struct {
    // repository is the container image repository.
    // +kubebuilder:default="ghcr.io/ia-eknorr/stoker-agent"
    // +optional
    Repository string `json:"repository,omitempty"`

    // tag is the container image tag.
    // +kubebuilder:default="latest"
    // +optional
    Tag string `json:"tag,omitempty"`

    // pullPolicy is the image pull policy.
    // +kubebuilder:default="IfNotPresent"
    // +optional
    PullPolicy string `json:"pullPolicy,omitempty"`
}
```

Added to `StokerSpec`:

```go
type StokerSpec struct {
    // ... existing fields ...

    // agent configures the sync agent sidecar injected by the mutating webhook.
    // +optional
    Agent AgentSpec `json:"agent,omitempty"`
}
```

**Image Resolution Order (3-tier):**

```
pod annotation stoker.io/agent-image  →  (highest priority, debugging)
CR spec.agent.image                          →  (normal configuration)
operator Helm chart defaults                 →  (fallback, set via env var on controller)
```

---

## Injection Logic

### What Gets Injected

The webhook patches the pod spec with:

1. **Native sidecar container** — `initContainers` entry with `restartPolicy: Always`
2. **emptyDir volume** — `/repo` for the agent's local git clone
3. **Secret volumes** — git credentials and Ignition API key (projected, read-only)
4. **Environment variables** — all values from `internal/agent/config.go`
5. **Injection annotation** — `stoker.io/injected: "true"` for tracking

### Agent Container Spec

```yaml
initContainers:
  - name: stoker-agent
    restartPolicy: Always    # Native sidecar — survives, restarts with pod
    image: ghcr.io/ia-eknorr/stoker-agent:1.0.0
    env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      - name: POD_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
      - name: CR_NAME
        value: "proveit-sync"           # from annotation or auto-derived
      - name: CR_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
      - name: GATEWAY_NAME
        value: ""                        # from annotation, empty = fallback to pod name
      - name: SYNC_PROFILE
        value: "proveit-area"            # from annotation
      - name: REPO_PATH
        value: "/repo"
      - name: DATA_PATH
        value: "/ignition-data"
      - name: GATEWAY_PORT
        value: "8043"                    # from CR spec.gateway.port
      - name: GATEWAY_TLS
        value: "true"                    # from CR spec.gateway.tls
      - name: API_KEY_FILE
        value: "/etc/stoker/api-key/apiKey"
      - name: GIT_SSH_KEY_FILE
        value: "/etc/stoker/git-credentials/ssh-privatekey"  # or GIT_TOKEN_FILE
      - name: SYNC_PERIOD
        value: "30"
    volumeMounts:
      - name: sync-repo
        mountPath: /repo
      - name: ignition-data
        mountPath: /ignition-data
      - name: git-credentials
        mountPath: /etc/stoker/git-credentials
        readOnly: true
      - name: api-key
        mountPath: /etc/stoker/api-key
        readOnly: true
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 256Mi
    securityContext:
      runAsNonRoot: true
      # RunAsUser intentionally omitted — inherits pod-level UID (e.g., 2003
      # for Ignition) so files on the shared data volume have correct ownership.
      readOnlyRootFilesystem: true
      allowPrivilegeEscalation: false
      seccompProfile:
        type: RuntimeDefault
      capabilities:
        drop: ["ALL"]
```

### Volumes Injected

```yaml
volumes:
  - name: sync-repo
    emptyDir: {}                        # Agent's local git clone
  - name: git-credentials
    secret:
      secretName: "git-sync-secret"     # from CR spec.git.auth
      defaultMode: 0400                 # restrictive permissions
  - name: api-key
    secret:
      secretName: "ignition-api-key"    # from CR spec.gateway.apiKeySecretRef.name
      defaultMode: 0400
```

**Note:** The `ignition-data` volume mount assumes the Ignition Helm chart already defines this volume on the main container (mounted at `/usr/local/bin/ignition/data/`). The webhook adds a `volumeMount` to the agent but does not create the volume itself — the Ignition chart owns it.

### Idempotency Guard

The handler checks if injection already happened before patching:

```go
func isAlreadyInjected(pod *corev1.Pod) bool {
    for _, c := range pod.Spec.InitContainers {
        if c.Name == "stoker-agent" {
            return true
        }
    }
    return false
}
```

If already injected, the webhook returns `admission.Allowed("already injected")` without modification.

---

## Handler Implementation

### File: `internal/webhook/inject.go`

```go
// PodInjector implements admission.Handler for sidecar injection.
type PodInjector struct {
    client  client.Client
    decoder admission.Decoder
}

func (p *PodInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
    pod := &corev1.Pod{}
    if err := p.decoder.Decode(req, pod); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }

    // Early return for non-annotated pods (~1ms, no network calls)
    if pod.Annotations[stokertypes.AnnotationInject] != "true" {
        return admission.Allowed("injection not requested")
    }

    // Idempotency: skip if already injected
    if isAlreadyInjected(pod) {
        return admission.Allowed("already injected")
    }

    // Resolve CR name (annotation or auto-derive)
    crName, err := p.resolveCRName(ctx, pod)
    if err != nil {
        return admission.Denied(err.Error())
    }

    // Fetch Stoker CR
    var stk stokerv1alpha1.Stoker
    key := types.NamespacedName{Name: crName, Namespace: req.Namespace}
    if err := p.client.Get(ctx, key, &stk); err != nil {
        if apierrors.IsNotFound(err) {
            return admission.Denied(fmt.Sprintf(
                "Stoker CR '%s' not found in namespace '%s'", crName, req.Namespace))
        }
        return admission.Errored(http.StatusInternalServerError, err)
    }

    // Check if CR is paused
    if stk.Spec.Paused {
        return admission.Denied(fmt.Sprintf(
            "Stoker CR '%s' is paused", crName))
    }

    // Validate SyncProfile if specified
    profileName := pod.Annotations[stokertypes.AnnotationSyncProfile]
    if profileName != "" {
        var profile stokerv1alpha1.SyncProfile
        profileKey := types.NamespacedName{Name: profileName, Namespace: req.Namespace}
        if err := p.client.Get(ctx, profileKey, &profile); err != nil {
            if apierrors.IsNotFound(err) {
                return admission.Denied(fmt.Sprintf(
                    "SyncProfile '%s' not found in namespace '%s'", profileName, req.Namespace))
            }
            return admission.Errored(http.StatusInternalServerError, err)
        }
    }

    // Inject sidecar
    if err := p.injectSidecar(pod, &stk); err != nil {
        return admission.Errored(http.StatusInternalServerError, err)
    }

    // Return patch
    marshaledPod, err := json.Marshal(pod)
    if err != nil {
        return admission.Errored(http.StatusInternalServerError, err)
    }
    return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}
```

### CR Name Auto-Derivation

```go
func (p *PodInjector) resolveCRName(ctx context.Context, pod *corev1.Pod) (string, error) {
    if crName := pod.Annotations[stokertypes.AnnotationCRName]; crName != "" {
        return crName, nil
    }

    // Auto-discover: list CRs in namespace
    var list stokerv1alpha1.StokerList
    if err := p.client.List(ctx, &list, client.InNamespace(pod.Namespace)); err != nil {
        return "", fmt.Errorf("failed to list Stoker CRs: %w", err)
    }

    switch len(list.Items) {
    case 0:
        return "", fmt.Errorf("no Stoker CR found in namespace '%s'", pod.Namespace)
    case 1:
        return list.Items[0].Name, nil
    default:
        names := make([]string, len(list.Items))
        for i, item := range list.Items {
            names[i] = item.Name
        }
        return "", fmt.Errorf(
            "multiple Stoker CRs in namespace '%s': %v — set annotation '%s' explicitly",
            pod.Namespace, names, stokertypes.AnnotationCRName)
    }
}
```

### Registration in `cmd/controller/main.go`

```go
// Register mutating webhook for pod injection
mgr.GetWebhookServer().Register("/mutate-v1-pod", &webhook.Admission{
    Handler: &iswebhook.PodInjector{
        Client:  mgr.GetClient(),
        Decoder: admission.NewDecoder(mgr.GetScheme()),
    },
})
```

---

## Compensating Controls: Missing-Sidecar Detection

Since `failurePolicy: Ignore` means pods can be created without the webhook running, the controller must detect and report missing sidecars.

### Controller-Side Detection

In `gateway_discovery.go`, when discovering gateway pods, check for the agent container:

```go
func hasSyncAgent(pod *corev1.Pod) bool {
    for _, c := range pod.Spec.InitContainers {
        if c.Name == "stoker-agent" {
            return true
        }
    }
    return false
}
```

If a pod has `stoker.io/inject: "true"` annotation but no `stoker-agent` container, the controller:

1. Sets condition `SidecarMissing` on the Stoker CR
2. Emits a Kubernetes Event: `"Pod {name} has inject annotation but no stoker-agent sidecar — webhook may have been unavailable during pod creation. Delete and recreate the pod."`
3. Reports in `status.discoveredGateways[].syncStatus = "MissingSidecar"`

This ensures operators are notified even if the webhook was down during pod creation.

---

## Security

### Pod Security Standards

The injected container meets `restricted` PSS:
- `runAsNonRoot: true`
- No explicit `runAsUser` — inherits the pod-level UID (e.g., 2003 for Ignition) so files on the shared data volume have correct ownership
- `readOnlyRootFilesystem: true`
- `allowPrivilegeEscalation: false`
- `seccompProfile.type: RuntimeDefault`
- `capabilities.drop: ["ALL"]`

### Secret Isolation

Git credentials and API keys are mounted as projected volumes with `defaultMode: 0400`. They are mounted only into the `stoker-agent` container, not the main Ignition container. The main container never sees these secrets.

### Audit Trail

The webhook emits Kubernetes Events on the Stoker CR for:
- Successful injection: `"Injected stoker-agent into pod {name}"`
- Denied injection: `"Denied injection for pod {name}: {reason}"`
- Auto-derived CR: `"Auto-derived CR name '{crName}' for pod {name}"`

---

## New Constants (`pkg/types/annotations.go`)

### Labels (on namespaces, not pods)

```go
// LabelNamespaceInjection enables webhook injection for a namespace via namespaceSelector.
// Applied to namespaces: kubectl label namespace site1 stoker.io/injection=enabled
LabelNamespaceInjection = AnnotationPrefix + "/injection"
```

### Annotations (new, for webhook use)

```go
// AnnotationInjected is set by the webhook after successful injection for tracking.
AnnotationInjected = AnnotationPrefix + "/injected"

// AnnotationAgentImage overrides the agent image for a specific pod.
// Format: "repo:tag" or "repo@sha256:digest"
AnnotationAgentImage = AnnotationPrefix + "/agent-image"
```

---

## Files to Create/Modify

### New Files

| File | Purpose |
|------|---------|
| `internal/webhook/inject.go` | `PodInjector` admission handler |
| `internal/webhook/inject_test.go` | Unit tests for injection logic |
| `config/webhook/manifests.yaml` | `MutatingWebhookConfiguration` |
| `config/webhook/service.yaml` | Service for webhook endpoint |
| `config/webhook/kustomization.yaml` | Webhook kustomize overlay |
| `config/certmanager/certificate.yaml` | cert-manager Certificate |
| `config/certmanager/kustomization.yaml` | cert-manager kustomize overlay |

### Modified Files

| File | Change |
|------|--------|
| `api/v1alpha1/stoker_types.go` | Add `AgentSpec`, `AgentImageSpec`, `spec.agent` field |
| `pkg/types/annotations.go` | Add `LabelNamespaceInjection`, `AnnotationInjected`, `AnnotationAgentImage` |
| `cmd/controller/main.go` | Register `/mutate-v1-pod` handler |
| `internal/controller/gateway_discovery.go` | Add missing-sidecar detection |
| `config/default/kustomization.yaml` | Uncomment `../webhook` and `../certmanager` |
| `config/rbac/role.yaml` | Add read permission for SyncProfiles (for webhook handler) |

---

## Implementation Phases

### Phase 1: Foundation (webhook infrastructure)

1. Add `AgentSpec` types to CRD, regenerate manifests
2. Add new constants to `pkg/types/annotations.go`
3. Create `config/webhook/` and `config/certmanager/` kustomize directories
4. Create `MutatingWebhookConfiguration` manifest
5. Uncomment webhook/certmanager in `config/default/kustomization.yaml`

### Phase 2: Handler (injection logic)

6. Implement `PodInjector` in `internal/webhook/inject.go`
7. Implement `injectSidecar()` — builds container, volumes, env vars
8. Implement `resolveCRName()` — auto-derivation logic
9. Implement `isAlreadyInjected()` — idempotency guard
10. Register handler at `/mutate-v1-pod` in `cmd/controller/main.go`

### Phase 3: Compensating controls

11. Add `hasSyncAgent()` check to gateway discovery
12. Emit `SidecarMissing` condition and Event when sidecar is absent
13. Add `SidecarMissing` to the condition type constants

### Phase 4: Testing

14. Unit tests: mock admission requests, verify patches, test error messages
15. EnvTest integration: deploy CR + SyncProfile, simulate pod admission
16. Functional test: kind cluster, real pod creation, verify agent runs
17. Lab 07 validation: run all 13+ acceptance checks

---

## Testing Strategy

### Unit Tests (`internal/webhook/inject_test.go`)

| Test Case | Assertion |
|-----------|-----------|
| Pod with inject annotation + all annotations | Agent container injected with correct env vars |
| Pod without inject annotation | `Allowed("injection not requested")`, no mutation |
| Pod with inject but missing CR | `Denied` with "not found" message |
| Pod with inject but CR is paused | `Denied` with "paused" message |
| Pod with inject but invalid SyncProfile | `Denied` with "not found" message |
| Already injected pod | `Allowed("already injected")`, no mutation |
| Auto-derive CR name (1 CR) | CR name resolved, injection succeeds |
| Auto-derive CR name (0 CRs) | `Denied` with "no CR found" message |
| Auto-derive CR name (2+ CRs) | `Denied` with "multiple CRs" message listing names |
| SSH key auth | `GIT_SSH_KEY_FILE` env var set, git-credentials volume mounted |
| Token auth | `GIT_TOKEN_FILE` env var set, git-credentials volume mounted |
| Agent image override via annotation | Annotation image used instead of CR spec |
| Agent resources from CR spec | Container resources match CR `spec.agent.resources` |

### Integration Tests (envtest)

- Full admission flow with real K8s API objects
- Verify JSON patch produces valid pod spec
- Verify volume mounts don't conflict with existing volumes
- Verify security context passes PSS restricted validation

### Functional Tests (kind cluster)

- Create namespace with injection label
- Deploy Stoker CR + SyncProfile
- Deploy Ignition gateway with inject annotation
- Verify pod has stoker-agent initContainer
- Verify agent starts and clones repo
- Remove webhook pod, create new gateway pod → verify missing-sidecar detection

---

## Migration from Manual Sidecar

### Before: Manual Helm Configuration

```yaml
# ~40 lines per gateway in values.yaml
gateway:
  initContainers:
    - name: git-sync
      image: registry.k8s.io/git-sync/git-sync:v4.4.0
      envFrom:
        - configMapRef:
            name: git-sync-env-site
      volumeMounts:
        - name: git-volume
          mountPath: /git
  volumes:
    - name: git-volume
      emptyDir: {}
    - name: git-secret
      secret:
        secretName: git-sync-secret
  # Plus ConfigMap, sync script, etc.
```

### After: Webhook Injection

```yaml
# 2 lines per gateway in values.yaml
gateway:
  podAnnotations:
    stoker.io/inject: "true"
    stoker.io/sync-profile: "proveit-area"
```

**Reduction: ~40 lines per gateway down to 2 lines.** For a 5-gateway deployment, that's ~200 lines reduced to ~10 lines, plus 1 shared Stoker CR (~25 lines) and 2 SyncProfiles (~35 lines total).

---

## Open Questions

These can be resolved during implementation:

1. **Volume name collision** — if the Ignition chart defines a volume named `sync-repo` or `git-credentials`, the webhook would create a duplicate. Should the handler check for existing volume names and skip/rename?

2. **Ignition data path** — the agent mounts to `/ignition-data`, but the Ignition chart mounts data at `/usr/local/bin/ignition/data/`. These need to be aligned — either the agent config uses the Ignition path directly, or there's a symlink.

3. **Agent image tag pinning** — should the Helm chart's default agent image tag track the controller image tag (same release), or be independently versioned?

4. **Webhook port sharing** — the controller already uses the webhook server for CRD validation (future). Should the admission webhook register on the same server, or use a separate port? Same server is simpler; separate port allows independent TLS.
