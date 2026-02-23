<!-- Part of: Stoker Architecture (v3) -->
<!-- See also: 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-stoker-agent.md, 08-deployment-operations.md, 09-security-testing-roadmap.md, 10-enterprise-examples.md -->

# Stoker — Architecture Design (v3)

## Vision

A first-class Kubernetes operator, published and maintained by Inductive Automation alongside the `ignition` Helm chart at `charts.ia.io`. It provides declarative, webhook-driven, bi-directional git synchronization for Ignition gateway deployments — replacing the current git-sync sidecar pattern with a purpose-built, production-ready solution.

The operator auto-discovers Ignition gateway pods via annotations, injects sync agent sidecars through a mutating admission webhook, manages one or more git repositories, and reconciles file state across any number of gateways and namespaces. It works on any Kubernetes distribution — EKS, GKE, AKS, on-prem, single-node — with no shared storage requirements.

## Design Principles

This operator is built on core principles that guide all architectural and implementation decisions:

1. **Annotation-Driven Discovery** — Gateways declare intent via Kubernetes annotations. No hardcoded lists or custom CRD resource blocks per gateway.
2. **K8s-Native Patterns** — Uses ConfigMaps for metadata signaling (preferred over trigger files), informers for change detection, conditions for status reporting, and standard K8s conventions for RBAC and ownership.
3. **Ignition Domain Awareness** — Deep understanding of Ignition's architecture: gateway hierarchy, tag inheritance, module systems, scan API semantics, session management, and configuration best practices.
4. **Security by Default** — No plaintext secrets in CRDs, HMAC validation on webhooks, signed container images, air-gap support, and least-privilege access controls.
5. **Cloud-Agnostic** — Works on any Kubernetes distribution without vendor lock-in. No RWX storage requirements — each agent clones independently.

```
charts.ia.io/
├── ignition               # The Ignition gateway Helm chart
├── stoker          # The Stoker chart
└── (future: ignition-stack, etc.)
```

---

## Quick Start

Get a single-gateway sync running in under 5 minutes:

```bash
# 1. Install the operator
helm repo add ia https://charts.ia.io
helm install stoker ia/stoker -n stoker-system --create-namespace

# 2. Create a git auth secret
kubectl create secret generic git-sync-secret -n default \
  --from-file=ssh-privatekey=$HOME/.ssh/id_rsa

# 3. Create an API key secret
kubectl create secret generic ignition-api-key -n default \
  --from-literal=apiKey=YOUR_IGNITION_API_KEY

# 4. Create the Stoker CR (minimal — defaults handle the rest)
cat <<EOF | kubectl apply -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: my-sync
  namespace: default
spec:
  git:
    repo: "git@github.com:myorg/my-ignition-app.git"
    ref: "main"
    auth:
      sshKey:
        secretRef:
          name: git-sync-secret
          key: ssh-privatekey
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

# 5. Add annotation to your Ignition gateway pod (via Helm values)
# gateway:
#   podAnnotations:
#     stoker.io/inject: "true"
#     stoker.io/service-path: "services/gateway"

# 6. Check status
kubectl get stokers
```

That's it. The operator auto-discovers the gateway via annotation, injects the sync agent sidecar, clones the repo, and syncs files. All other fields (`polling`, `webhook`, `excludePatterns`) use sensible defaults.

---

## Problem Statement

The current git-sync approach has fundamental limitations:

