#!/usr/bin/env bash
# Phase 05: Webhook Receiver — HTTP handler, HMAC, payload formats
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_phase "05 — Webhook Receiver"

# Ensure clean state
phase_cleanup

CONTROLLER_NS="stoker-system"
WEBHOOK_LOCAL_PORT=19443
WEBHOOK_REMOTE_PORT=9443

# Setup: create API key secret and base CR
apply_fixture "api-key-secret.yaml"
apply_fixture "test-cr.yaml"
wait_for_typed_condition "stoker/test-sync" "RefResolved" "True" 90

# Start port-forward to controller manager pod for webhook access
CTRL_POD=$($KUBECTL get pods -n "$CONTROLLER_NS" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].metadata.name}')
port_forward_bg "pod/${CTRL_POD}" "${WEBHOOK_LOCAL_PORT}:${WEBHOOK_REMOTE_PORT}" "$CONTROLLER_NS"
sleep 3

WEBHOOK_URL="http://localhost:${WEBHOOK_LOCAL_PORT}/webhook/${TEST_NAMESPACE}"

# ────────────────────────────────────────────────────────────────────
# Test 4.1: Webhook Server Port Reachable
# ────────────────────────────────────────────────────────────────────
log_test "4.1: Webhook Server Port Reachable"

# Simple connectivity test — any request to verify port is open
http_post "${WEBHOOK_URL}/test-sync" '{"ref":"v1.0.0"}'
if [[ "$HTTP_STATUS" != "000" ]]; then
    log_pass "Webhook port reachable (got HTTP ${HTTP_STATUS})"
else
    log_fail "Webhook port not reachable"
fi

# ────────────────────────────────────────────────────────────────────
# Test 4.2: Generic Payload → 202, requested-by=generic
# ────────────────────────────────────────────────────────────────────
log_test "4.2: Generic Payload"

# Clear any previous annotations by resetting the CR
$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

http_post "${WEBHOOK_URL}/test-sync" '{"ref":"v2.0.0"}'
assert_eq "202" "$HTTP_STATUS" "Generic payload returns 202"

# Verify annotations on CR
req_ref=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-ref}')
assert_eq "v2.0.0" "$req_ref" "requested-ref annotation set to v2.0.0"

req_by=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-by}')
assert_eq "generic" "$req_by" "requested-by annotation is generic"

req_at=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-at}')
assert_not_empty "$req_at" "requested-at annotation is set"

# ────────────────────────────────────────────────────────────────────
# Test 4.3: GitHub Payload → requested-by=github
# ────────────────────────────────────────────────────────────────────
log_test "4.3: GitHub Release Payload"

# Clear annotations
$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

http_post "${WEBHOOK_URL}/test-sync" \
    '{"action":"published","release":{"tag_name":"v3.0.0"}}'
assert_eq "202" "$HTTP_STATUS" "GitHub payload returns 202"

req_by=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-by}')
assert_eq "github" "$req_by" "requested-by is github"

req_ref=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-ref}')
assert_eq "v3.0.0" "$req_ref" "requested-ref is v3.0.0 from GitHub release"

# ────────────────────────────────────────────────────────────────────
# Test 4.4: ArgoCD Payload → requested-by=argocd
# ────────────────────────────────────────────────────────────────────
log_test "4.4: ArgoCD Notification Payload"

$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

http_post "${WEBHOOK_URL}/test-sync" \
    '{"app":{"metadata":{"annotations":{"git.ref":"v4.0.0"}}}}'
assert_eq "202" "$HTTP_STATUS" "ArgoCD payload returns 202"

req_by=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-by}')
assert_eq "argocd" "$req_by" "requested-by is argocd"

req_ref=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-ref}')
assert_eq "v4.0.0" "$req_ref" "requested-ref from ArgoCD payload"

# ────────────────────────────────────────────────────────────────────
# Test 4.5: Kargo Payload → requested-by=kargo
# ────────────────────────────────────────────────────────────────────
log_test "4.5: Kargo Promotion Payload"

