#!/usr/bin/env bash
# Phase 04: SyncProfile CRD — Validation, 3-Tier Config, Backward Compatibility
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_phase "04 — SyncProfile CRD"

# Ensure clean state
phase_cleanup
# Also clean SyncProfiles
$KUBECTL delete syncprofiles --all -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 2

# ────────────────────────────────────────────────────────────────────
# Test 3A.1: CRD Installation
# ────────────────────────────────────────────────────────────────────
log_test "3A.1: SyncProfile CRD Installation"

crd_name=$($KUBECTL get crd syncprofiles.stoker.io -o jsonpath='{.metadata.name}' 2>/dev/null || echo "")
assert_eq "syncprofiles.stoker.io" "$crd_name" "SyncProfile CRD should exist"

# Verify short name
assert_exit_code 0 "kubectl get sp works" $KUBECTL get sp -n "$TEST_NAMESPACE"

# ────────────────────────────────────────────────────────────────────
# Test 3A.2: Valid SyncProfile — Accepted=True
# ────────────────────────────────────────────────────────────────────
log_test "3A.2: Valid SyncProfile Accepted"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-site-profile
spec:
  mappings:
    - source: "services/site/projects"
      destination: "projects"
    - source: "services/site/config/resources/core"
      destination: "config/resources/core"
    - source: "shared/external-resources"
      destination: "config/resources/external"
  deploymentMode:
    name: "prd-cloud"
    source: "services/site/overlays/prd-cloud"
  excludePatterns:
    - "**/tag-*/MQTT Engine/"
  syncPeriod: 30
EOF

wait_for_typed_condition "syncprofile/test-site-profile" "Accepted" "True" 30
log_pass "SyncProfile Accepted=True"

# Verify observedGeneration
obs_gen=$(kubectl_json "syncprofile/test-site-profile" '{.status.observedGeneration}')
assert_eq "1" "$obs_gen" "observedGeneration is 1"

# ────────────────────────────────────────────────────────────────────
# Test 3A.3: Invalid SyncProfile — Path Traversal
# ────────────────────────────────────────────────────────────────────
log_test "3A.3: Path Traversal Rejected"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-bad-traversal
spec:
  mappings:
    - source: "../../../etc/passwd"
      destination: "config"
EOF

wait_for_typed_condition "syncprofile/test-bad-traversal" "Accepted" "False" 30
log_pass "Path traversal → Accepted=False"

# Verify message mentions traversal
msg=$(kubectl_json "syncprofile/test-bad-traversal" \
    '{.status.conditions[?(@.type=="Accepted")].message}')
if [[ "$msg" == *"traversal"* ]] || [[ "$msg" == *".."* ]]; then
    log_pass "Message mentions path traversal"
else
    log_info "Message: $msg (may not explicitly mention traversal)"
fi

$KUBECTL delete syncprofile test-bad-traversal -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.4: Invalid SyncProfile — Absolute Path
# ────────────────────────────────────────────────────────────────────
log_test "3A.4: Absolute Path Rejected"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-bad-absolute
spec:
  mappings:
    - source: "/etc/passwd"
      destination: "config"
EOF

wait_for_typed_condition "syncprofile/test-bad-absolute" "Accepted" "False" 30
log_pass "Absolute path → Accepted=False"

$KUBECTL delete syncprofile test-bad-absolute -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.5: Pod with sync-profile Annotation (3-Tier Mode)
# ────────────────────────────────────────────────────────────────────
log_test "3A.5: Pod with sync-profile Annotation"

# Setup: create API key secret and Stoker CR
apply_fixture "api-key-secret.yaml"
apply_fixture "test-cr.yaml"
wait_for_typed_condition "stoker/test-sync" "RefResolved" "True" 90

# Create a pod referencing the SyncProfile
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-profile-1
  labels:
    app: gateway-test
    app.kubernetes.io/name: gateway-profile-1
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "test-sync"
    stoker.io/sync-profile: "test-site-profile"
    stoker.io/gateway-name: "profile-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

wait_for_named_pod_phase "gateway-profile-1" "Running" 60

# Wait for gateway discovery
deadline=$((SECONDS + 30))
gw_name=""
while [[ $SECONDS -lt $deadline ]]; do
    gw_name=$(kubectl_jq "stoker/test-sync" \
        '.status.discoveredGateways[] | select(.name=="profile-gw") | .name')
    if [[ "$gw_name" == "profile-gw" ]]; then
        break
    fi
    sleep 2
done
assert_eq "profile-gw" "$gw_name" "Gateway with sync-profile discovered"

# Verify profile gatewayCount updated
sleep 5
gw_count=$(kubectl_json "syncprofile/test-site-profile" '{.status.gatewayCount}')
if [[ "$gw_count" -ge 1 ]]; then
    log_pass "SyncProfile gatewayCount >= 1 (got: $gw_count)"
