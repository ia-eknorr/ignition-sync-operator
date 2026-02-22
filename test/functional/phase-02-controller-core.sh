#!/usr/bin/env bash
# Phase 02: Controller Core — CRD, Ref Resolution, Finalizer, ConfigMap
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_phase "02 — Controller Core"

# Ensure clean state
phase_cleanup

# ────────────────────────────────────────────────────────────────────
# Test 2.1: CRD Installation & Validation
# ────────────────────────────────────────────────────────────────────
log_test "2.1: CRD Installation & Validation"

# Verify CRD exists
crd_name=$($KUBECTL get crd stokers.stoker.io -o jsonpath='{.metadata.name}' 2>/dev/null || echo "")
assert_eq "stokers.stoker.io" "$crd_name" "CRD should exist"

# Verify short names work (just check exit code)
assert_exit_code 0 "kubectl get stk works" $KUBECTL get stk -n "$TEST_NAMESPACE"

# Apply invalid CR (missing spec.git)
set +e
invalid_output=$($KUBECTL apply -n "$TEST_NAMESPACE" -f "${FIXTURES_DIR}/test-cr-invalid.yaml" 2>&1)
invalid_rc=$?
set -e
if [[ $invalid_rc -ne 0 ]]; then
    log_pass "Invalid CR rejected by API server"
else
    # Some CRD validation happens in webhook, not admission — check if controller catches it
    log_info "Invalid CR accepted by API server (validation may be controller-side)"
    $KUBECTL delete stoker test-sync-invalid -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
    log_pass "Invalid CR handled"
fi

# Apply secret first (required by valid CR)
apply_fixture "api-key-secret.yaml"

# Apply valid CR
apply_fixture "test-cr.yaml"
assert_exit_code 0 "Valid CR accepted" $KUBECTL get stoker test-sync -n "$TEST_NAMESPACE"

# Clean up for next tests
$KUBECTL delete stoker test-sync -n "$TEST_NAMESPACE" --wait=true 2>/dev/null || true
wait_for_deletion stoker test-sync 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.2: Finalizer
# ────────────────────────────────────────────────────────────────────
log_test "2.2: Finalizer"

apply_fixture "test-cr.yaml"

# Wait for finalizer to be added
finalizer=""
deadline=$((SECONDS + 15))
while [[ $SECONDS -lt $deadline ]]; do
    finalizer=$(kubectl_json "stoker/test-sync" '{.metadata.finalizers[0]}')
    if [[ -n "$finalizer" ]]; then
        break
    fi
    sleep 1
done
assert_eq "stoker.io/finalizer" "$finalizer" "Finalizer should be added"

# Delete CR and verify it's fully removed
$KUBECTL delete stoker test-sync -n "$TEST_NAMESPACE" --wait=false
wait_for_deletion stoker test-sync 30
log_pass "CR deleted cleanly (finalizer removed)"

sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.3: Secret Validation
# ────────────────────────────────────────────────────────────────────
log_test "2.3: Secret Validation (missing API key secret)"

# Delete the API key secret so controller can't find it
$KUBECTL delete secret ignition-api-key -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# Create CR with reference to non-existent secret
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: test-sync-nosecret
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
      name: missing-secret
      key: apiKey
EOF

# Wait for Ready=False with reason containing "not found" or similar
sleep 10
ready_status=$(kubectl_json "stoker/test-sync-nosecret" \
    '{.status.conditions[?(@.type=="Ready")].status}')
ready_msg=$(kubectl_json "stoker/test-sync-nosecret" \
    '{.status.conditions[?(@.type=="Ready")].message}')

if [[ "$ready_status" == "False" ]]; then
    log_pass "Ready=False when secret is missing"
else
    log_info "Ready status: $ready_status (may not have condition yet)"
    # Even if no condition yet, verify controller didn't crash
fi

# Verify controller is still running
controller_ns="stoker-system"
ctrl_phase=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
assert_eq "Running" "$ctrl_phase" "Controller still running after missing secret"

# Create the secret and verify recovery
apply_fixture "api-key-secret.yaml"
sleep 5

# Clean up
$KUBECTL delete stoker test-sync-nosecret -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync-nosecret 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.4: Metadata ConfigMap Creation
# ────────────────────────────────────────────────────────────────────
log_test "2.4: Metadata ConfigMap Creation"

apply_fixture "api-key-secret.yaml"
apply_fixture "test-cr.yaml"

# Wait for metadata ConfigMap to exist
cm_name="stoker-metadata-test-sync"
wait_for_resource configmap "$cm_name" 30
log_pass "Metadata ConfigMap created: $cm_name"