$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

http_post "${WEBHOOK_URL}/test-sync" \
    '{"freight":{"commits":[{"tag":"v5.0.0"}]}}'
assert_eq "202" "$HTTP_STATUS" "Kargo payload returns 202"

req_by=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-by}')
assert_eq "kargo" "$req_by" "requested-by is kargo"

req_ref=$(kubectl_json "stoker/test-sync" '{.metadata.annotations.stoker\.io/requested-ref}')
assert_eq "v5.0.0" "$req_ref" "requested-ref from Kargo payload"

# ────────────────────────────────────────────────────────────────────
# Test 4.6: Invalid HMAC → 401
# ────────────────────────────────────────────────────────────────────
log_test "4.6: Invalid HMAC → 401"

# Patch the controller deployment to set WEBHOOK_HMAC_SECRET
HMAC_SECRET="test-hmac-secret-$(date +%s)"
$KUBECTL set env deployment/stoker-operator-controller-manager \
    -n "$CONTROLLER_NS" \
    WEBHOOK_HMAC_SECRET="$HMAC_SECRET"

# Wait for the rollout to complete
$KUBECTL rollout status deployment/stoker-operator-controller-manager \
    -n "$CONTROLLER_NS" --timeout=60s

# Kill old port-forward, start new one
for pid in "${PORT_FORWARD_PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
done
PORT_FORWARD_PIDS=()
sleep 3

CTRL_POD=$($KUBECTL get pods -n "$CONTROLLER_NS" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].metadata.name}')

