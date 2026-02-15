#!/usr/bin/env bash
# Phase 03: Gateway Discovery & Status Collection
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_phase "03 — Gateway Discovery & Status"

# Ensure clean state
phase_cleanup

# Helper: create a gateway pod with customizable fields
create_gateway_pod() {
    local suffix="$1" cr_name="$2" gw_name="$3" image="${4:-registry.k8s.io/pause:3.9}"
    local pull_policy="${5:-IfNotPresent}"
    sed \
        -e "s/SUFFIX/${suffix}/g" \
        -e "s/CR_NAME/${cr_name}/g" \
        -e "s/GATEWAY_NAME/${gw_name}/g" \
        "${FIXTURES_DIR}/gateway-pod.yaml" | \
    sed "s|image: registry.k8s.io/pause:3.9|image: ${image}|g" | \
    sed "s|imagePullPolicy:.*||g" | \
    if [[ "$pull_policy" != "IfNotPresent" ]]; then
        sed "/image: /a\\
\\            imagePullPolicy: ${pull_policy}"
    else
        cat
    fi | \
    $KUBECTL apply -n "$TEST_NAMESPACE" -f -
}

# Helper: create a status ConfigMap
create_status_cm() {
    local cr_name="$1" gw_name="$2" status="$3" commit="${4:-abc123}"
    sed \
        -e "s/CR_NAME/${cr_name}/g" \
        -e "s/GATEWAY_NAME/${gw_name}/g" \
        -e "s/SYNC_STATUS/${status}/g" \
        -e "s/COMMIT_SHA/${commit}/g" \
        -e "s/REF/main/g" \
        "${FIXTURES_DIR}/gateway-status-cm.yaml" | \
    $KUBECTL apply -n "$TEST_NAMESPACE" -f -
}

# Setup: create API key secret and a CR for discovery tests
apply_fixture "api-key-secret.yaml"
apply_fixture "test-cr.yaml"
wait_for_typed_condition "ignitionsync/test-sync" "RepoCloned" "True" 90
CLONE_COMMIT=$(kubectl_json "ignitionsync/test-sync" '{.status.lastSyncCommit}')

# ────────────────────────────────────────────────────────────────────
# Test 3.1: Discover Annotated Pods
# ────────────────────────────────────────────────────────────────────
log_test "3.1: Discover Annotated Pods"

create_gateway_pod "1" "test-sync" "gateway-alpha"
create_gateway_pod "2" "test-sync" "gateway-beta"

# Wait for pods to be Running
wait_for_named_pod_phase "gateway-test-1" "Running" 60
wait_for_named_pod_phase "gateway-test-2" "Running" 60

# Wait for discoveredGateways length=2
deadline=$((SECONDS + 30))
gw_count=0
while [[ $SECONDS -lt $deadline ]]; do
    gw_count=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways | length')
    if [[ "$gw_count" == "2" ]]; then
        break
    fi
    sleep 2
done
assert_eq "2" "$gw_count" "Two gateways discovered"

# Verify names
gw_names=$(kubectl_jq "ignitionsync/test-sync" '[.status.discoveredGateways[].name] | sort | join(",")')
assert_eq "gateway-alpha,gateway-beta" "$gw_names" "Gateway names match annotations"

# ────────────────────────────────────────────────────────────────────
# Test 3.2: Non-Running Pods Excluded
# ────────────────────────────────────────────────────────────────────
log_test "3.2: Non-Running Pods Excluded"

# Create a pod that will stay Pending (nonexistent image, Never pull)
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-test-pending
  labels:
    app: gateway-test
  annotations:
    ignition-sync.io/cr-name: "test-sync"
    ignition-sync.io/gateway-name: "gateway-pending"
spec:
  containers:
    - name: bad
      image: nonexistent-image-xxxxx:latest
      imagePullPolicy: Never
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
EOF

# Give controller time to reconcile
sleep 10

