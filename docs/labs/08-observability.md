# Lab 08 — Observability

## Objective

Validate the operator's observability features: Prometheus metrics endpoint, structured JSON logging, Kubernetes events, and status conditions as a debugging surface. Verify these work in the real cluster environment with Ignition gateways running.

**Prerequisite:** Complete [07 — Helm Chart](07-helm-chart.md). The operator should be running (via Helm or kustomize) with `lab-sync` CR active.

---

## Lab 8.1: Prometheus Metrics Endpoint

### Purpose
Verify the controller exposes Prometheus-compatible metrics at the configured endpoint.

### Steps

```bash
# Port-forward to the controller's metrics port (default: 8443 for secure, 8080 for insecure)
CTRL_POD=$(kubectl get pods -n ignition-sync-operator-system \
  -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')

# Check what ports the controller listens on
kubectl get pod "$CTRL_POD" -n ignition-sync-operator-system -o json | \
  jq '.spec.containers[0].args'
```

The metrics endpoint is controlled by `--metrics-bind-address`. If metrics are secured (default), you may need to use the ServiceAccount token or disable secure metrics for testing.

**For insecure metrics** (if configured with `--metrics-bind-address=:8080`):
```bash
kubectl port-forward "pod/${CTRL_POD}" -n ignition-sync-operator-system 8080:8080 &
sleep 2
curl -s http://localhost:8080/metrics | head -50
```

**For secure metrics** (default `--metrics-secure=true`):
```bash
# Use kubectl exec to fetch from inside the pod
kubectl exec "$CTRL_POD" -n ignition-sync-operator-system -- \
  wget -qO- http://localhost:8080/metrics 2>/dev/null | head -50 || \
  echo "Metrics may be on a different port or require TLS"
```

### What to Verify

Metrics output should include:

1. **Controller-runtime standard metrics:**
   ```
   controller_runtime_reconcile_total{controller="ignitionsync",...}
   controller_runtime_reconcile_time_seconds_bucket{...}
   controller_runtime_reconcile_errors_total{...}
   ```

2. **Workqueue metrics:**
   ```
   workqueue_depth{name="ignitionsync"}
   workqueue_adds_total{name="ignitionsync"}
   workqueue_retries_total{name="ignitionsync"}
   ```

3. **Custom operator metrics** (if implemented):
   ```
   ignition_sync_ref_resolve_duration_seconds{...}
   ignition_sync_discovered_gateways{...}
   ignition_sync_synced_gateways{...}
   ignition_sync_agent_clone_duration_seconds{...}
   ```

Record which custom metrics exist — this informs whether we need to add more in a future iteration.

---

## Lab 8.2: Structured JSON Logging

### Purpose
Verify controller logs are structured JSON with expected fields, making them parseable by log aggregation systems (Loki, Elasticsearch, CloudWatch).

### Steps

```bash
# Get recent logs
kubectl logs "$CTRL_POD" -n ignition-sync-operator-system --tail=30
```

### What to Verify

1. **Logs are JSON-formatted** (each line is valid JSON):
   ```bash
   kubectl logs "$CTRL_POD" -n ignition-sync-operator-system --tail=10 | \
     while read line; do
       echo "$line" | jq . >/dev/null 2>&1 && echo "JSON OK" || echo "NOT JSON: $line"
     done
   ```

2. **Standard fields present** in each log entry:
   ```bash
   kubectl logs "$CTRL_POD" -n ignition-sync-operator-system --tail=5 | jq -r 'keys | join(", ")' 2>/dev/null | head -3
   ```
   Expected fields: `ts` (or `timestamp`), `level`, `logger`, `msg`, `controller`, `namespace`, `name`

3. **Reconciliation logs have CR context:**
   ```bash
   kubectl logs "$CTRL_POD" -n ignition-sync-operator-system --tail=50 | \
     jq -r 'select(.msg | test("reconcil|resolve|gateway|ref"; "i")) | {msg, controller, namespace, name}' 2>/dev/null | head -20
   ```
   Expected: Each reconciliation log includes the CR namespace and name.

4. **No unstructured log lines** (everything should be JSON):
   ```bash
   NON_JSON=$(kubectl logs "$CTRL_POD" -n ignition-sync-operator-system --tail=100 | \
     while read line; do echo "$line" | jq . >/dev/null 2>&1 || echo "$line"; done | wc -l | tr -d ' ')
   echo "Non-JSON lines: $NON_JSON"
   ```
   Expected: `0` (or very few — startup banner may be unstructured).