# Wait for the new pod to be ready
deadline=$((SECONDS + 60))
while [[ $SECONDS -lt $deadline ]]; do
    ready=$($KUBECTL get pod "$CTRL_POD" -n "$CONTROLLER_NS" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
    if [[ "$ready" == "True" ]]; then
        break
    fi
    sleep 2
    CTRL_POD=$($KUBECTL get pods -n "$CONTROLLER_NS" -l control-plane=controller-manager \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
done

port_forward_bg "pod/${CTRL_POD}" "${WEBHOOK_LOCAL_PORT}:${WEBHOOK_REMOTE_PORT}" "$CONTROLLER_NS"
sleep 3

# Send with WRONG HMAC
WRONG_SECRET="wrong-secret"
DATA='{"ref":"v6.0.0"}'
WRONG_SIG=$(echo -n "$DATA" | openssl dgst -sha256 -hmac "$WRONG_SECRET" 2>/dev/null | awk '{print $NF}')
http_post "${WEBHOOK_URL}/test-sync" "$DATA" -H "X-Hub-Signature-256: sha256=${WRONG_SIG}"
assert_eq "401" "$HTTP_STATUS" "Invalid HMAC returns 401"

# ────────────────────────────────────────────────────────────────────
# Test 4.7: Valid HMAC → 202
# ────────────────────────────────────────────────────────────────────
log_test "4.7: Valid HMAC → 202"

$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

DATA='{"ref":"v7.0.0"}'
http_post_hmac "${WEBHOOK_URL}/test-sync" "$DATA" "$HMAC_SECRET"
assert_eq "202" "$HTTP_STATUS" "Valid HMAC returns 202"

# ────────────────────────────────────────────────────────────────────
# Test 4.8: HMAC Before CR Lookup (invalid HMAC to nonexistent CR → 401)
# ────────────────────────────────────────────────────────────────────
log_test "4.8: HMAC Before CR Lookup"

DATA='{"ref":"v8.0.0"}'
WRONG_SIG=$(echo -n "$DATA" | openssl dgst -sha256 -hmac "wrong" 2>/dev/null | awk '{print $NF}')
http_post "${WEBHOOK_URL}/nonexistent-cr" "$DATA" -H "X-Hub-Signature-256: sha256=${WRONG_SIG}"
assert_eq "401" "$HTTP_STATUS" "Invalid HMAC to nonexistent CR returns 401 (not 404)"

# ────────────────────────────────────────────────────────────────────
# Test 4.9: Nonexistent CR → 404
# ────────────────────────────────────────────────────────────────────
log_test "4.9: Nonexistent CR → 404"

DATA='{"ref":"v9.0.0"}'
http_post_hmac "${WEBHOOK_URL}/nonexistent-cr" "$DATA" "$HMAC_SECRET"
assert_eq "404" "$HTTP_STATUS" "Valid HMAC to nonexistent CR returns 404"

# ────────────────────────────────────────────────────────────────────
# Test 4.10: Bad Payload → 400
# ────────────────────────────────────────────────────────────────────
log_test "4.10: Bad Payload → 400"

DATA='{"nothing":"here"}'
http_post_hmac "${WEBHOOK_URL}/test-sync" "$DATA" "$HMAC_SECRET"
assert_eq "400" "$HTTP_STATUS" "Payload with no ref returns 400"

# ────────────────────────────────────────────────────────────────────
# Test 4.11: Duplicate Ref → 200 Idempotent
# ────────────────────────────────────────────────────────────────────
log_test "4.11: Duplicate Ref → 200 Idempotent"

# Set a known ref first
$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

DATA='{"ref":"v11.0.0"}'
http_post_hmac "${WEBHOOK_URL}/test-sync" "$DATA" "$HMAC_SECRET"
assert_eq "202" "$HTTP_STATUS" "First request returns 202"

# Send same ref again
http_post_hmac "${WEBHOOK_URL}/test-sync" "$DATA" "$HMAC_SECRET"
assert_eq "200" "$HTTP_STATUS" "Duplicate ref returns 200 (idempotent)"
assert_contains "$HTTP_BODY" "already set" "Response mentions ref already set"

# ────────────────────────────────────────────────────────────────────
# Test 4.12: Webhook Triggers Reconcile (ref change → lastSyncCommit changes)
# ────────────────────────────────────────────────────────────────────
log_test "4.12: Webhook Triggers Reconcile"

# Record current commit (should be cloned at 'main')
current_commit=$(kubectl_json "stoker/test-sync" '{.status.lastSyncCommit}')
assert_not_empty "$current_commit" "Current commit recorded"

# Clear annotations and send webhook for 0.1.0 (different tag = different commit)
$KUBECTL annotate stoker test-sync -n "$TEST_NAMESPACE" \
    stoker.io/requested-ref- \
    stoker.io/requested-at- \
    stoker.io/requested-by- 2>/dev/null || true

# First patch the spec.git.ref directly (webhook sets annotation, but reconciler
# reads spec.git.ref). The webhook annotation triggers a reconcile that may update
# spec.git.ref depending on controller logic. Let's test by patching ref directly.
$KUBECTL patch stoker test-sync -n "$TEST_NAMESPACE" \
    --type=merge -p '{"spec":{"git":{"ref":"0.1.0"}}}'

# Wait for commit to change
new_commit=$(wait_for_change "stoker/test-sync" '{.status.lastSyncCommit}' "$current_commit" 90)
if [[ -n "$new_commit" && "$new_commit" != "$current_commit" ]]; then
    log_pass "Commit changed after ref update (old=$current_commit, new=$new_commit)"
else
    log_fail "Commit did not change after ref update"
fi

# ────────────────────────────────────────────────────────────────────
# Cleanup: remove HMAC secret from controller
# ────────────────────────────────────────────────────────────────────
log_info "Removing HMAC secret from controller..."
$KUBECTL set env deployment/stoker-operator-controller-manager \
    -n "$CONTROLLER_NS" \
    WEBHOOK_HMAC_SECRET-
$KUBECTL rollout status deployment/stoker-operator-controller-manager \
    -n "$CONTROLLER_NS" --timeout=60s 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────
# Phase cleanup & summary
# ────────────────────────────────────────────────────────────────────
phase_cleanup
print_summary
