# Quickstart

Get a single Ignition gateway syncing projects from Git in under 15 minutes.

This guide walks through a complete end-to-end setup: installing the operator, deploying an Ignition gateway, and configuring Stoker to sync project files from a Git repository.

## Prerequisites

- Kubernetes cluster (v1.28+)
- `kubectl` and `helm` CLI tools

<details>
<summary>Need a cluster? Create one with kind</summary>

```bash
kind create cluster --name stoker-quickstart
kubectl cluster-info
```
</details>

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

The gateway API key secret is required by the Stoker CR. For this quickstart we'll use a placeholder since file sync works without a real Ignition API key:

```bash
kubectl create secret generic gw-api-key -n quickstart \
  --from-literal=apiKey=placeholder:placeholder
```

> **Note:** A real API key (created through the Ignition web UI) is needed for the agent to trigger project scans after syncing. File delivery works without it.

No git credentials are needed for this quickstart since we're using a public repository.

## 5. Create a Stoker CR

The Stoker CR defines the git repository to sync from:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: quickstart
  namespace: quickstart
spec:
  git:
    repo: "https://github.com/ia-eknorr/test-ignition-project.git"
    ref: "main"
  gateway:
    apiKeySecretRef:
      name: gw-api-key
      key: apiKey
EOF
```

Verify the controller resolved the git ref:

```bash
kubectl get stokers -n quickstart
```

The `REF` column should show `main` and `READY` should be `True`.

## 6. Create a SyncProfile

The SyncProfile defines which files to sync and where to put them. The example repository ([ia-eknorr/test-ignition-project](https://github.com/ia-eknorr/test-ignition-project)) has per-gateway directories under `services/`, so we point the mappings at `services/ignition-blue/`:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: standard
  namespace: quickstart
spec:
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

Verify:

```bash
kubectl get syncprofiles -n quickstart
```

The `ACCEPTED` column should show `True`.

## 7. Deploy an Ignition gateway

Install using the [official Ignition Helm chart](https://charts.ia.io) with Stoker annotations:

```bash
helm repo add inductiveautomation https://charts.ia.io
helm repo update
```

```bash
helm install ignition inductiveautomation/ignition \
  -n quickstart \
  --set commissioning.edition=standard \
  --set commissioning.acceptIgnitionEULA=true \
  --set podAnnotations."stoker\.io/inject"="true" \
  --set podAnnotations."stoker\.io/cr-name"="quickstart" \
  --set podAnnotations."stoker\.io/sync-profile"="standard"
```

The key annotations:

| Annotation | Value | Purpose |
|---|---|---|
| `stoker.io/inject` | `"true"` | Triggers sidecar injection |
| `stoker.io/cr-name` | `"quickstart"` | Links to the Stoker CR |
| `stoker.io/sync-profile` | `"standard"` | Links to the SyncProfile |

> **Why install the gateway last?** The Stoker webhook injects the agent sidecar when a pod is created. By installing the operator and CRs first, the webhook is ready to inject on the gateway's first pod creation -- no restart needed.

Wait for the gateway to start:

```bash
kubectl get pods -n quickstart -w
```

You should see the Ignition pod with **2/2** containers ready (the gateway + the `stoker-agent` sidecar).

## 8. Verify sync

Check the Stoker CR status:

```bash
kubectl get stokers -n quickstart
```

The `GATEWAYS` column should show the discovered gateway and `SYNCED` should be `True`.

Check the agent logs to see what was synced:

```bash
kubectl logs -n quickstart -l app.kubernetes.io/name=ignition -c stoker-agent --tail=20
```

You can also inspect the status ConfigMap for detailed sync information:

```bash
kubectl get cm -n quickstart -l stoker.io/cr-name=quickstart
```

## 9. Explore

Open the Ignition web UI to see the synced projects:

```bash
kubectl port-forward -n quickstart svc/ignition 8088:8088
```

Navigate to `http://localhost:8088` in your browser. After completing the initial commissioning wizard, you should see the project from the example repository.

Try changing the git ref to a specific tag:

```bash
kubectl patch stoker quickstart -n quickstart --type=merge \
  -p '{"spec":{"git":{"ref":"v0.1.0"}}}'
```

Watch the agent pick up the change:

```bash
kubectl get stokers -n quickstart -w
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

- **Multiple gateways:** Instead of hardcoding paths per gateway, use `{{.GatewayName}}` in your SyncProfile source paths and the `stoker.io/gateway-name` annotation on each pod. One SyncProfile then serves any number of gateways.
- **Deployment mode overlays:** Use `spec.deploymentMode` in your SyncProfile to apply environment-specific config.
- **Webhook-driven sync:** Configure `POST /webhook/{namespace}/{crName}` to trigger syncs on git push events instead of polling.
- **Real API key:** Create an API key through the Ignition web UI and update the `gw-api-key` secret to enable project scan triggers after sync.

See the [architecture docs](architecture/) for deeper technical detail.