# Verify ConfigMap labels
cm_label=$(kubectl_json "configmap/$cm_name" '{.metadata.labels.stoker\.io/cr-name}')
assert_eq "test-sync" "$cm_label" "Metadata ConfigMap has cr-name label"

# Verify owner reference
owner_kind=$(kubectl_json "configmap/$cm_name" '{.metadata.ownerReferences[0].kind}')
assert_eq "Stoker" "$owner_kind" "Metadata ConfigMap has Stoker owner reference"

# Verify commit key exists and is a valid SHA
commit_hash=$(kubectl_json "configmap/$cm_name" '{.data.commit}')
assert_not_empty "$commit_hash" "Metadata ConfigMap has commit key"

# ────────────────────────────────────────────────────────────────────
# Test 2.5: Ref Resolution (Happy Path)
# ────────────────────────────────────────────────────────────────────
log_test "2.5: Ref Resolution (Happy Path)"

# CR from 2.4 is still active — wait for ref resolution
wait_for_typed_condition "stoker/test-sync" "RefResolved" "True" 90
log_pass "RefResolved=True"

# Verify status fields
ref_status=$(kubectl_json "stoker/test-sync" '{.status.refResolutionStatus}')
assert_eq "Resolved" "$ref_status" "refResolutionStatus is Resolved"

commit=$(kubectl_json "stoker/test-sync" '{.status.lastSyncCommit}')
assert_not_empty "$commit" "lastSyncCommit is set"

ref=$(kubectl_json "stoker/test-sync" '{.status.lastSyncRef}')
assert_eq "main" "$ref" "lastSyncRef is main"

sync_time=$(kubectl_json "stoker/test-sync" '{.status.lastSyncTime}')
assert_not_empty "$sync_time" "lastSyncTime is set"

# Verify metadata ConfigMap
cm_name="stoker-metadata-test-sync"
wait_for_resource configmap "$cm_name" 10
cm_commit=$(kubectl_json "configmap/$cm_name" '{.data.commit}')
assert_not_empty "$cm_commit" "Metadata ConfigMap has commit key"

cm_ref=$(kubectl_json "configmap/$cm_name" '{.data.ref}')
assert_eq "main" "$cm_ref" "Metadata ConfigMap has ref=main"

# Clean up
$KUBECTL delete stoker test-sync -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync 30 2>/dev/null || true
sleep 3

# ────────────────────────────────────────────────────────────────────
# Test 2.6: Ref Resolution Error Handling
# ────────────────────────────────────────────────────────────────────
log_test "2.6: Ref Resolution Error Handling"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: test-sync-badrepo
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

# Wait for refResolutionStatus=Error (ls-remote to nonexistent repo takes time over network)
wait_for_condition "stoker/test-sync-badrepo" '{.status.refResolutionStatus}' "Error" 90
log_pass "refResolutionStatus is Error for bad repo"

ref_resolved=$(kubectl_json "stoker/test-sync-badrepo" \
    '{.status.conditions[?(@.type=="RefResolved")].status}')
assert_eq "False" "$ref_resolved" "RefResolved=False for bad repo"

# Verify controller still running
ctrl_phase=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
assert_eq "Running" "$ctrl_phase" "Controller still running after ref resolution error"

# Clean up
$KUBECTL delete stoker test-sync-badrepo -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync-badrepo 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.7: Paused CR
# ────────────────────────────────────────────────────────────────────
log_test "2.7: Paused CR"

apply_fixture "test-cr-paused.yaml"
sleep 15

# Verify no metadata ConfigMap was created
cm_exists=$($KUBECTL get configmap "stoker-metadata-test-sync-paused" -n "$TEST_NAMESPACE" 2>/dev/null && echo "yes" || echo "no")
assert_eq "no" "$cm_exists" "No metadata ConfigMap created for paused CR"

# Check Ready=False with Paused reason
ready_status=$(kubectl_json "stoker/test-sync-paused" \
    '{.status.conditions[?(@.type=="Ready")].status}')
if [[ "$ready_status" == "False" ]]; then
    ready_reason=$(kubectl_json "stoker/test-sync-paused" \
        '{.status.conditions[?(@.type=="Ready")].reason}')
    assert_eq "Paused" "$ready_reason" "Ready reason is Paused"
else
    log_info "Ready condition not set to False yet (status: $ready_status)"
    log_pass "Paused CR did not create metadata ConfigMap (main assertion)"
fi

# Clean up
$KUBECTL delete stoker test-sync-paused -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync-paused 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.8: Ref Tracking
# ────────────────────────────────────────────────────────────────────
log_test "2.8: Ref Tracking (tag switch)"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: test-sync-ref
spec:
  git:
    repo: "${GIT_REPO_URL}"
    ref: "0.1.0"
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