1. **Polling-only** — relies on a configurable interval, no event-driven updates
2. **One clone per sidecar** — 5 gateways = 5 identical git clones in the same namespace
3. **One-directional only** — no path for gateway changes to flow back to git
4. **Fighting the tool** — we override the entrypoint, bypass the exec hook model, and only use git-sync for SSH auth
5. **Limited tooling** — the git-sync image only has `cp` and basic coreutils; no rsync, jq, or yq
6. **No observability** — sync status is buried in container logs with no structured reporting
7. **Tightly coupled** — hardcoded `site`/`area*` path mapping, per-project script destinations, provider-specific assumptions

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  Cluster-Scoped                                                              │
│                                                                              │
│  ┌─────────────────────────────────────────────────────┐                     │
│  │  Stoker Controller Manager                   │                     │
│  │  (Deployment, leader-elected, 1 active replica)     │                     │
│  │                                                      │                     │
│  │  Reconciles: Stoker CRs (all namespaces)      │                     │
│  │  Manages: ref resolution, metadata ConfigMaps,      │                     │
│  │           PR creation, status reporting              │                     │
│  └─────────────────────┬───────────────────────────────┘                     │
│                        │                                                     │
│  ┌─────────────────────┴───────────────────────────────┐                     │
│  │  Mutating Admission Webhook                          │                     │
│  │  (separate Deployment, HA, TLS via cert-manager)     │                     │
│  │                                                      │                     │
│  │  Watches: Pod CREATE with annotation                 │                     │
│  │    stoker.io/inject: "true"                   │                     │
│  │  Injects: Sync agent sidecar + volumes               │                     │
│  └──────────────────────────────────────────────────────┘                     │
│                                                                              │
│  ┌──────────────────────────────────────────────────────┐                    │
│  │  Webhook Receiver (Deployment or in-controller)       │                    │
│  │                                                       │                    │
│  │  POST /webhook/{namespace}/{crName}                   │                    │
│  │  Accepts: ArgoCD, Kargo, GitHub, generic              │                    │
│  │  Action: Annotates CR → triggers reconcile            │                    │
│  └──────────────────────────────────────────────────────┘                     │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│  Namespace: site1                                                            │
│                                                                              │
│  ┌───────────────────┐   ┌───────────────────┐   ┌───────────────────┐       │
│  │ Stoker CR   │   │ Metadata ConfigMap │   │ Webhook Secret    │       │
│  │ "proveit-sync"    │   │ stoker-     │   │ (HMAC for auth)   │       │
│  │                   │   │ metadata-proveit-  │   │                   │       │
│  │                   │   │ sync               │   │                   │       │
│  └───────────────────┘   └───────────────────┘   └───────────────────┘       │
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐   │
│  │ StatefulSet: site                                                      │   │
│  │ ┌───────────┐ ┌───────────────────────────────────────┐                │   │
│  │ │ ignition  │ │ stoker-agent (injected sidecar)         │                │   │
│  │ │ container │ │                                        │                │   │
│  │ │           │ │  /repo          — emptyDir (local)    │                │   │
│  │ │           │ │  /ignition-data — shared w/ gateway   │                │   │
│  │ │           │ │  git auth       — projected secret    │                │   │
│  │ │           │ │  agent config   — projected ConfigMap  │                │   │
│  │ │           │ └───────────────────────────────────────┘                │   │
│  │ │  annotations:                                                        │   │
│  │ │  stoker.io/inject: "true"                                     │   │
│  │ │  stoker.io/cr-name: "proveit-sync"                            │   │
│  │ │  stoker.io/service-path: "services/site"                      │   │
│  │ └───────────┘                                                          │   │
│  └───────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  ┌───────────────────────────────────────────────────────────────────────┐   │
│  │ StatefulSet: area1                                                     │   │
│  │ ┌───────────┐ ┌───────────────────────────────────────┐                │   │
│  │ │ ignition  │ │ stoker-agent (injected sidecar)         │                │   │
│  │ │ container │ │                                        │                │   │
│  │ │           │ │  /repo          — emptyDir (local)    │                │   │
│  │ │           │ │  /ignition-data — shared w/ gateway   │                │   │
│  │ └───────────┘ └───────────────────────────────────────┘                │   │
│  └───────────────────────────────────────────────────────────────────────┘   │
│  ... area2, area3, area4                                                     │
│                                                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│  Namespace: site2                                                            │
│  (same pattern — own Stoker CR, own gateway pods with agent sidecars)  │
└──────────────────────────────────────────────────────────────────────────────┘
```

The operator has three logical components:

1. **Controller Manager** — a single cluster-scoped Deployment that reconciles all `Stoker` CRs across namespaces, manages ref resolution via git ls-remote, handles bi-directional PR creation, and reports status.

2. **Mutating Admission Webhook** — a separate high-availability Deployment that intercepts Pod creation and injects sync agent sidecars into annotated Ignition gateway pods. TLS certificates managed by cert-manager.

3. **Sync Agent** — a lightweight sidecar container injected into each Ignition gateway pod. It clones the git repository to a local emptyDir, syncs files to the gateway data volume, and reports status via ConfigMap.

---

## Gateway Discovery & Sidecar Injection

The operator discovers Ignition gateways via pod annotations, following the established pattern used by Istio, Vault Agent, and Linkerd. No gateways are hardcoded in the CRD — the CRD defines *what* to sync (repo, shared resources, service path mappings), and annotations on the pods declare *which* gateways participate.

### Annotations

Applied to Ignition gateway pods (via `podAnnotations` in the ignition Helm chart values):

| Annotation | Required | Description |
|---|---|---|
| `stoker.io/inject` | Yes | `"true"` to enable sidecar injection |
| `stoker.io/cr-name` | No* | Name of the `Stoker` CR in this namespace. *Auto-derived if exactly one CR exists in the namespace. |
| `stoker.io/service-path` | Yes | Repo-relative path to this gateway's service directory |
| `stoker.io/gateway-name` | No | Override gateway identity (defaults to pod label `app.kubernetes.io/name`) |
| `stoker.io/deployment-mode` | No | Config resource overlay to apply (e.g., `prd-cloud`) |
| `stoker.io/tag-provider` | No | UDT tag provider destination (default: `default`) |
| `stoker.io/sync-period` | No | Fallback poll interval in seconds (default: `30`) |
| `stoker.io/exclude-patterns` | No | Comma-separated exclude globs for this gateway |
| `stoker.io/system-name` | No | Override for config normalization systemName |
| `stoker.io/system-name-template` | No | Go template for systemName (default: `{{.GatewayName}}` if omitted) |

### Example: ProveIt Site Chart

```yaml
# values.yaml — site gateway
site:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "proveit-sync"
      stoker.io/service-path: "services/site"
      stoker.io/deployment-mode: "prd-cloud"
      stoker.io/tag-provider: "default"
      stoker.io/system-name-template: "site{{.SiteNumber}}"