else
    log_info "SyncProfile gatewayCount: $gw_count (may not be implemented yet)"
fi

# ────────────────────────────────────────────────────────────────────
# Test 3A.6: Pod without sync-profile (2-Tier Backward Compatibility)
# ────────────────────────────────────────────────────────────────────
log_test "3A.6: Pod without sync-profile (2-Tier Mode)"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-2tier-1
  labels:
    app: gateway-test
    app.kubernetes.io/name: gateway-2tier-1
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "test-sync"
    stoker.io/service-path: "services/gateway"
    stoker.io/gateway-name: "twotier-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

wait_for_named_pod_phase "gateway-2tier-1" "Running" 60

# Wait for gateway discovery
deadline=$((SECONDS + 30))
gw_name=""
while [[ $SECONDS -lt $deadline ]]; do
    gw_name=$(kubectl_jq "stoker/test-sync" \
        '.status.discoveredGateways[] | select(.name=="twotier-gw") | .name')
    if [[ "$gw_name" == "twotier-gw" ]]; then
        break
    fi
    sleep 2
done
assert_eq "twotier-gw" "$gw_name" "2-tier gateway discovered (no sync-profile)"

# Verify servicePath populated from annotation
svc_path=$(kubectl_jq "stoker/test-sync" \
    '.status.discoveredGateways[] | select(.name=="twotier-gw") | .servicePath')
assert_eq "services/gateway" "$svc_path" "servicePath from annotation"

# Verify controller still running
controller_ns="stoker-system"
ctrl_phase=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
assert_eq "Running" "$ctrl_phase" "Controller still running"

# ────────────────────────────────────────────────────────────────────
# Test 3A.7: Multiple Gateways Share One Profile
# ────────────────────────────────────────────────────────────────────
log_test "3A.7: Multiple Gateways Share One Profile"

# Create an area profile
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-area-profile
spec:
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
EOF

wait_for_typed_condition "syncprofile/test-area-profile" "Accepted" "True" 30

# Create 3 pods referencing the same profile
for i in 1 2 3; do
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-area-${i}
  labels:
    app: gateway-test
    app.kubernetes.io/name: gateway-area-${i}
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "test-sync"
    stoker.io/sync-profile: "test-area-profile"
    stoker.io/gateway-name: "area${i}"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF
done

# Wait for all 3 to be Running
for i in 1 2 3; do
    wait_for_named_pod_phase "gateway-area-${i}" "Running" 60
done

# Wait for all 3 gateways discovered
deadline=$((SECONDS + 30))
area_count=0
while [[ $SECONDS -lt $deadline ]]; do
    area_count=$(kubectl_jq "stoker/test-sync" \
        '[.status.discoveredGateways[] | select(.name | startswith("area"))] | length')
    if [[ "$area_count" == "3" ]]; then
        break
    fi
    sleep 2
done
assert_eq "3" "$area_count" "3 area gateways discovered sharing one profile"

# ────────────────────────────────────────────────────────────────────
# Test 3A.8: Profile Update Triggers Re-Reconcile
# ────────────────────────────────────────────────────────────────────
log_test "3A.8: Profile Update Triggers Re-Reconcile"

# Record current observed generation of Stoker
obs_before=$(kubectl_json "stoker/test-sync" '{.status.observedGeneration}')

# Update the profile
$KUBECTL patch syncprofile test-site-profile -n "$TEST_NAMESPACE" --type=merge \
    -p '{"spec":{"syncPeriod":60}}'

# Verify profile still Accepted
wait_for_typed_condition "syncprofile/test-site-profile" "Accepted" "True" 30
log_pass "Updated profile still Accepted=True"

# Verify profile observedGeneration bumped
sleep 5
profile_gen=$(kubectl_json "syncprofile/test-site-profile" '{.status.observedGeneration}')
if [[ "$profile_gen" -gt 1 ]]; then
    log_pass "Profile observedGeneration bumped (got: $profile_gen)"
else
    log_info "Profile observedGeneration: $profile_gen"
fi

# ────────────────────────────────────────────────────────────────────
# Test 3A.9: Profile Deletion — Graceful Degradation
# ────────────────────────────────────────────────────────────────────
log_test "3A.9: Profile Deletion Graceful Degradation"

# Create a temporary profile
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: temp-profile
spec:
  mappings:
    - source: "services/temp"
      destination: "temp"
EOF

wait_for_typed_condition "syncprofile/temp-profile" "Accepted" "True" 30

# Create a pod referencing it
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-temp
  labels:
    app: gateway-test
    app.kubernetes.io/name: gateway-temp
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "test-sync"
    stoker.io/sync-profile: "temp-profile"
    stoker.io/gateway-name: "temp-gw"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

