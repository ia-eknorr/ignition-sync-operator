# stoker-operator

![Version: 0.3.0](https://img.shields.io/badge/Version-0.3.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.3.0](https://img.shields.io/badge/AppVersion-0.3.0-informational?style=flat-square)

Kubernetes operator that syncs Ignition gateway projects from a Git repository

**Homepage:** <https://github.com/ia-eknorr/stoker-operator>

## Prerequisites

- Kubernetes >= 1.28
- Helm 3
- [cert-manager](https://cert-manager.io/) (when `certManager.enabled=true`)

## Installation

```bash
helm install stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator
```

See the post-install notes (`helm get notes <release>`) for next steps: creating
secrets and applying CRs. Agent RBAC is managed automatically by default.

## Architecture

The operator has two components:

- **Controller** — watches GatewaySync CRs, resolves git refs via `ls-remote`,
  and manages metadata ConfigMaps.
- **Agent sidecar** — injected into gateway pods via MutatingWebhook, clones the
  repo and syncs files to the Ignition data directory.

Two webhook-like features exist and are configured separately:

| Feature | Values key | Description |
|---------|------------|-------------|
| Sidecar injection | `webhook.*` | MutatingWebhook that injects the stoker-agent into annotated pods |
| Push receiver | `webhookReceiver.*` | HTTP endpoint that accepts GitHub/GitLab push events for immediate sync |

## Requirements

Kubernetes: `>= 1.28.0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for scheduling the controller pod. |
| agentImage | object | `{"repository":"ghcr.io/ia-eknorr/stoker-agent","tag":""}` | Agent sidecar image injected into gateway pods by the webhook. |
| agentImage.repository | string | `"ghcr.io/ia-eknorr/stoker-agent"` | Image repository for the sync agent sidecar. |
| agentImage.tag | string | `""` | Image tag. Defaults to the chart's appVersion if empty. |
| certManager | object | `{"enabled":true}` | cert-manager integration for webhook TLS certificates. Requires cert-manager to be installed in the cluster. |
| certManager.enabled | bool | `true` | Create a self-signed Issuer and Certificate for webhook TLS. Requires cert-manager to be installed in the cluster. |
| fullnameOverride | string | `""` | Override the full release name used in resource names. |
| image | object | `{"pullPolicy":"IfNotPresent","repository":"ghcr.io/ia-eknorr/stoker-operator","tag":""}` | Controller container image configuration. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy (Always, IfNotPresent, Never). |
| image.repository | string | `"ghcr.io/ia-eknorr/stoker-operator"` | Image repository for the controller manager. |
| image.tag | string | `""` | Image tag. Defaults to the chart's appVersion if empty. |
| imagePullSecrets | list | `[]` | Credentials for private container registries. Example:   imagePullSecrets:     - name: my-registry-secret |
| leaderElection | object | `{"enabled":true}` | Leader election prevents multiple controller instances from reconciling simultaneously. Disable only for single-replica development setups. |
| leaderElection.enabled | bool | `true` | Enable leader election for controller manager. |
| metrics | object | `{"enabled":true,"service":{"port":8443,"type":"ClusterIP"}}` | Metrics endpoint configuration. The controller exposes Prometheus metrics over HTTPS on the metrics service port. |
| metrics.enabled | bool | `true` | Enable the metrics Service. |
| metrics.service.port | int | `8443` | Port the metrics service listens on. |
| metrics.service.type | string | `"ClusterIP"` | Service type for the metrics endpoint. |
| nameOverride | string | `""` | Override the chart name used in resource names. |
| networkPolicy | object | `{"enabled":false}` | NetworkPolicy restricts ingress to the metrics port. Only allows traffic from namespaces labeled `metrics: enabled`. |
| networkPolicy.enabled | bool | `false` | Create a NetworkPolicy for the controller. |
| nodeSelector | object | `{}` | Node selector labels for scheduling the controller pod. Example:   nodeSelector:     kubernetes.io/os: linux |
| rbac | object | `{"autoBindAgent":{"enabled":true}}` | RBAC configuration for the agent sidecar. |
| rbac.autoBindAgent.enabled | bool | `true` | Automatically create RoleBindings for the agent sidecar in namespaces where GatewaySync CRs exist. The controller discovers ServiceAccounts from gateway pods and binds only those SAs to the stoker-agent ClusterRole. Disable for environments that manage RBAC externally (e.g., GitOps-managed RBAC). |
| replicaCount | int | `1` | Number of controller replicas. Only one replica holds the leader lock at a time; additional replicas provide fast failover. |
| resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | CPU and memory resource requests/limits for the controller container. The controller runs git ls-remote (no clone) and watches CRs, so resource requirements are modest. |
| serviceMonitor | object | `{"enabled":false}` | Prometheus ServiceMonitor for automatic scrape target discovery. Requires the prometheus-operator CRDs to be installed in the cluster. |
| serviceMonitor.enabled | bool | `false` | Create a ServiceMonitor resource. |
| tolerations | list | `[]` | Tolerations for scheduling the controller pod on tainted nodes. |
| webhook | object | `{"enabled":true,"namespaceSelector":{"requireLabel":false},"port":9443}` | Mutating webhook for sidecar injection. When enabled, pods with annotation `stoker.io/inject: "true"` get the stoker-agent sidecar injected automatically. By default, injection works in all namespaces except kube-system and kube-node-lease. |
| webhook.enabled | bool | `true` | Enable the MutatingWebhookConfiguration and webhook Service. |
| webhook.namespaceSelector.requireLabel | bool | `false` | Require the stoker.io/injection=enabled label on namespaces for sidecar injection. When false (default), the webhook intercepts pod creates in all namespaces except kube-system and kube-node-lease. Enable for regulated environments that require explicit namespace opt-in. |
| webhook.port | int | `9443` | Webhook server port on the controller container. |
| webhookReceiver | object | `{"enabled":false,"hmac":{"secret":"","secretRef":{"key":"webhook-secret","name":""}},"port":9444}` | Git webhook receiver for push-event-driven sync. Disabled by default — enable when you want push-event-driven syncs. When disabled, the controller does not start the HTTP receiver server. When enabled without HMAC, any network client that can reach the Service can trigger a reconcile. Configure hmac for production use. |
| webhookReceiver.enabled | bool | `false` | Enable the webhook receiver HTTP server and its Service. |
| webhookReceiver.hmac | object | `{"secret":"","secretRef":{"key":"webhook-secret","name":""}}` | HMAC secret for validating webhook signatures (X-Hub-Signature-256). Provide either a literal value or a reference to an existing Secret. |
| webhookReceiver.hmac.secret | string | `""` | HMAC secret value. Ignored if secretRef is set. |
| webhookReceiver.hmac.secretRef | object | `{"key":"webhook-secret","name":""}` | Reference to an existing Secret containing the HMAC key. |
| webhookReceiver.hmac.secretRef.key | string | `"webhook-secret"` | Key within the Secret. |
| webhookReceiver.hmac.secretRef.name | string | `""` | Name of the Secret. |
| webhookReceiver.port | int | `9444` | Port for the inbound git webhook receiver. |