# values.yaml — area gateways (all share the same config)
area1:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "proveit-sync"
      stoker.io/service-path: "services/area"
      stoker.io/tag-provider: "edge"
      stoker.io/system-name-template: "site{{.SiteNumber}}-{{.GatewayName}}"
```

### Example: Public Demo Chart

```yaml
# 2-gateway pattern with replicated frontends
frontend:
  gateway:
    replicas: 5
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "publicdemo-sync"
      stoker.io/service-path: "services/ignition-frontend"

backend:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "publicdemo-sync"
      stoker.io/service-path: "services/ignition-backend"
```

### Example: Simple Single-Gateway

```yaml
ignition:
  gateway:
    podAnnotations:
      stoker.io/inject: "true"
      stoker.io/cr-name: "my-sync"
      stoker.io/service-path: "services/gateway"
```

### How Injection Works

The `MutatingWebhookConfiguration` targets Pod CREATE events where `stoker.io/inject: "true"` is present. The webhook:

1. Reads the pod annotations to determine CR name, service path, etc.
2. Looks up the referenced `Stoker` CR in the pod's namespace.
3. **Validates service-path** — checks that `stoker.io/service-path` is a valid relative path (no `..`, no absolute paths). Logs a warning if the path cannot be validated against the repo at injection time (repo may not be cloned yet). Agent validates path existence at sync time.
4. Injects a sidecar container with the sync agent image.
5. Adds volume mounts: emptyDir for repo clone, git auth secret (projected), agent config.
6. Adds the Ignition API key secret volume mount (from the CR spec).
7. Sets environment variables derived from annotations + CR spec.
8. Adds a startup probe so the gateway doesn't start before initial sync completes.

The webhook **does not modify** if the annotation is absent or `"false"`, and it is configured with `failurePolicy: Ignore` so a webhook outage doesn't block unrelated pod creation.

---


---

## Related Documents

- [01-crd.md](01-crd.md) — Custom Resource Definition (spec, status, markers, versioning)
- [02-controller.md](02-controller.md) — Controller Manager, RBAC, reconciliation loop, storage, multi-repo
- [04-sync-profile.md](04-sync-profile.md) — SyncProfile CRD, 3-tier config model, ordered mappings
- [06-stoker-agent.md](06-stoker-agent.md) — Sync agent binary, sync flow, Ignition-aware sync
- [06a-agent-development-plan.md](06a-agent-development-plan.md) — Agent development plan and build phases
- [08-deployment-operations.md](08-deployment-operations.md) — Helm chart, deployment safety, observability, scale
- [09-security-testing-roadmap.md](09-security-testing-roadmap.md) — Security architecture, testing, migration, roadmap
- [10-enterprise-examples.md](10-enterprise-examples.md) — Integration patterns, worked examples, enterprise features