# Verify it's NOT in discoveredGateways (only running pods counted)
gw_count=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways | length')
assert_eq "2" "$gw_count" "Pending pod not counted in discoveredGateways"

# Check that gateway-pending is not in the list
pending_found=$(kubectl_jq "ignitionsync/test-sync" '[.status.discoveredGateways[].name] | map(select(. == "gateway-pending")) | length')
assert_eq "0" "$pending_found" "gateway-pending not in discovered list"

# Clean up pending pod
$KUBECTL delete pod gateway-test-pending -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Test 3.3: Multi-CR Isolation
# ────────────────────────────────────────────────────────────────────
log_test "3.3: Multi-CR Isolation"

# Create a second CR
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: sync.ignition.io/v1alpha1
kind: IgnitionSync
metadata:
  name: test-sync-b
spec:
  git:
    repo: "${GIT_REPO_URL}"
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

wait_for_typed_condition "ignitionsync/test-sync-b" "RepoCloned" "True" 90

# Create a pod for test-sync-b
create_gateway_pod "b1" "test-sync-b" "gateway-bravo"
wait_for_named_pod_phase "gateway-test-b1" "Running" 60
sleep 10

# test-sync should still see 2 gateways
gw_count_a=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways | length')
assert_eq "2" "$gw_count_a" "test-sync still sees 2 gateways"

# test-sync-b should see 1 gateway
gw_count_b=$(kubectl_jq "ignitionsync/test-sync-b" '.status.discoveredGateways | length')
assert_eq "1" "$gw_count_b" "test-sync-b sees 1 gateway"

gw_name_b=$(kubectl_jq "ignitionsync/test-sync-b" '.status.discoveredGateways[0].name')
assert_eq "gateway-bravo" "$gw_name_b" "test-sync-b gateway is gateway-bravo"

# Clean up CR-B and its pod
$KUBECTL delete pod gateway-test-b1 -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
$KUBECTL delete ignitionsync test-sync-b -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion ignitionsync test-sync-b 30 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Test 3.4: Gateway Name Fallback Chain
# ────────────────────────────────────────────────────────────────────
log_test "3.4: Gateway Name Fallback Chain"

# Delete existing gateway pods to start fresh
$KUBECTL delete pod gateway-test-1 gateway-test-2 -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 5

# Pod with gateway-name annotation → name from annotation
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gw-fallback-annotated
  labels:
    app: gateway-test
    app.kubernetes.io/name: label-name
  annotations:
    ignition-sync.io/cr-name: "test-sync"
    ignition-sync.io/gateway-name: "annotation-name"
spec:
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.9
EOF

# Pod with only app.kubernetes.io/name label → name from label
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gw-fallback-labeled
  labels:
    app: gateway-test
    app.kubernetes.io/name: label-gateway
  annotations:
    ignition-sync.io/cr-name: "test-sync"
spec:
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.9
EOF

# Pod with neither → name = pod name
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gw-fallback-bare
  labels:
    app: gateway-test
  annotations:
    ignition-sync.io/cr-name: "test-sync"
spec:
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.9
EOF

wait_for_named_pod_phase "gw-fallback-annotated" "Running" 60
wait_for_named_pod_phase "gw-fallback-labeled" "Running" 60
wait_for_named_pod_phase "gw-fallback-bare" "Running" 60
sleep 10

# Verify names
gw_names=$(kubectl_jq "ignitionsync/test-sync" '[.status.discoveredGateways[].name] | sort | join(",")')
assert_contains "$gw_names" "annotation-name" "Annotation name takes priority"
assert_contains "$gw_names" "label-gateway" "Label name used when no annotation"
assert_contains "$gw_names" "gw-fallback-bare" "Pod name used as fallback"

# Clean up fallback pods
$KUBECTL delete pod gw-fallback-annotated gw-fallback-labeled gw-fallback-bare -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 5

# ────────────────────────────────────────────────────────────────────
# Test 3.5: Status Collection from ConfigMap
# ────────────────────────────────────────────────────────────────────
log_test "3.5: Status Collection from ConfigMap"

