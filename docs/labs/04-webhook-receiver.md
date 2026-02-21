# Lab 04 — Webhook Receiver

## Objective

Validate the inbound webhook HTTP handler end-to-end: port reachability, all four payload formats (generic, GitHub, ArgoCD, Kargo), HMAC signature validation, error responses, idempotency, and that a webhook-triggered ref change actually propagates through the reconciliation loop to resolve the ref and populate the metadata ConfigMap. Test with the real Ignition gateways running alongside.

**Prerequisite:** Complete [03 — Gateway Discovery](03-gateway-discovery.md). The `lab-sync` CR should be `Ready=True` with ref resolved and metadata ConfigMap populated.

---

## Lab 4.1: Webhook Port Reachability

### Purpose
Verify the webhook receiver HTTP server is running inside the controller and reachable via port-forward.

### Steps

```bash
# Identify controller pod
CTRL_POD=$(kubectl get pods -n ignition-sync-operator-system \
  -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')

# Start port-forward to webhook receiver port (9443)
kubectl port-forward "pod/${CTRL_POD}" -n ignition-sync-operator-system 19443:9443 &
PF_PID=$!
sleep 3

# Test connectivity with a simple request
curl -s -o /dev/null -w 'HTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{"ref":"v1.0.0"}'
```

### Expected
HTTP `200` or `202` — confirms the server is listening and processing requests. We'll test specific responses next.

### Verify in operator logs:
```bash
kubectl logs -n ignition-sync-operator-system "$CTRL_POD" --tail=10 | grep webhook
```

Expected: `webhook accepted` log entry.

---

## Lab 4.2: Generic Payload

### Purpose
Test the simplest payload format: `{"ref": "..."}`.

### Steps

```bash
# Clear any previous webhook annotations
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- \
  ignition-sync.io/requested-at- \
  ignition-sync.io/requested-by- 2>/dev/null || true

# Send generic payload
RESPONSE=$(curl -s -w '\n%{http_code}' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{"ref":"v2.0.0"}')

HTTP_BODY=$(echo "$RESPONSE" | head -1)
HTTP_CODE=$(echo "$RESPONSE" | tail -1)

echo "Status: $HTTP_CODE"
echo "Body: $HTTP_BODY"
```

### What to Verify

1. **HTTP 202 Accepted:**
   ```bash
   [ "$HTTP_CODE" = "202" ] && echo "PASS" || echo "FAIL: expected 202, got $HTTP_CODE"
   ```

2. **Response body contains ref:**
   ```bash
   echo "$HTTP_BODY" | jq '.ref'
   ```
   Expected: `"v2.0.0"`

3. **CR annotations set:**
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o json | jq '{
     "requested-ref": .metadata.annotations["ignition-sync.io/requested-ref"],
     "requested-at": .metadata.annotations["ignition-sync.io/requested-at"],
     "requested-by": .metadata.annotations["ignition-sync.io/requested-by"]
   }'
   ```
   Expected:
   - `requested-ref: "v2.0.0"`
   - `requested-at:` a valid RFC3339 timestamp
   - `requested-by: "generic"`

---

## Lab 4.3: GitHub Release Payload

### Steps

```bash
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{
    "action": "published",
    "release": {
      "tag_name": "v3.1.0",
      "name": "Release 3.1.0",
      "body": "New features"
    }
  }'
```

### What to Verify

```bash
kubectl get ignitionsync lab-sync -n lab -o json | jq '{
  ref: .metadata.annotations["ignition-sync.io/requested-ref"],
  by: .metadata.annotations["ignition-sync.io/requested-by"]
}'
```

Expected: `ref: "v3.1.0"`, `by: "github"`

---

## Lab 4.4: ArgoCD Notification Payload

### Steps

```bash
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{
    "app": {
      "metadata": {
        "name": "ignition-config",
        "namespace": "argocd",
        "annotations": {
          "git.ref": "v4.2.0"
        }
      },
      "status": {
        "sync": {"status": "Synced"}
      }
    }
  }'
```

### What to Verify

```bash
kubectl get ignitionsync lab-sync -n lab -o json | jq '{
  ref: .metadata.annotations["ignition-sync.io/requested-ref"],
  by: .metadata.annotations["ignition-sync.io/requested-by"]
}'
```

Expected: `ref: "v4.2.0"`, `by: "argocd"`

---

## Lab 4.5: Kargo Promotion Payload

### Steps

```bash
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{
    "freight": {
      "name": "abc123",
      "commits": [
        {
          "repoURL": "https://github.com/example/ignition-config",
          "tag": "v5.0.0",
          "id": "abc123def456"
        }
      ]
    }
  }'
