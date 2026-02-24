---
sidebar_position: 3
title: Webhook Sync
description: Trigger instant syncs on git push events via webhook.
---

# Webhook Sync

By default, Stoker polls for git changes at a configurable interval (default 60s). For faster feedback, configure a webhook so pushes trigger syncs immediately.

## How it works

The controller runs an HTTP server (port 9444) that accepts webhook payloads. When a payload arrives, the receiver:

1. Validates the HMAC signature (if configured)
2. Extracts the ref from the payload (auto-detects format)
3. Annotates the GatewaySync CR with the requested ref
4. The controller's reconciliation predicate detects the annotation change and triggers an immediate sync

## Endpoint

```
POST /webhook/{namespace}/{crName}
```

- `{namespace}` — the namespace of the GatewaySync CR
- `{crName}` — the name of the GatewaySync CR

The Helm chart creates a Service for the webhook receiver automatically.

## Exposing the receiver

The webhook receiver Service needs to be reachable from your git hosting provider. Common approaches:

**Ingress (recommended for production):**

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: stoker-webhook
  namespace: stoker-system
spec:
  rules:
    - host: stoker-webhook.example.com
      http:
        paths:
          - path: /webhook
            pathType: Prefix
            backend:
              service:
                name: stoker-stoker-operator-webhook-receiver
                port:
                  number: 9444
```

**Port-forward (for testing):**

```bash
kubectl port-forward -n stoker-system svc/stoker-stoker-operator-webhook-receiver 9444:9444
```

## Payload formats

The receiver auto-detects the payload format. No configuration needed — just point your webhook at the endpoint.

### GitHub release

```json
{
  "action": "published",
  "release": {
    "tag_name": "v2.0.0"
  }
}
```

### ArgoCD notification

```json
{
  "app": {
    "metadata": {
      "annotations": {
        "git.ref": "v2.0.0"
      }
    }
  }
}
```

### Kargo promotion

```json
{
  "freight": {
    "commits": [
      {
        "tag": "v2.0.0"
      }
    ]
  }
}
```

### Generic

Any system can trigger a sync by sending:

```json
{
  "ref": "v2.0.0"
}
```

## HMAC signature validation

To verify that payloads come from a trusted source, configure HMAC validation.

### Option 1: Inline secret

```yaml
# Helm values
webhookReceiver:
  hmac:
    secret: "my-webhook-secret"
```

### Option 2: Existing secret

```bash
kubectl create secret generic webhook-hmac -n stoker-system \
  --from-literal=webhook-secret=my-webhook-secret
```

```yaml
# Helm values
webhookReceiver:
  hmac:
    secretRef:
      name: webhook-hmac
      key: webhook-secret
```

The receiver validates the `X-Hub-Signature-256` header against the payload using HMAC-SHA256. Requests with missing or invalid signatures are rejected with HTTP 401.

## GitHub webhook setup

1. Go to your repository **Settings → Webhooks → Add webhook**
2. Set **Payload URL** to `https://stoker-webhook.example.com/webhook/{namespace}/{crName}`
3. Set **Content type** to `application/json`
4. Set **Secret** to the same value configured in your Helm values
5. Select events: **Releases** (for tag-based deploys) or **Pushes** (for branch-based deploys)
6. Click **Add webhook**

Test with a curl:

```bash
curl -X POST https://stoker-webhook.example.com/webhook/my-namespace/my-sync \
  -H "Content-Type: application/json" \
  -d '{"ref": "v1.0.0"}'
```

## Combining with polling

Webhooks and polling are complementary. A good production pattern:

- Set a **long poll interval** (e.g., `5m`) as a fallback in case a webhook is missed
- Use **webhooks** for instant sync on push events

```yaml
spec:
  polling:
    enabled: true
    interval: "5m"  # Fallback only — webhooks handle normal flow
```

To disable polling entirely when relying solely on webhooks:

```yaml
spec:
  polling:
    enabled: false
```

## Next steps

- [Helm Values](../reference/helm-values.md#push-receiver-webhook) — webhook receiver configuration
- [Annotations Reference](../reference/annotations.md) — CR annotations set by the receiver
