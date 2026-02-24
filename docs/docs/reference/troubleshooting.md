---
sidebar_position: 4
title: Troubleshooting
description: Common issues, debug commands, and FAQ.
---

# Troubleshooting

## Common issues

### Sidecar not injected

**Symptoms:** Gateway pod has only 1 container, no `stoker-agent` init container.

**Checklist:**

1. **Namespace label** — ensure the namespace has `stoker.io/injection=enabled`:
   ```bash
   kubectl get namespace <ns> --show-labels
   ```
2. **Pod annotation** — ensure the pod has `stoker.io/inject: "true"` (must be a string, not a boolean):
   ```bash
   kubectl get pod <pod> -n <ns> -o jsonpath='{.metadata.annotations}'
   ```
3. **Webhook running** — check the controller pod logs for webhook server startup:
   ```bash
   kubectl logs -n stoker-system deploy/stoker-stoker-operator-controller-manager | grep webhook
   ```
4. **cert-manager certificates** — the webhook requires a valid TLS certificate:
   ```bash
   kubectl get certificate -n stoker-system
   ```
5. **Timing** — the webhook only injects on pod creation. If the pod was created before the operator was installed, delete the pod and let the StatefulSet recreate it.

### Status stuck at Pending

**Symptoms:** `kubectl get gs` shows READY=False, SYNCED=False, but RefResolved is True.

**Checklist:**

1. **Agent logs** — check for errors in the agent sidecar:
   ```bash
   kubectl logs <pod> -n <ns> -c stoker-agent --tail=50
   ```
2. **RBAC** — ensure the agent's service account has the required RoleBinding:
   ```bash
   kubectl get rolebinding -n <ns> | grep stoker
   ```
3. **Secret mounts** — if using private repos, verify the git credentials secret exists:
   ```bash
   kubectl get secret -n <ns>
   ```
4. **API key** — verify the Ignition API key secret exists and is referenced correctly:
   ```bash
   kubectl get secret gw-api-key -n <ns>
   ```

### RefResolved=False

**Symptoms:** `kubectl describe gs <name>` shows `RefResolved=False` with an error message.

**Causes:**

- **Invalid repo URL** — check for typos in `spec.git.repo`
- **Auth failure** — the token/SSH key/GitHub App credentials are wrong or expired
- **Network access** — the controller pod can't reach the git host (check network policies)
- **Ref doesn't exist** — the specified branch or tag doesn't exist in the remote

Check controller logs for the specific error:

```bash
kubectl logs -n stoker-system deploy/stoker-stoker-operator-controller-manager | grep "ls-remote"
```

### AllGatewaysSynced=False

**Symptoms:** Ref is resolved but gateways show sync errors.

**Causes:**

- **Scan API failure** — check gateway port and TLS settings match `spec.gateway.port` and `spec.gateway.tls`
- **API key format** — the Ignition API key must be in `name:secret` format (e.g., `ignition-api-key:CYCSdRg...`), not just the secret portion
- **Gateway not ready** — the Ignition gateway may still be starting up

```bash
kubectl logs <pod> -n <ns> -c stoker-agent | grep -i "scan\|error"
```

### Scan failures (non-200 response)

**Symptoms:** Agent logs show scan errors with non-200 status codes.

| Code | Likely cause |
|------|-------------|
| 401 | API key missing, wrong format, or not loaded by gateway |
| 404 | Wrong gateway port — the API is on a different port than configured |
| 500 | Gateway internal error — check the Ignition gateway logs |
| Connection refused | Wrong port, TLS mismatch, or gateway not yet started |

:::tip API key format
The Ignition REST API uses a custom header format: `X-Ignition-API-Token: name:secret`. Make sure the secret value includes both the token name and the secret, separated by a colon.
:::

### Agent CrashLoopBackOff

**Symptoms:** The `stoker-agent` container repeatedly crashes.

**Checklist:**

1. **Previous logs** — check the last crash output:
   ```bash
   kubectl logs <pod> -n <ns> -c stoker-agent --previous
   ```
2. **Resource limits** — the default agent has no resource limits. If limits are set too low, OOM kills can occur.
3. **Volume mounts** — the agent requires `/ignition-data/` to be mounted from the gateway's data volume.
4. **ConfigMap missing** — if the GatewaySync CR was deleted while the pod is running, the metadata ConfigMap no longer exists.

## Debug commands

Quick reference for common debugging commands:

```bash
# Check GatewaySync CR status
kubectl get gs -n <ns>
kubectl get gs -n <ns> -o wide  # includes LAST SYNC column

# Detailed CR status with conditions
kubectl describe gs <name> -n <ns>

# Agent sidecar logs
kubectl logs <pod> -n <ns> -c stoker-agent --tail=50

# Controller logs
kubectl logs -n stoker-system deploy/stoker-stoker-operator-controller-manager --tail=50

# What the controller sent to the agent
kubectl get cm stoker-metadata-<crName> -n <ns> -o yaml

# What the agent reported back (includes sync status and file change details)
kubectl get cm stoker-status-<crName> -n <ns> -o jsonpath='{.data}' | python3 -m json.tool

# Recent events in the namespace
kubectl get events -n <ns> --sort-by=.lastTimestamp | tail -20

# Check webhook certificate status
kubectl get certificate -n stoker-system

# Verify sidecar injection on a pod
kubectl get pod <pod> -n <ns> -o jsonpath='{.spec.initContainers[*].name}'
```