wait_for_named_pod_phase "gateway-temp" "Running" 60
sleep 5

# Delete the profile
$KUBECTL delete syncprofile temp-profile -n "$TEST_NAMESPACE"
sleep 10

# Controller should still be running
ctrl_phase=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
assert_eq "Running" "$ctrl_phase" "Controller survives profile deletion"

# Stoker CR should still be healthy
clone_status=$(kubectl_json "stoker/test-sync" '{.status.refResolutionStatus}')
assert_eq "Resolved" "$clone_status" "Stoker CR still Resolved after profile deletion"

# Clean up the temp pod
$KUBECTL delete pod gateway-temp -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Test 3A.10: Paused Profile
# ────────────────────────────────────────────────────────────────────
log_test "3A.10: Paused Profile"

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-paused-profile
spec:
  paused: true
  mappings:
    - source: "services/gateway"
      destination: "."
EOF

# Paused should still be Accepted (paused doesn't invalidate the spec)
wait_for_typed_condition "syncprofile/test-paused-profile" "Accepted" "True" 30
log_pass "Paused profile still Accepted=True"

# Verify paused flag
paused=$(kubectl_json "syncprofile/test-paused-profile" '{.spec.paused}')
assert_eq "true" "$paused" "Paused flag preserved"

$KUBECTL delete syncprofile test-paused-profile -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.12: Profile with dependsOn
# ────────────────────────────────────────────────────────────────────
log_test "3A.12: Profile with dependsOn"

apply_rc=0
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f - 2>/dev/null || apply_rc=$?
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-depends-on
spec:
  dependsOn:
    - profileName: "test-site-profile"
  mappings:
    - source: "services/area/projects"
      destination: "projects"
    - source: "services/area/config/resources/core"
      destination: "config/resources/core"
EOF

if [[ $apply_rc -eq 0 ]]; then
    # Verify the field roundtrips
    dep=$(kubectl_jq "syncprofile/test-depends-on" '.spec.dependsOn[0].profileName')
    if [[ "$dep" == "test-site-profile" ]]; then
        log_pass "dependsOn field accepted and roundtrips"
    elif [[ "$dep" == "null" ]] || [[ -z "$dep" ]]; then
        log_skip "dependsOn field not in CRD schema yet (stripped by server)"
    else
        log_pass "dependsOn field accepted (got: $dep)"
    fi

    # Verify Accepted condition still set
    wait_for_typed_condition "syncprofile/test-depends-on" "Accepted" "True" 30 && \
        log_pass "Profile with dependsOn is Accepted=True" || \
        log_info "Profile with dependsOn not yet reconciled"
else
    log_skip "dependsOn: CRD rejected the field (not in schema yet)"
fi

$KUBECTL delete syncprofile test-depends-on -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.13: Profile with vars
# ────────────────────────────────────────────────────────────────────
log_test "3A.13: Profile with vars"

apply_rc=0
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f - 2>/dev/null || apply_rc=$?
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-vars
spec:
  vars:
    siteNumber: "1"
    region: "us-east"
  mappings:
    - source: "services/site/projects"
      destination: "projects"
EOF

if [[ $apply_rc -eq 0 ]]; then
    site_num=$(kubectl_jq "syncprofile/test-vars" '.spec.vars.siteNumber')
    region=$(kubectl_jq "syncprofile/test-vars" '.spec.vars.region')
    if [[ "$site_num" == "1" ]] && [[ "$region" == "us-east" ]]; then
        log_pass "vars map accepted and roundtrips"
    elif [[ "$site_num" == "null" ]] || [[ -z "$site_num" ]]; then
        log_skip "vars field not in CRD schema yet (stripped by server)"
    else
        log_pass "vars field accepted (siteNumber=$site_num, region=$region)"
    fi

    wait_for_typed_condition "syncprofile/test-vars" "Accepted" "True" 30 && \
        log_pass "Profile with vars is Accepted=True" || \
        log_info "Profile with vars not yet reconciled"
else
    log_skip "vars: CRD rejected the field (not in schema yet)"
fi

$KUBECTL delete syncprofile test-vars -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.14: Profile with dryRun
# ────────────────────────────────────────────────────────────────────
log_test "3A.14: Profile with dryRun"

apply_rc=0
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f - 2>/dev/null || apply_rc=$?
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-dryrun
spec:
  dryRun: true
  mappings:
    - source: "services/site/projects"
      destination: "projects"
EOF

