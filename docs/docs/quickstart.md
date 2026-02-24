---
sidebar_position: 1
title: Quickstart
description: Get a single Ignition gateway syncing projects from Git in 7 steps.
---

# Quickstart

Get a single Ignition gateway syncing projects from Git in 7 steps.

## Prerequisites

- Kubernetes cluster (v1.28+)
- `kubectl` and `helm` CLI tools

:::tip Need a cluster?
Install [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation), then:

```bash
kind create cluster --name stoker-quickstart
kubectl cluster-info
```
:::

## 1. Install cert-manager

Stoker uses cert-manager for webhook TLS certificates:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=120s
```

## 2. Install the Stoker operator

```bash
helm install stoker oci://ghcr.io/ia-eknorr/charts/stoker-operator \
  -n stoker-system --create-namespace
```

Verify the controller is running:

```bash
kubectl get pods -n stoker-system
```

You should see a `controller-manager` pod in `Running` state.

## 3. Prepare a namespace

Create a namespace and label it for sidecar injection:

```bash
kubectl create namespace quickstart
kubectl label namespace quickstart stoker.io/injection=enabled
```

The `stoker.io/injection=enabled` label tells the mutating webhook to watch for annotated pods in this namespace.

## 4. Create secrets

The example repository includes a pre-configured API token resource. Create a matching secret so the agent can authenticate with the gateway's scan API:

```bash
kubectl create secret generic gw-api-key -n quickstart \
  --from-literal=apiKey="ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY"
```

:::note
This API key belongs to the public example repository and carries no security risk. The example repository is provided solely for this quickstart — do not use it as a base template for production projects. In your own deployments, generate unique API tokens for each gateway.
:::

No git credentials are needed since we're using a public repository.

## 5. Create a GatewaySync CR

The GatewaySync CR defines the git repository and sync profiles. We set `gateway.port` and `gateway.tls` to match the default Ignition Helm chart (HTTP on 8088):

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: stoker.io/v1alpha1
kind: GatewaySync
metadata:
  name: quickstart
  namespace: quickstart
spec:
  git:
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
  gateway:
    port: 8088
    tls: false
    apiKeySecretRef:
      name: gw-api-key
      key: apiKey
  sync:
    profiles:
      standard:
        mappings:
          - source: "services/ignition-blue/projects/"
            destination: "projects/"
            type: dir
            required: true
          - source: "services/ignition-blue/config/"
            destination: "config/"
            type: dir
        syncPeriod: 30
EOF
```

Verify the controller resolved the git ref:

```bash
kubectl get gatewaysyncs -n quickstart
```

The `REF` column should show `main` and `COMMIT` should show a short hash. `READY` will be `False` until a gateway is deployed and synced.

## 6. Grant agent RBAC

The agent sidecar needs permission to read GatewaySync CRs and write status ConfigMaps. The Helm chart installs a ClusterRole for this — bind it to the gateway's service account:

```bash
kubectl create rolebinding stoker-agent -n quickstart \
  --clusterrole=stoker-stoker-operator-agent \
  --serviceaccount=quickstart:ignition
```

:::note
The service account name (`ignition`) matches the default created by the Ignition Helm chart. If your gateway uses a different service account, substitute it here.
:::

## 7. Deploy an Ignition gateway