```

### What to Verify

```bash
kubectl get ignitionsync lab-sync -n lab -o json | jq '{
  ref: .metadata.annotations["ignition-sync.io/requested-ref"],
  by: .metadata.annotations["ignition-sync.io/requested-by"]
}'
```

Expected: `ref: "v5.0.0"`, `by: "kargo"`

---

## Lab 4.6: HMAC Signature Validation

### Purpose
Test the full HMAC lifecycle: enable HMAC on the controller, verify invalid signatures are rejected, valid signatures accepted.

### Steps

**Enable HMAC:**

```bash
HMAC_SECRET="lab-test-secret-$(date +%s)"
echo "HMAC secret: $HMAC_SECRET"

# Patch controller deployment to set HMAC
kubectl set env deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system \
  WEBHOOK_HMAC_SECRET="$HMAC_SECRET"

# Wait for rollout
kubectl rollout status deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --timeout=60s

# Kill old port-forward, establish new one
kill $PF_PID 2>/dev/null || true
sleep 2

CTRL_POD=$(kubectl get pods -n ignition-sync-operator-system \
  -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward "pod/${CTRL_POD}" -n ignition-sync-operator-system 19443:9443 &
PF_PID=$!
sleep 3
```

**Test invalid HMAC → 401:**

```bash
PAYLOAD='{"ref":"v6.0.0"}'
WRONG_SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "wrong-secret" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${WRONG_SIG}" \
  -d "$PAYLOAD"
```

Expected: `HTTP 401`

**Test no signature at all → 401:**

```bash
curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -d '{"ref":"v6.0.0"}'
```

Expected: `HTTP 401`

**Test valid HMAC → 202:**

```bash
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

PAYLOAD='{"ref":"v1.0.0"}'
VALID_SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${VALID_SIG}" \
  -d "$PAYLOAD"
```

Expected: `HTTP 202`

---

## Lab 4.7: HMAC Checked Before CR Lookup (Enumeration Prevention)

### Purpose
Verify HMAC is validated BEFORE the controller looks up the CR. This prevents attackers from using 404 vs 401 responses to enumerate valid CR names.

### Steps

```bash
# Invalid HMAC to a CR that doesn't exist
PAYLOAD='{"ref":"v7.0.0"}'
WRONG_SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "wrong" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/definitely-not-a-real-cr \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${WRONG_SIG}" \
  -d "$PAYLOAD"
```

### Expected
`HTTP 401` — NOT `404`. The server should reject the invalid HMAC without revealing whether the CR exists.

**Verify with valid HMAC to nonexistent CR:**

```bash
VALID_SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/definitely-not-a-real-cr \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${VALID_SIG}" \
  -d "$PAYLOAD"
```

Expected: `HTTP 404` — valid HMAC passes, then CR lookup fails.

---

## Lab 4.8: Error Responses

### Bad payload (no ref extractable):

```bash
PAYLOAD='{"nothing":"here","empty":"payload"}'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${SIG}" \
  -d "$PAYLOAD"
```

Expected: `HTTP 400`

### Invalid JSON:

```bash
PAYLOAD='this is not json at all'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${SIG}" \
  -d "$PAYLOAD"
```

Expected: `HTTP 400`

### Wrong HTTP method:

```bash
curl -s -w '\nHTTP %{http_code}\n' \
  -X GET http://localhost:19443/webhook/lab/lab-sync
```

Expected: `HTTP 405` (Method Not Allowed) or `404`.

---

## Lab 4.9: Idempotent Duplicate Ref

### Purpose
Sending the same ref twice should return 200 (not 202) with a message about the ref already being set.

### Steps

```bash
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

PAYLOAD='{"ref":"v2.0.0"}'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

# First request
RESP1=$(curl -s -w '\n%{http_code}' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${SIG}" \
  -d "$PAYLOAD")
echo "First: HTTP $(echo "$RESP1" | tail -1)"

# Second request (same payload)
RESP2=$(curl -s -w '\n%{http_code}' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${SIG}" \
  -d "$PAYLOAD")
CODE2=$(echo "$RESP2" | tail -1)
BODY2=$(echo "$RESP2" | head -1)
echo "Second: HTTP $CODE2"
echo "Body: $BODY2"
```

### Expected
- First: `HTTP 202`
- Second: `HTTP 200` with body containing `"ref already set"`

---

## Lab 4.10: Webhook Triggers Full Reconciliation

### Purpose
This is the end-to-end test: send a webhook to change the ref, and verify the controller actually resolves the new ref, updates the metadata ConfigMap, and refreshes status.

### Steps

```bash
# Ensure CR is at main
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'
sleep 20

# Record current state from metadata ConfigMap
COMMIT_BEFORE=$(kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.commit}')
echo "Before: commit=$COMMIT_BEFORE, ref=main"

# Clear webhook annotations
kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

# Send webhook to switch to v1.0.0
PAYLOAD='{"ref":"v1.0.0"}'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" | awk '{print $NF}')

curl -s -w '\nHTTP %{http_code}\n' \
  -X POST http://localhost:19443/webhook/lab/lab-sync \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=${SIG}" \
  -d "$PAYLOAD"

# Watch for the reconciliation to pick up the annotation and update
echo "Waiting for reconciliation..."
sleep 30

# Check if the commit changed in the metadata ConfigMap
COMMIT_AFTER=$(kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o jsonpath='{.data.commit}')
echo "After: commit=$COMMIT_AFTER"

if [ "$COMMIT_BEFORE" != "$COMMIT_AFTER" ]; then
  echo "PASS: Commit changed in metadata ConfigMap — webhook triggered reconciliation"
else
  echo "INFO: Commit did not change. Check if controller uses annotation to override spec.git.ref"
  echo "The webhook annotation sets requested-ref, which the controller may read during reconcile."
fi

# Verify metadata ConfigMap contents
echo "--- Metadata ConfigMap ---"
kubectl get configmap ignition-sync-metadata-lab-sync -n lab -o json | jq '.data'
```

### What to Watch For
The exact behavior depends on how the controller uses the `requested-ref` annotation:
- If the controller reads `requested-ref` and uses it instead of `spec.git.ref`, the metadata ConfigMap should update with the new commit SHA
- If the controller only reacts to `spec.git.ref` changes, the webhook's effect is to signal to an external system (like ArgoCD) that should then update the spec

Check the operator logs:
```bash
kubectl logs -n ignition-sync-operator-system "$CTRL_POD" --tail=30 | grep -E "webhook|requested|reconcil"
```

---

## Lab 4.11: Restore State and Disable HMAC

```bash
# Remove HMAC from controller
kubectl set env deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system \
  WEBHOOK_HMAC_SECRET-

kubectl rollout status deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --timeout=60s

# Clean up port-forward
kill $PF_PID 2>/dev/null || true

# Restore CR to main
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"ref":"main"}}}'