---

## Lab 8.3: Kubernetes Events

### Purpose
Verify the operator emits meaningful Kubernetes events that appear in `kubectl describe` and event streams.

### Steps

```bash
# Get all events for the CR
kubectl describe ignitionsync lab-sync -n lab | grep -A 30 "^Events:"

# Get events by field selector
kubectl get events -n lab \
  --field-selector involvedObject.name=lab-sync,involvedObject.kind=IgnitionSync \
  --sort-by=.lastTimestamp
```

### Expected Events

| Reason | Type | Message Pattern |
|--------|------|----------------|
| `GatewaysDiscovered` | Normal | `Discovered N gateway(s)` |
| `RefResolved` | Normal/Warning | Ref resolved / resolution failed |

```bash
# Check for specific event reasons
for reason in GatewaysDiscovered; do
  count=$(kubectl get events -n lab --field-selector reason=$reason --no-headers 2>/dev/null | wc -l | tr -d ' ')
  echo "$reason: $count events"
done
```

### Edge Case: Events After Error Recovery

Trigger an error and recovery to verify events are emitted for both:

```bash
# Create a CR that will fail (bad repo)
cat <<EOF | kubectl apply -n lab -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: event-test
spec:
  git:
    repo: "https://github.com/ia-eknorr/nonexistent-repo-does-not-exist.git"
    ref: "main"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
EOF

sleep 20

# Check events for the error
kubectl get events -n lab --field-selector involvedObject.name=event-test --sort-by=.lastTimestamp

# Fix it
kubectl patch ignitionsync event-test -n lab --type=merge \
  -p '{"spec":{"git":{"repo":"https://github.com/ia-eknorr/test-ignition-project.git"}}}'

sleep 30

# Check events for recovery
kubectl get events -n lab --field-selector involvedObject.name=event-test --sort-by=.lastTimestamp

kubectl delete ignitionsync event-test -n lab
```

---

## Lab 8.4: Status Conditions as Debugging Surface

### Purpose
Verify conditions provide enough information for operators to diagnose issues without reading logs.

### Steps

```bash
# Full condition output
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.conditions[] | {
  type,
  status,
  reason,
  message,
  lastTransitionTime
}]'
```

### What to Verify

1. **All expected condition types present:**
   - `Ready`
   - `RefResolved`
   - `AllGatewaysSynced`

2. **Messages are human-readable** and actionable:
   - Not just "True" or "False" — should explain WHY
   - Error conditions should include enough detail to diagnose

3. **lastTransitionTime is recent** (not stale):
   ```bash
   kubectl get ignitionsync lab-sync -n lab -o json | jq '.status.conditions[] | {
     type,
     age: ((now - (.lastTransitionTime | fromdateiso8601)) | tostring + "s ago")
   }'
   ```

4. **kubectl columns reflect conditions:**
   ```bash
   kubectl get ignitionsyncs -n lab -o wide
   ```
   Expected: REF, SYNCED, GATEWAYS, READY, AGE columns all populated.

---

## Lab 8.5: Log Levels and Verbosity

### Purpose
Verify the controller respects log verbosity settings (useful for debugging vs. production).

### Steps

```bash
# Check current log level configuration
kubectl get deployment ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system -o json | \
  jq '.spec.template.spec.containers[0].args'

# If using zap logger, check for --zap-log-level flag
# Increase verbosity to debug
kubectl patch deployment ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --type=json \
  -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--zap-log-level=debug"}]' \
  2>/dev/null || echo "Args patching may need different approach"

kubectl rollout status deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --timeout=60s 2>/dev/null || true
```

### What to Verify

In debug mode, logs should show more detail:
```bash
kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=30
```

Look for additional context in debug entries (e.g., specific resource versions, reconcile triggers, cache sync details).

---

## Lab 8.6: Health Endpoints

### Purpose
Verify liveness and readiness probes work correctly.

### Steps

