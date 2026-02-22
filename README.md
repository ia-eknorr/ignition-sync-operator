<p align="center">
  <img src="assets/logo.png" alt="Stoker logo" width="180" />
</p>

# Stoker

> **stok·er** /ˈstōkər/ — *a person who tends the fire in a furnace, feeding it fuel to keep it burning.*

Stoker tends your Ignition gateways, continuously feeding them configuration from Git to keep them running in the desired state.

## Features

- **Git-driven configuration sync** — gateway projects, tags, and resources managed in Git
- **No shared storage** — controller resolves refs via `ls-remote`; agent clones independently to a local emptyDir
- **SyncProfile mappings** — declarative source-to-destination file mappings with glob patterns and template variables
- **Automatic sidecar injection** — MutatingWebhook injects the sync agent into annotated pods
- **Gateway discovery** — controller discovers annotated pods and aggregates sync status
- **Webhook receiver** — push-event-driven sync via `POST /webhook/{namespace}/{crName}`
- **Deployment mode overlays** — per-profile overlays applied after mappings

## Prerequisites

- Kubernetes >= 1.28
- [cert-manager](https://cert-manager.io/) (for webhook TLS)
- Helm 3

## Quick Start

```bash
# Install the operator
helm install stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator

# Create a git auth secret
kubectl create secret generic git-creds --from-literal=token=ghp_...

# Create a gateway API key secret
kubectl create secret generic gw-api-key --from-literal=apiKey=my-key:my-secret

# Apply a Stoker CR
kubectl apply -f config/samples/stoker_v1alpha1_stoker.yaml

# Apply a SyncProfile
kubectl apply -f config/samples/stoker_v1alpha1_syncprofile.yaml

# Label the namespace for sidecar injection
kubectl label namespace default stoker.io/injection=enabled

# Grant the agent RBAC in your namespace
kubectl create rolebinding stoker-agent \
  --clusterrole=stoker-agent \
  --serviceaccount=default:default
```

## Architecture

```text
┌──────────────────────────────────────────────────────┐
│  Controller (Deployment)                              │
│  • Resolves git refs via ls-remote (no clone)        │
│  • Writes metadata ConfigMap (commit, ref, gitURL)   │
│  • Discovers annotated gateway pods                  │
│  • Aggregates sync status from agent ConfigMaps      │
│  • Injects agent sidecar via MutatingWebhook         │
└──────────────────────┬───────────────────────────────┘
                       │ ConfigMaps
                       ▼
┌──────────────────────────────────────────────────────┐
│  Agent Sidecar (per gateway pod)                      │
│  • Reads metadata ConfigMap for commit + ref          │
│  • Clones repo to local emptyDir /repo               │
│  • Applies SyncProfile mappings to /ignition-data    │
│  • Reports status via status ConfigMap               │
└──────────────────────────────────────────────────────┘
```

## CRDs

| CRD | Description |
| --- | --- |
| `Stoker` | Defines the git repository, auth, polling, and gateway connection settings |
| `SyncProfile` | Defines file mappings, deployment mode overlays, exclude patterns, and template variables |

## Development

```bash
# Build controller and agent binaries
make build

# Run tests
make test

# Run the controller locally (requires kubeconfig)
make run

# Generate CRDs and RBAC
make manifests

# Sync CRDs to Helm chart
make helm-sync

# Run functional tests in kind
make functional-test

# Lint
make lint
```

## License

This project is licensed under the [MIT License](LICENSE).
