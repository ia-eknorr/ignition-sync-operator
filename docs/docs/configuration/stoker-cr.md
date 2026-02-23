---
sidebar_position: 1
title: Stoker CR
description: Full reference for the Stoker custom resource.
---

# Stoker CR Reference

The `Stoker` custom resource defines the git repository to sync from, authentication, polling behavior, gateway connection settings, and agent configuration.

```yaml
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: my-stoker
  namespace: my-namespace
spec:
  git:
    repo: "https://github.com/org/repo.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token
          key: token
  polling:
    enabled: true
    interval: "60s"
  gateway:
    port: 8043
    tls: true
    apiKeySecretRef:
      name: gw-api-key
      key: apiKey
  excludePatterns:
    - "**/.git/"
    - "**/.gitkeep"
    - "**/.resources/**"
  agent:
    image:
      repository: ghcr.io/ia-eknorr/stoker-agent
      tag: latest
      pullPolicy: IfNotPresent
  paused: false
```

## `spec.git`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `repo` | string | Yes | — | Git repository URL (SSH or HTTPS) |
| `ref` | string | Yes | — | Git reference to sync — branch, tag, or commit SHA |
| `auth` | object | No | — | Git authentication configuration |

### `spec.git.auth`

Exactly one authentication method should be specified. Omit entirely for public repositories.

#### Token authentication

```yaml
auth:
  token:
    secretRef:
      name: git-token
      key: token
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `token.secretRef.name` | string | Yes | Name of the Secret |
| `token.secretRef.key` | string | Yes | Key within the Secret |

#### SSH key authentication

```yaml
auth:
  sshKey:
    secretRef:
      name: ssh-key
      key: id_ed25519
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sshKey.secretRef.name` | string | Yes | Name of the Secret containing the SSH private key |
| `sshKey.secretRef.key` | string | Yes | Key within the Secret |

#### GitHub App authentication

```yaml
auth:
  githubApp:
    appId: 12345
    installationId: 67890
    privateKeySecretRef:
      name: github-app-key
      key: private-key.pem
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `githubApp.appId` | integer | Yes | GitHub App ID |
| `githubApp.installationId` | integer | Yes | GitHub App installation ID |
| `githubApp.privateKeySecretRef.name` | string | Yes | Name of the Secret containing the PEM key |
| `githubApp.privateKeySecretRef.key` | string | Yes | Key within the Secret |

## `spec.polling`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Whether periodic polling for git changes is active |
| `interval` | string | No | `"60s"` | Polling period (e.g., `"60s"`, `"5m"`) |

:::tip
If you configure a [webhook receiver](/configuration/helm-values#push-receiver-webhook) for push-event-driven sync, you can set `polling.enabled: false` or increase the interval to reduce API calls.
:::

## `spec.gateway`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `port` | int32 | No | `8043` | Ignition gateway API port |
| `tls` | bool | No | `true` | Enable TLS for gateway API connections |
| `apiKeySecretRef.name` | string | Yes | — | Name of the Secret containing the Ignition API key |
| `apiKeySecretRef.key` | string | Yes | — | Key within the Secret |

## `spec.excludePatterns`

Glob patterns for files to exclude from sync. The pattern `**/.resources/**` is always enforced by the agent even if omitted.

**Default:** `["**/.git/", "**/.gitkeep", "**/.resources/**"]`

## `spec.agent`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `image.repository` | string | No | `ghcr.io/ia-eknorr/stoker-agent` | Agent container image repository |
| `image.tag` | string | No | `latest` | Agent container image tag |
| `image.pullPolicy` | string | No | `IfNotPresent` | Image pull policy |
| `resources` | object | No | — | Agent container resource requirements |

## `spec.paused`

When set to `true`, halts all sync operations. The controller continues to reconcile and resolve refs, but agents will not perform syncs.

## Pod Annotations

Gateways are discovered by pod annotations. These are typically set via `podAnnotations` in the Ignition Helm chart values:

| Annotation | Required | Description |
|---|---|---|
| `stoker.io/inject` | Yes | Set to `"true"` to trigger sidecar injection |
| `stoker.io/cr-name` | Yes | Name of the Stoker CR to sync from |
| `stoker.io/sync-profile` | Yes | Name of the SyncProfile to use |
| `stoker.io/gateway-name` | No | Override gateway identity (defaults to pod label `app.kubernetes.io/name`) |

## Status

The Stoker CR status is managed by the controller and reports:

- **`lastSyncRef`** / **`lastSyncCommit`** — the last resolved git reference and commit SHA
- **`refResolutionStatus`** — `NotResolved`, `Resolving`, `Resolved`, or `Error`
- **`discoveredGateways`** — list of gateway pods with per-gateway sync status, commit, projects synced
- **`conditions`** — standard Kubernetes conditions including `RefResolved`, `AllGatewaysSynced`, and `Ready`
