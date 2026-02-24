---
sidebar_position: 2
title: Installation
description: Install the Stoker operator on your Kubernetes cluster.
---

# Installation

## Prerequisites

- Kubernetes >= 1.28
- [cert-manager](https://cert-manager.io/) (for webhook TLS)
- Helm 3

## Install cert-manager

Stoker's mutating webhook requires TLS certificates managed by cert-manager:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=120s
```

## Install the operator

```bash
helm install stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator \
  -n stoker-system --create-namespace
```

Verify:

```bash
kubectl get pods -n stoker-system
```

You should see a `controller-manager` pod in `Running` state.

## Enable sidecar injection

Label any namespace where you want the webhook to inject agent sidecars:

```bash
kubectl label namespace <your-namespace> stoker.io/injection=enabled
```

## Grant agent RBAC

The Helm chart installs a `ClusterRole` for the agent. Bind it to the service account used by your gateway pods:

```bash
kubectl create rolebinding stoker-agent -n <your-namespace> \
  --clusterrole=stoker-stoker-operator-agent \
  --serviceaccount=<your-namespace>:<service-account>
```

:::tip
The default service account name for the [Ignition Helm chart](https://charts.ia.io) is `ignition`.
:::

## Upgrading

```bash
helm upgrade stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator \
  -n stoker-system
```

CRDs are updated automatically when included in the chart's `crds/` directory.

## Uninstalling

```bash
helm uninstall stoker -n stoker-system
kubectl delete namespace stoker-system
```

:::caution
Uninstalling the operator removes the mutating webhook. Existing agent sidecars will continue running but won't receive new metadata ConfigMap updates.
:::

## Configuration

See [Helm Values](./reference/helm-values.md) for all configurable chart values.
