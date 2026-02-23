---
sidebar_position: 2
title: SyncProfile
description: Full reference for the SyncProfile custom resource.
---

# SyncProfile Reference

The `SyncProfile` custom resource defines file mappings, deployment mode overlays, exclude patterns, and sync behavior for gateways.

```yaml
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: production
  namespace: my-namespace
spec:
  mappings:
    - source: "services/{{.GatewayName}}/projects/"
      destination: "projects/"
      type: dir
      required: true
    - source: "services/{{.GatewayName}}/config/"
      destination: "config/"
      type: dir
  deploymentMode:
    name: production
    source: "overlays/production/"
  syncPeriod: 30
  designerSessionPolicy: wait
```

## `spec.mappings`

An ordered list of source-to-destination file mappings. Applied top to bottom; later mappings overlay earlier ones.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `source` | string | Yes | — | Repo-relative path to copy from |
| `destination` | string | Yes | — | Gateway-relative path to copy to |
| `type` | string | No | `"dir"` | Entry type — `"dir"` or `"file"` |
| `required` | bool | No | `false` | Fail sync if the source path doesn't exist |

### Template variables

Both `source` and `destination` support Go template variables:

| Variable | Description |
|----------|-------------|
| `{{.GatewayName}}` | Gateway identity from the `stoker.io/gateway-name` annotation (or `app.kubernetes.io/name` label) |
| `{{.Vars.key}}` | Custom variable from `spec.vars` |

Using `{{.GatewayName}}` in source paths lets a single SyncProfile serve multiple gateways, each syncing from its own directory in the repo.

## `spec.deploymentMode`

Configures an Ignition deployment mode overlay. The overlay directory is merged on top of `config/` after all mappings are applied.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Mode name (informational, shown in status) |
| `source` | string | Yes | Repo-relative overlay directory |

## `spec.excludePatterns`

Glob patterns for files to exclude from sync. These are merged with the Stoker CR's global `excludePatterns` (additive).

## `spec.dependsOn`

Declares dependencies on other SyncProfiles for sync ordering. This profile's gateways will not sync until the named profile's gateways all report the specified condition.

```yaml
dependsOn:
  - profileName: infrastructure
    condition: Synced
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `profileName` | string | Yes | — | Name of the SyncProfile dependency (same namespace) |
| `condition` | string | No | `"Synced"` | Status condition that must be true |

:::note
Only single-level dependencies are supported — no transitive dependency chains.
:::

## `spec.vars`

A map of template variables resolved by the agent at sync time. Available in `source` and `destination` paths as `{{.Vars.key}}`.

```yaml
vars:
  region: us-east
  environment: prod
```

## `spec.syncPeriod`

Agent-side polling interval in seconds. The agent checks for new metadata (new commits) at this frequency.

| Constraint | Value |
|------------|-------|
| Default | `30` |
| Minimum | `5` |
| Maximum | `3600` |

## `spec.dryRun`

When `true`, the agent syncs to a staging directory without copying files to `/ignition-data/`. The diff report is written to the status ConfigMap for inspection.

## `spec.designerSessionPolicy`

Controls sync behavior when Ignition Designer sessions are active on the gateway.

| Value | Behavior |
|-------|----------|
| `proceed` (default) | Logs a warning and continues the sync |
| `wait` | Retries until sessions close (up to 5 minutes) |
| `fail` | Aborts the sync |

## `spec.paused`

When `true`, halts sync for all gateways referencing this profile.

## Status

The SyncProfile status reports:

- **`gatewayCount`** — number of gateway pods referencing this profile
- **`conditions`** — standard Kubernetes conditions including `Accepted`