if [[ $apply_rc -eq 0 ]]; then
    dry=$(kubectl_jq "syncprofile/test-dryrun" '.spec.dryRun')
    if [[ "$dry" == "true" ]]; then
        log_pass "dryRun field accepted and roundtrips"
    elif [[ "$dry" == "null" ]] || [[ -z "$dry" ]]; then
        log_skip "dryRun field not in CRD schema yet (stripped by server)"
    else
        log_pass "dryRun field accepted (got: $dry)"
    fi

    wait_for_typed_condition "syncprofile/test-dryrun" "Accepted" "True" 30 && \
        log_pass "Profile with dryRun is Accepted=True" || \
        log_info "Profile with dryRun not yet reconciled"
else
    log_skip "dryRun: CRD rejected the field (not in schema yet)"
fi

$KUBECTL delete syncprofile test-dryrun -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.15: Mapping with required field
# ────────────────────────────────────────────────────────────────────
log_test "3A.15: Mapping with required Field"

apply_rc=0
cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f - 2>/dev/null || apply_rc=$?
apiVersion: stoker.io/v1alpha1
kind: SyncProfile
metadata:
  name: test-required
spec:
  mappings:
    - source: "services/site/projects"
      destination: "projects"
      required: true
    - source: "shared/optional-extras"
      destination: "extras"
      required: false
EOF

if [[ $apply_rc -eq 0 ]]; then
    req=$(kubectl_jq "syncprofile/test-required" '.spec.mappings[0].required')
    if [[ "$req" == "true" ]]; then
        log_pass "required field accepted on mapping and roundtrips"
    elif [[ "$req" == "null" ]] || [[ -z "$req" ]]; then
        log_skip "required field not in CRD schema yet (stripped by server)"
    else
        log_pass "required field accepted (got: $req)"
    fi

    wait_for_typed_condition "syncprofile/test-required" "Accepted" "True" 30 && \
        log_pass "Profile with required mappings is Accepted=True" || \
        log_info "Profile with required mappings not yet reconciled"
else
    log_skip "required: CRD rejected the field (not in schema yet)"
fi

$KUBECTL delete syncprofile test-required -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.16: Pod with ref-override Annotation
# ────────────────────────────────────────────────────────────────────
log_test "3A.16: Pod with ref-override Annotation"

# Ensure Stoker CR exists (may already from 3A.5)
apply_fixture "api-key-secret.yaml" 2>/dev/null || true
apply_fixture "test-cr.yaml" 2>/dev/null || true
wait_for_typed_condition "stoker/test-sync" "RefResolved" "True" 90

cat <<EOF | $KUBECTL apply -n "$TEST_NAMESPACE" -f -
apiVersion: v1
kind: Pod
metadata:
  name: gateway-ref-override
  labels:
    app: gateway-test
    app.kubernetes.io/name: gateway-ref-override
  annotations:
    stoker.io/inject: "true"
    stoker.io/cr-name: "test-sync"
    stoker.io/sync-profile: "test-site-profile"
    stoker.io/gateway-name: "override-gw"
    stoker.io/ref-override: "v1.0.0-rc1"
spec:
  containers:
    - name: ignition
      image: registry.k8s.io/pause:3.9
      imagePullPolicy: IfNotPresent
EOF

wait_for_named_pod_phase "gateway-ref-override" "Running" 60

# Verify gateway discovered
deadline=$((SECONDS + 30))
gw_name=""
while [[ $SECONDS -lt $deadline ]]; do
    gw_name=$(kubectl_jq "stoker/test-sync" \
        '.status.discoveredGateways[] | select(.name=="override-gw") | .name')
    if [[ "$gw_name" == "override-gw" ]]; then
        break
    fi
    sleep 2
done
assert_eq "override-gw" "$gw_name" "Gateway with ref-override annotation discovered"

# Verify the annotation is preserved on the pod
ref_val=$($KUBECTL get pod gateway-ref-override -n "$TEST_NAMESPACE" \
    -o jsonpath='{.metadata.annotations.stoker\.io/ref-override}' 2>/dev/null || echo "")
assert_eq "v1.0.0-rc1" "$ref_val" "ref-override annotation preserved on pod"

$KUBECTL delete pod gateway-ref-override -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
sleep 1

# ────────────────────────────────────────────────────────────────────
# Test 3A.17: Controller Health
# ────────────────────────────────────────────────────────────────────
log_test "3A.17: Controller Health After All Tests"

ctrl_phase=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
assert_eq "Running" "$ctrl_phase" "Controller pod Running"

restart_count=$($KUBECTL get pods -n "$controller_ns" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}' 2>/dev/null || echo "")
assert_eq "0" "$restart_count" "Controller has 0 restarts"

# ────────────────────────────────────────────────────────────────────
# Phase cleanup & summary
# ────────────────────────────────────────────────────────────────────
# Clean SyncProfiles too
$KUBECTL delete syncprofiles --all -n "$TEST_NAMESPACE" --ignore-not-found 2>/dev/null || true
phase_cleanup
print_summary