# Recreate gateway pods
create_gateway_pod "s1" "test-sync" "gw-status-test"
wait_for_named_pod_phase "gateway-test-s1" "Running" 60
sleep 5

# Create status ConfigMap with Synced status
create_status_cm "test-sync" "gw-status-test" "Synced" "$CLONE_COMMIT"
sleep 10

# Verify status fields are populated
gw_sync=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways[] | select(.name=="gw-status-test") | .syncStatus')
assert_eq "Synced" "$gw_sync" "syncStatus is Synced"

gw_commit=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways[] | select(.name=="gw-status-test") | .syncedCommit')
assert_eq "$CLONE_COMMIT" "$gw_commit" "syncedCommit matches"

gw_agent=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways[] | select(.name=="gw-status-test") | .agentVersion')
assert_eq "0.1.0" "$gw_agent" "agentVersion populated"

gw_files=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways[] | select(.name=="gw-status-test") | .filesChanged')
assert_eq "5" "$gw_files" "filesChanged populated"

gw_projects=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways[] | select(.name=="gw-status-test") | .projectsSynced | join(",")')
assert_eq "MyProject,SharedScripts" "$gw_projects" "projectsSynced populated"

# ────────────────────────────────────────────────────────────────────
# Test 3.6: AllGatewaysSynced Condition
# ────────────────────────────────────────────────────────────────────
log_test "3.6: AllGatewaysSynced Condition"

# With one gateway synced, AllGatewaysSynced should be True (1/1)
all_synced=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].status}')
all_synced_msg=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].message}')
assert_eq "True" "$all_synced" "AllGatewaysSynced=True when all gateways synced"
assert_contains "$all_synced_msg" "1/1" "Message shows 1/1 gateways synced"

# Add a second gateway pod
create_gateway_pod "s2" "test-sync" "gw-status-test-2"
wait_for_named_pod_phase "gateway-test-s2" "Running" 60
sleep 10

# Second gateway has no status yet (Pending) → AllGatewaysSynced=False
all_synced=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].status}')
all_synced_msg=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].message}')
assert_eq "False" "$all_synced" "AllGatewaysSynced=False when not all synced"
assert_contains "$all_synced_msg" "1/2" "Message shows 1/2 gateways synced"

# Update status CM to include second gateway as Synced
$KUBECTL get configmap "ignition-sync-status-test-sync" -n "$TEST_NAMESPACE" -o json | \
    jq --arg name "gw-status-test-2" --arg commit "$CLONE_COMMIT" \
    '.data[$name] = "{\"syncStatus\":\"Synced\",\"syncedCommit\":\"" + $commit + "\",\"syncedRef\":\"main\",\"lastSyncTime\":\"2025-01-01T00:00:00Z\",\"agentVersion\":\"0.1.0\",\"filesChanged\":3,\"projectsSynced\":[\"MyProject\"]}"' | \
    $KUBECTL apply -n "$TEST_NAMESPACE" -f -
sleep 10

all_synced=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].status}')
assert_eq "True" "$all_synced" "AllGatewaysSynced=True after both synced"

# Now simulate one gateway with Error status
$KUBECTL get configmap "ignition-sync-status-test-sync" -n "$TEST_NAMESPACE" -o json | \
    jq --arg name "gw-status-test-2" \
    '.data[$name] = "{\"syncStatus\":\"Error\",\"errorMessage\":\"test error\"}"' | \
    $KUBECTL apply -n "$TEST_NAMESPACE" -f -
sleep 10

all_synced=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].status}')
assert_eq "False" "$all_synced" "AllGatewaysSynced=False when one gateway has Error"

# ────────────────────────────────────────────────────────────────────
# Test 3.7: Ready Condition (Full Stack)
# ────────────────────────────────────────────────────────────────────
log_test "3.7: Ready Condition (Full Stack)"