```bash
CTRL_POD=$(kubectl get pods -n ignition-sync-operator-system \
  -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')

# Check health endpoints from inside the pod
kubectl exec "$CTRL_POD" -n ignition-sync-operator-system -- \
  wget -qO- http://localhost:8081/healthz 2>/dev/null || echo "healthz: check port"

kubectl exec "$CTRL_POD" -n ignition-sync-operator-system -- \
  wget -qO- http://localhost:8081/readyz 2>/dev/null || echo "readyz: check port"
```

### Expected
Both return `ok` with HTTP 200.

### Edge Case: Readiness During Startup

```bash
# Delete the controller pod to force a restart
kubectl delete pod "$CTRL_POD" -n ignition-sync-operator-system

# Immediately check readiness
sleep 2
NEW_POD=$(kubectl get pods -n ignition-sync-operator-system \
  -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')

kubectl get pod "$NEW_POD" -n ignition-sync-operator-system -o json | jq '{
  phase: .status.phase,
  ready: (.status.conditions[] | select(.type=="Ready") | .status),
  containerReady: (.status.containerStatuses[0].ready)
}'
```

The pod should go through `Not Ready → Ready` as the controller initializes.

---

## Lab 8.7: Agent Logs (if sidecar is injected)

### Purpose
Verify agent sidecar logs are accessible and structured. The agent clones the git repo to a local emptyDir on the gateway pod and handles sync operations independently.

### Steps

```bash
# If injection is active from Lab 06
kubectl logs ignition-0 -n lab -c sync-agent --tail=20 2>/dev/null || \
  echo "No agent sidecar — skip this lab"
```

### What to Verify
- Agent logs are structured JSON
- Include gateway name, CR name, sync status
- Include clone and sync timing information (clone duration, sync duration)
- Show the agent cloning the repo to its local emptyDir

---

## Lab 8.8: Full Observability Walkthrough — Simulated Incident

### Purpose
Simulate a real debugging scenario: something breaks, and we use only observability tools (no code reading) to diagnose it.

### Scenario
Point the CR at a bad repo URL, observe the operator's response, then fix it.

> **Note:** Since we use a real GitHub repo, we simulate a git failure by switching the CR to a nonexistent repo URL, then fixing it back.

### Steps

```bash
# Break the git connection by pointing to a nonexistent repo
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"repo":"https://github.com/ia-eknorr/nonexistent-repo-does-not-exist.git"}}}'

# Watch the operator's response
echo "=== Events ==="
kubectl get events -n lab --field-selector involvedObject.name=lab-sync --sort-by=.lastTimestamp --watch &
WATCH_PID=$!
sleep 5

echo "=== Conditions ==="
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.conditions[] | {type, status, reason, message}]'

echo "=== Operator Logs ==="
kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=10 | \
  jq '{ts: .ts, msg: .msg, error: .error}' 2>/dev/null || \
  kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=10

# Fix it — restore the correct repo URL
kubectl patch ignitionsync lab-sync -n lab --type=merge \
  -p '{"spec":{"git":{"repo":"https://github.com/ia-eknorr/test-ignition-project.git"}}}'

sleep 30

echo "=== Conditions After Recovery ==="
kubectl get ignitionsync lab-sync -n lab -o json | jq '[.status.conditions[] | {type, status, reason, message}]'

kill $WATCH_PID 2>/dev/null || true
```

### What to Verify
1. **During outage:** Events and conditions clearly indicate the problem (ref resolution failed for nonexistent repo)
2. **Logs:** Error messages include enough context (repo URL, error details) to diagnose
3. **After recovery:** Conditions return to healthy state, events show recovery
4. **No manual intervention needed** beyond fixing the root cause (repo URL)

---

## Phase 8 Completion Checklist

| Check | Status |
|-------|--------|
| Prometheus metrics endpoint reachable | |
| controller_runtime_reconcile_total metric exists | |
| workqueue metrics present | |
| Controller logs are structured JSON | |
| Log entries include namespace, name, controller context | |
| No unstructured log lines in normal operation | |
| GatewaysDiscovered event emitted | |
| Events emitted for error and recovery scenarios | |
| Status conditions provide actionable debugging info | |
| kubectl columns (REF, SYNCED, READY) populated | |
| Health endpoints (/healthz, /readyz) return ok | |
| Readiness transitions correctly during pod restart | |
| Agent sidecar logs accessible and structured | |
| Full incident walkthrough: break → diagnose → fix using only observability | |
