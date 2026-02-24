---
sidebar_position: 3
title: Annotations & Labels
description: Complete reference for all Stoker annotations and labels.
---

# Annotations & Labels Reference

Stoker uses annotations and labels to control sidecar injection, gateway discovery, and sync behavior. This page documents every annotation and label recognized by the system.

## Pod annotations (set by users)

These annotations are set on gateway pods, typically via `podAnnotations` in the Ignition Helm chart.

| Annotation | Value | Required | Description |
|------------|-------|----------|-------------|
| `stoker.io/inject` | `"true"` | Yes | Triggers sidecar injection by the mutating webhook |
| `stoker.io/cr-name` | string | No | Name of the GatewaySync CR to sync from. Auto-derived if exactly one CR exists in the namespace. |
| `stoker.io/profile` | string | Yes | Sync profile name from `spec.sync.profiles`. If unset, the `default` profile is used. |
| `stoker.io/gateway-name` | string | No | Override gateway identity. Defaults to the pod's `app.kubernetes.io/name` label. |
| `stoker.io/agent-image` | `"repo:tag"` | No | Override the agent sidecar image for this pod. For debugging use. |

**Example:**

```yaml
podAnnotations:
  stoker.io/inject: "true"
  stoker.io/cr-name: my-sync
  stoker.io/profile: standard
```

:::tip
Use `--set-string` (not `--set`) when passing annotation values through Helm to avoid boolean coercion (e.g., `"true"` becoming `true`).
:::

## Namespace labels

| Label | Value | Description |
|-------|-------|-------------|
| `stoker.io/injection` | `enabled` | Required on any namespace where the webhook should inject agent sidecars |

```bash
kubectl label namespace my-namespace stoker.io/injection=enabled
```

The mutating webhook uses a `namespaceSelector` that matches this label. Pods in unlabeled namespaces are never intercepted.

## CR annotations (set by webhook receiver)

These annotations are set automatically on GatewaySync CRs by the webhook receiver. Users should not set them manually.

| Annotation | Value | Description |
|------------|-------|-------------|
| `stoker.io/requested-ref` | string | Git ref requested by the last webhook payload |
| `stoker.io/requested-at` | RFC 3339 timestamp | When the webhook request was received |
| `stoker.io/requested-by` | `"github"`, `"argocd"`, `"kargo"`, or `"generic"` | Source format detected from the payload |

These annotations trigger an immediate reconciliation via the controller's predicate filter.

## Internal annotations (set by webhook)

| Annotation | Value | Description |
|------------|-------|-------------|
| `stoker.io/injected` | `"true"` | Set by the mutating webhook after successful sidecar injection. Used for tracking — do not set manually. |

## Labels on owned resources

| Label | Value | Set on | Description |
|-------|-------|--------|-------------|
| `stoker.io/cr-name` | CR name | ConfigMaps | Identifies the parent GatewaySync CR that owns this resource |

## Agent image resolution order

The agent sidecar image is resolved using a three-tier fallback:

1. Pod annotation `stoker.io/agent-image` (highest priority)
2. CR field `spec.agent.image`
3. Environment variable `DEFAULT_AGENT_IMAGE` (set by Helm chart)