# RepoCloned=True + AllGatewaysSynced=False → Ready=False
wait_for_typed_condition "ignitionsync/test-sync" "RepoCloned" "True" 30
log_pass "RepoCloned=True"

ready_status=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="Ready")].status}')
assert_eq "False" "$ready_status" "Ready=False when AllGatewaysSynced=False"

# Fix the error gateway
$KUBECTL get configmap "ignition-sync-status-test-sync" -n "$TEST_NAMESPACE" -o json | \
    jq --arg name "gw-status-test-2" --arg commit "$CLONE_COMMIT" \
    '.data[$name] = "{\"syncStatus\":\"Synced\",\"syncedCommit\":\"" + $commit + "\",\"syncedRef\":\"main\",\"lastSyncTime\":\"2025-01-01T00:00:00Z\",\"agentVersion\":\"0.1.0\",\"filesChanged\":3,\"projectsSynced\":[\"MyProject\"]}"' | \
    $KUBECTL apply -n "$TEST_NAMESPACE" -f -
sleep 10

# Now RepoCloned=True + AllGatewaysSynced=True → Ready=True
ready_status=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="Ready")].status}')
assert_eq "True" "$ready_status" "Ready=True when RepoCloned and AllGatewaysSynced are True"

# ────────────────────────────────────────────────────────────────────
# Test 3.8: No Gateways → AllGatewaysSynced=False, reason=NoGatewaysDiscovered
# ────────────────────────────────────────────────────────────────────
log_test "3.8: No Gateways → NoGatewaysDiscovered"

# Delete all gateway pods
$KUBECTL delete pods -n "$TEST_NAMESPACE" -l app=gateway-test --ignore-not-found 2>/dev/null || true
# Delete status ConfigMap
$KUBECTL delete configmap "ignition-sync-status-test-sync" -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 15

all_synced=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].status}')
all_reason=$(kubectl_json "ignitionsync/test-sync" \
    '{.status.conditions[?(@.type=="AllGatewaysSynced")].reason}')
assert_eq "False" "$all_synced" "AllGatewaysSynced=False with no gateways"
assert_eq "NoGatewaysDiscovered" "$all_reason" "Reason is NoGatewaysDiscovered"

# ────────────────────────────────────────────────────────────────────
# Test 3.9: Pod Deletion removes gateway from status
# ────────────────────────────────────────────────────────────────────
log_test "3.9: Pod Deletion Removes Gateway from Status"

# Create a pod, verify it appears, then delete
create_gateway_pod "del1" "test-sync" "gw-deleteme"
wait_for_named_pod_phase "gateway-test-del1" "Running" 60
sleep 10

gw_count=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways | length')
assert_eq "1" "$gw_count" "Gateway appears after pod creation"

# Delete the pod
$KUBECTL delete pod gateway-test-del1 -n "$TEST_NAMESPACE" --wait=true
sleep 15

gw_count=$(kubectl_jq "ignitionsync/test-sync" '.status.discoveredGateways | length')
assert_eq "0" "$gw_count" "Gateway removed after pod deletion"

# ────────────────────────────────────────────────────────────────────
# Test 3.10: GatewaysDiscovered Event
# ────────────────────────────────────────────────────────────────────
log_test "3.10: GatewaysDiscovered Event"

# Create a pod to trigger discovery
create_gateway_pod "evt1" "test-sync" "gw-event-test"
wait_for_named_pod_phase "gateway-test-evt1" "Running" 60
sleep 10

# Check for GatewaysDiscovered event
events=$($KUBECTL get events -n "$TEST_NAMESPACE" \
    --field-selector reason=GatewaysDiscovered \
    -o jsonpath='{.items[*].reason}' 2>/dev/null || echo "")
assert_contains "$events" "GatewaysDiscovered" "GatewaysDiscovered event emitted"

# Clean up
$KUBECTL delete pod gateway-test-evt1 -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Phase cleanup & summary
# ────────────────────────────────────────────────────────────────────
phase_cleanup
print_summary