Install using the [official Ignition Helm chart](https://charts.ia.io) with Stoker annotations.

```bash
helm repo add inductiveautomation https://charts.ia.io
helm repo update
```

Create a values file that enables auto-commissioning and adds the Stoker sidecar injection annotations:

```yaml title="ignition-values.yaml"
commissioning:
  edition: standard
  acceptIgnitionEULA: true

gateway:
  preconfigure:
    additionalCmds:
      - |
        [ -f "/data/commissioning.json" ] || echo "{}" > /data/commissioning.json

podAnnotations:
  stoker.io/inject: "true"
  stoker.io/cr-name: quickstart
  stoker.io/profile: standard
```

```bash
helm upgrade --install ignition inductiveautomation/ignition \
  -n quickstart -f ignition-values.yaml
```

The key annotations:

| Annotation | Value | Purpose |
|---|---|---|
| `stoker.io/inject` | `"true"` | Triggers sidecar injection |
| `stoker.io/cr-name` | `"quickstart"` | Links to the GatewaySync CR |
| `stoker.io/profile` | `"standard"` | Selects the sync profile from `spec.sync.profiles` |

:::tip Why install the gateway last?
The Stoker webhook injects the agent sidecar when a pod is created. By installing the operator and CRs first, the webhook is ready to inject on the gateway's first pod creation — no restart needed.
:::

Wait for the gateway to start:

```bash
kubectl get pods -n quickstart -w
```

You should see the Ignition pod with **2/2** containers ready (the gateway + the `stoker-agent` sidecar).

## Verify the deployment

Once the gateway pod shows **2/2**, walk through these checks to confirm everything is wired up correctly.

### Confirm sidecar injection

Verify the pod has both containers — the gateway and the injected `stoker-agent` sidecar:

```bash
kubectl get pod -n quickstart -o 'custom-columns=NAME:.metadata.name,SIDECARS:.spec.initContainers[*].name,STATUS:.status.phase'
```

You should see `stoker-agent` listed as an init container (native sidecar).

### Check events

Look at the namespace events to see the injection and sync activity:

```bash
kubectl get events -n quickstart --sort-by=.lastTimestamp | tail -15
```

### Check the GatewaySync CR status

```bash
kubectl get gs -n quickstart
```

After about 60 seconds you should see:

```text
NAME         REF    COMMIT    PROFILES   SYNCED   GATEWAYS             READY   AGE
quickstart   main   4d19160   1          True     1/1 gateways synced  True    5m
```

### Describe the GatewaySync CR

For detailed status including conditions and discovered gateways:

```bash
kubectl describe gatewaysync quickstart -n quickstart
```

Look for:

- **Conditions:** `RefResolved=True`, `AllGatewaysSynced=True`, and `Ready=True`
- **Discovered Gateways:** should list the gateway pod with its sync status and commit hash

### Read the agent logs

```bash
kubectl logs -n quickstart -l app.kubernetes.io/name=ignition -c stoker-agent --tail=20
```

Look for:

- `clone complete` — the repo was cloned successfully
- `files synced` with `added` and `projects` — files were delivered to the gateway
- `scan complete` with `projects=200 config=200` — Ignition acknowledged the sync

### Inspect the status ConfigMap

The agent writes detailed sync status to a ConfigMap:

```bash
kubectl get cm stoker-status-quickstart -n quickstart -o jsonpath='{.data}' | python3 -m json.tool
```

This shows the synced commit, file counts, project names, and any error messages per gateway.

## Explore

Open the Ignition web UI to see the synced projects:

```bash
kubectl port-forward -n quickstart svc/ignition 8088:8088
```

Navigate to `http://localhost:8088` in your browser. After completing the initial commissioning wizard, you should see the project from the example repository.

Try changing the git ref to a specific tag:

```bash
kubectl patch gatewaysync quickstart -n quickstart --type=merge \
  -p '{"spec":{"git":{"ref":"v0.1.0"}}}'
```

Watch the agent pick up the change:

```bash
kubectl get gs -n quickstart -w
```

## Cleanup

```bash
helm uninstall ignition -n quickstart
kubectl delete namespace quickstart
helm uninstall stoker -n stoker-system
kubectl delete namespace stoker-system
```

If you created a kind cluster:

```bash
kind delete cluster --name stoker-quickstart
```

## Next steps

- **Multiple gateways:** Instead of hardcoding paths per gateway, use `{{.GatewayName}}` or `{{.Labels.key}}` in your profile source paths. For example, add a `site` label to each pod and use `source: "services/{{.Labels.site}}/projects/"` — one profile then serves any number of gateways, each syncing from its own directory.
- **Webhook-driven sync:** Configure `POST /webhook/{namespace}/{crName}` to trigger syncs on git push events instead of polling.
- **Private repos:** Add `spec.git.auth` with a token or SSH key secret reference to sync from private repositories.
- **[GatewaySync CR Reference](./configuration/gatewaysync-cr.md)** — full spec reference including git auth, polling, sync profiles, and agent configuration
- **[Helm Values](./configuration/helm-values.md)** — all configurable values for the operator chart
