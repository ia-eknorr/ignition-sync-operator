---
sidebar_position: 2
slug: /reference/helm-values
title: Helm Values
description: All configurable values for the Stoker operator Helm chart.
---

# Helm Values Reference

The Stoker operator is installed via Helm:

```bash
helm install stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator \
  -n stoker-system --create-namespace
```

## All Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `replicaCount` | int | `1` | Number of controller replicas. Only one replica holds the leader lock at a time; additional replicas provide fast failover. |
| `image.repository` | string | `ghcr.io/ia-eknorr/stoker-operator` | Image repository for the controller manager. |
| `image.tag` | string | `""` | Image tag. Defaults to the chart's appVersion if empty. |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | list | `[]` | Credentials for private container registries. |
| `nameOverride` | string | `""` | Override the chart name used in resource names. |
| `fullnameOverride` | string | `""` | Override the full release name. |
| `agentImage.repository` | string | `ghcr.io/ia-eknorr/stoker-agent` | Image repository for the sync agent sidecar. |
| `agentImage.tag` | string | `""` | Agent image tag. Defaults to the chart's appVersion if empty. |
| `leaderElection.enabled` | bool | `true` | Enable leader election. Disable only for single-replica dev setups. |
| `resources.requests.cpu` | string | `10m` | Controller CPU request. |
| `resources.requests.memory` | string | `64Mi` | Controller memory request. |
| `resources.limits.cpu` | string | `500m` | Controller CPU limit. |
| `resources.limits.memory` | string | `128Mi` | Controller memory limit. |
| `nodeSelector` | object | `{}` | Node selector labels for the controller pod. |
| `tolerations` | list | `[]` | Tolerations for scheduling on tainted nodes. |
| `affinity` | object | `{}` | Affinity rules for the controller pod. |

### cert-manager

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `certManager.enabled` | bool | `true` | Create a self-signed Issuer and Certificate for webhook TLS. Requires cert-manager. |

### Metrics

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `metrics.enabled` | bool | `true` | Enable the metrics Service. |
| `metrics.service.type` | string | `ClusterIP` | Service type for the metrics endpoint. |
| `metrics.service.port` | int | `8443` | Port the metrics service listens on. |
| `serviceMonitor.enabled` | bool | `false` | Create a Prometheus ServiceMonitor. Requires prometheus-operator CRDs. |
| `networkPolicy.enabled` | bool | `false` | Create a NetworkPolicy restricting ingress to the metrics port. |

### Sidecar Injection Webhook

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `webhook.enabled` | bool | `true` | Enable the MutatingWebhookConfiguration. |
| `webhook.port` | int | `9443` | Webhook server port on the controller container. |
| `webhook.namespaceSelector.requireLabel` | bool | `false` | Require `stoker.io/injection=enabled` label on namespaces for injection. When false, injection works in all namespaces except `kube-system` and `kube-node-lease`. |

The webhook injects the agent sidecar into pods with annotation `stoker.io/inject: "true"`. By default, injection works in all namespaces except `kube-system` and `kube-node-lease`. Set `webhook.namespaceSelector.requireLabel=true` to require the `stoker.io/injection=enabled` namespace label.

### Agent RBAC

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `rbac.autoBindAgent.enabled` | bool | `true` | Automatically create RoleBindings for the agent sidecar in namespaces where GatewaySync CRs exist. The controller discovers ServiceAccounts from gateway pods and binds them to the `stoker-agent` ClusterRole. Disable for environments that manage RBAC externally. |

### Push Receiver (Webhook)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `webhookReceiver.port` | int | `9444` | Port for the inbound git webhook receiver. Set to `0` to disable. |
| `webhookReceiver.hmac.secret` | string | `""` | HMAC secret value for signature validation. Ignored if `secretRef` is set. |
| `webhookReceiver.hmac.secretRef.name` | string | `""` | Name of an existing Secret containing the HMAC key. |
| `webhookReceiver.hmac.secretRef.key` | string | `webhook-secret` | Key within the Secret. |

The push receiver accepts `POST /webhook/{namespace}/{crName}` and auto-detects payload format from GitHub releases, ArgoCD notifications, Kargo promotions, or generic `{"ref": "..."}` bodies. HMAC validation uses the `X-Hub-Signature-256` header.