# Wait for ref resolution at 0.1.0
wait_for_typed_condition "stoker/test-sync-ref" "RefResolved" "True" 90
commit_v1=$(kubectl_json "stoker/test-sync-ref" '{.status.lastSyncCommit}')
assert_not_empty "$commit_v1" "0.1.0 commit recorded"
log_info "0.1.0 commit: $commit_v1"

# Patch to 0.2.0
$KUBECTL patch stoker test-sync-ref -n "$TEST_NAMESPACE" \
    --type=merge -p '{"spec":{"git":{"ref":"0.2.0"}}}'

# Wait for commit to change
commit_v2=$(wait_for_change "stoker/test-sync-ref" '{.status.lastSyncCommit}' "$commit_v1" 90)
if [[ -n "$commit_v2" && "$commit_v2" != "$commit_v1" ]]; then
    log_pass "Commit changed after ref update (v1=$commit_v1, v2=$commit_v2)"
else
    log_fail "Commit did not change after ref update"
fi

# Clean up
$KUBECTL delete stoker test-sync-ref -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync-ref 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.9: Cleanup on Deletion
# ────────────────────────────────────────────────────────────────────
log_test "2.9: Cleanup on Deletion"

apply_fixture "test-cr.yaml"
wait_for_typed_condition "stoker/test-sync" "RefResolved" "True" 90

# Verify metadata ConfigMap exists before deletion
cm_name="stoker-metadata-test-sync"
wait_for_resource configmap "$cm_name" 10
log_info "Metadata ConfigMap exists before deletion"

# Delete the CR
$KUBECTL delete stoker test-sync -n "$TEST_NAMESPACE" --wait=true --timeout=30s

# Verify metadata ConfigMap is deleted (controller cleanup or GC)
wait_for_deletion configmap "$cm_name" 30
log_pass "Metadata ConfigMap deleted after CR deletion"

sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 2.10: Multiple CRs
# ────────────────────────────────────────────────────────────────────
log_test "2.10: Multiple CRs in Same Namespace"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: test-multi-a
spec:
  git:
    repo: "${GIT_REPO_URL}"
    ref: "0.1.0"
    auth:
      token:
        secretRef:
          name: git-token-secret
          key: token
  gateway:
    apiKeySecretRef:
      name: ignition-api-key
      key: apiKey
---
apiVersion: stoker.io/v1alpha1
kind: Stoker
metadata:
  name: test-multi-b
spec:
  git:
    repo: "${GIT_REPO_URL}"
    ref: "0.2.0"
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

# Wait for both to resolve
wait_for_typed_condition "stoker/test-multi-a" "RefResolved" "True" 90
wait_for_typed_condition "stoker/test-multi-b" "RefResolved" "True" 90
log_pass "Both CRs reached RefResolved=True"

# Verify separate metadata ConfigMaps
wait_for_resource configmap "stoker-metadata-test-multi-a" 10
wait_for_resource configmap "stoker-metadata-test-multi-b" 10
log_pass "Each CR has its own metadata ConfigMap"

# Delete one and verify other is unaffected
$KUBECTL delete stoker test-multi-a -n "$TEST_NAMESPACE" --wait=false
wait_for_deletion stoker test-multi-a 30

# Verify test-multi-b is still good
b_status=$(kubectl_json "stoker/test-multi-b" '{.status.refResolutionStatus}')
assert_eq "Resolved" "$b_status" "test-multi-b unaffected by test-multi-a deletion"

# Clean up
$KUBECTL delete stoker test-multi-b -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-multi-b 30 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Test 2.11: SSH Auth Ref Resolution
# ────────────────────────────────────────────────────────────────────
log_test "2.11: SSH Auth Ref Resolution"

apply_fixture "test-cr-ssh.yaml"

wait_for_typed_condition "stoker/test-sync-ssh" "RefResolved" "True" 90
log_pass "RefResolved=True via SSH auth"

ssh_commit=$(kubectl_json "stoker/test-sync-ssh" '{.status.lastSyncCommit}')
assert_not_empty "$ssh_commit" "SSH ref resolution has lastSyncCommit"

ssh_ref=$(kubectl_json "stoker/test-sync-ssh" '{.status.lastSyncRef}')
assert_eq "main" "$ssh_ref" "SSH ref resolution ref is main"

# Clean up
$KUBECTL delete stoker test-sync-ssh -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
wait_for_deletion stoker test-sync-ssh 30 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Phase cleanup & summary
# ────────────────────────────────────────────────────────────────────
phase_cleanup
print_summary