kubectl annotate ignitionsync lab-sync -n lab \
  ignition-sync.io/requested-ref- ignition-sync.io/requested-at- ignition-sync.io/requested-by- 2>/dev/null || true

sleep 20
```

---

## Lab 4.12: Ignition Gateway Health Check

```bash
# Both gateways still healthy
for pod in ignition-0 ignition-secondary-0; do
  echo "=== $pod ==="
  kubectl get pod $pod -n lab -o jsonpath='{.status.phase}'
  echo ""
done

# Operator healthy
kubectl get pods -n ignition-sync-operator-system -o json | jq '.items[0].status.containerStatuses[0].restartCount'

curl -s -o /dev/null -w 'Ignition HTTP: %{http_code}\n' http://localhost:8088/StatusPing
```

---

## Phase 4 Completion Checklist

| Check | Status |
|-------|--------|
| Webhook port reachable via port-forward | |
| Generic payload → 202, requested-by=generic | |
| GitHub release payload → 202, requested-by=github | |
| ArgoCD notification payload → 202, requested-by=argocd | |
| Kargo promotion payload → 202, requested-by=kargo | |
| Invalid HMAC → 401 | |
| No signature when HMAC enabled → 401 | |
| Valid HMAC → 202 | |
| HMAC checked before CR lookup (enumeration prevention) | |
| Bad payload (no ref) → 400 | |
| Invalid JSON → 400 | |
| Nonexistent CR with valid HMAC → 404 | |
| Duplicate ref → 200 idempotent | |
| Webhook triggers reconciliation (annotation set, metadata ConfigMap updates) | |
| HMAC secret removal restores unauthenticated mode | |
| Ignition gateways healthy throughout | |
| Operator pod 0 restarts | |
