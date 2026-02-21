#!/usr/bin/env bash
# Shared test library for functional tests.
# Source this file from each phase script.

set -euo pipefail

# ── Colors ──────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

# ── Counters ────────────────────────────────────────────────────────
TESTS_TOTAL=0
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0

# ── Cleanup tracking ───────────────────────────────────────────────
PORT_FORWARD_PIDS=()
TEMP_FILES=()

# ── Project root ───────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
FIXTURES_DIR="${SCRIPT_DIR}/fixtures"

# ── Defaults ───────────────────────────────────────────────────────
TEST_NAMESPACE="${TEST_NAMESPACE:-func-test}"
KIND_CLUSTER="${KIND_CLUSTER:-ignition-sync-func-test}"
KUBECTL="${KUBECTL:-kubectl}"
GIT_REPO_URL="${GIT_REPO_URL:-https://github.com/ia-eknorr/test-ignition-project.git}"
GIT_REPO_URL_SSH="${GIT_REPO_URL_SSH:-git@github.com:ia-eknorr/test-ignition-project.git}"

# ── Logging ─────────────────────────────────────────────────────────
log_phase() {
    echo -e "\n${BOLD}${BLUE}═══════════════════════════════════════════════════${RESET}"
    echo -e "${BOLD}${BLUE}  Phase: $1${RESET}"
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════${RESET}\n"
}

log_test() {
    echo -e "\n${BOLD}── Test: $1${RESET}"
    TESTS_TOTAL=$((TESTS_TOTAL + 1))
}

log_pass() {
    echo -e "  ${GREEN}✓ PASS${RESET}: $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

log_fail() {
    echo -e "  ${RED}✗ FAIL${RESET}: $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

log_skip() {
    echo -e "  ${YELLOW}⊘ SKIP${RESET}: $1"
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
}

log_info() {
    echo -e "  ${BLUE}ℹ${RESET} $1"
}

# ── Assertions ──────────────────────────────────────────────────────

assert_eq() {
    local expected="$1" actual="$2" msg="${3:-values should be equal}"
    if [[ "$expected" == "$actual" ]]; then
        log_pass "$msg (got: '$actual')"
        return 0
    else
        log_fail "$msg (expected: '$expected', got: '$actual')"
        return 1
    fi
}

assert_contains() {
    local haystack="$1" needle="$2" msg="${3:-should contain substring}"
    if [[ "$haystack" == *"$needle"* ]]; then
        log_pass "$msg"
        return 0
    else
        log_fail "$msg (expected to contain: '$needle', got: '$haystack')"
        return 1
    fi
}

assert_not_empty() {
    local value="$1" msg="${2:-value should not be empty}"
    if [[ -n "$value" ]]; then
        log_pass "$msg (got: '$value')"
        return 0
    else
        log_fail "$msg (was empty)"
        return 1
    fi
}

assert_empty() {
    local value="$1" msg="${2:-value should be empty}"
    if [[ -z "$value" ]]; then
        log_pass "$msg"
        return 0
    else
        log_fail "$msg (expected empty, got: '$value')"
        return 1
    fi
}

assert_exit_code() {
    local expected="$1" msg="${2:-exit code should match}"
    shift 2
    local actual
    set +e
    "$@" >/dev/null 2>&1
    actual=$?
    set -e
    if [[ "$expected" == "$actual" ]]; then
        log_pass "$msg (exit code: $actual)"
        return 0
    else
        log_fail "$msg (expected exit code: $expected, got: $actual)"
        return 1
    fi
}

# ── Wait helpers ────────────────────────────────────────────────────

# Wait for a jsonpath condition on a resource.
# Usage: wait_for_condition <resource> <jsonpath> <expected> [timeout_seconds]
wait_for_condition() {
    local resource="$1" jsonpath="$2" expected="$3" timeout="${4:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get "$resource" -n "$TEST_NAMESPACE" -o jsonpath="$jsonpath" 2>/dev/null || echo "")
        if [[ "$actual" == "$expected" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $resource $jsonpath == $expected (last: $actual)" >&2
    return 1
}

# Wait for a condition type on a resource to have a given status.
# Usage: wait_for_typed_condition <resource> <conditionType> <conditionStatus> [timeout_seconds]
wait_for_typed_condition() {
    local resource="$1" ctype="$2" cstatus="$3" timeout="${4:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get "$resource" -n "$TEST_NAMESPACE" \
            -o jsonpath="{.status.conditions[?(@.type==\"${ctype}\")].status}" 2>/dev/null || echo "")
        if [[ "$actual" == "$cstatus" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $resource condition $ctype == $cstatus (last: $actual)" >&2
    return 1
}

# Wait for a condition reason on a resource.
# Usage: wait_for_condition_reason <resource> <conditionType> <reason> [timeout_seconds]
wait_for_condition_reason() {
    local resource="$1" ctype="$2" reason="$3" timeout="${4:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get "$resource" -n "$TEST_NAMESPACE" \
            -o jsonpath="{.status.conditions[?(@.type==\"${ctype}\")].reason}" 2>/dev/null || echo "")
        if [[ "$actual" == "$reason" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $resource condition $ctype reason=$reason (last: $actual)" >&2
    return 1
}

# Wait for pod to reach a given phase.
# Usage: wait_for_pod_phase <pod-selector> <phase> [timeout_seconds]
wait_for_pod_phase() {
    local selector="$1" phase="$2" timeout="${3:-60}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get pods -n "$TEST_NAMESPACE" -l "$selector" \
            -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
        if [[ "$actual" == "$phase" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for pod ($selector) phase=$phase (last: $actual)" >&2
    return 1
}

# Wait for a pod by name to reach a given phase.
wait_for_named_pod_phase() {
    local pod_name="$1" phase="$2" timeout="${3:-60}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get pod "$pod_name" -n "$TEST_NAMESPACE" \
            -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
        if [[ "$actual" == "$phase" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for pod $pod_name phase=$phase (last: $actual)" >&2
    return 1
}

# Wait for a resource to exist.
# Usage: wait_for_resource <resource-type> <name> [timeout_seconds]
wait_for_resource() {
    local rtype="$1" name="$2" timeout="${3:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        if $KUBECTL get "$rtype" "$name" -n "$TEST_NAMESPACE" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $rtype/$name to exist" >&2
    return 1
}

# Wait for a cluster-scoped resource to exist.
wait_for_cluster_resource() {
    local rtype="$1" name="$2" timeout="${3:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        if $KUBECTL get "$rtype" "$name" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for cluster $rtype/$name to exist" >&2
    return 1
}

# Wait for a resource to be deleted.
# Usage: wait_for_deletion <resource-type> <name> [timeout_seconds]
wait_for_deletion() {
    local rtype="$1" name="$2" timeout="${3:-30}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        if ! $KUBECTL get "$rtype" "$name" -n "$TEST_NAMESPACE" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $rtype/$name to be deleted" >&2
    return 1
}

# Wait for a jsonpath value to change from a known value.
# Usage: wait_for_change <resource> <jsonpath> <old_value> [timeout_seconds]
wait_for_change() {
    local resource="$1" jsonpath="$2" old_value="$3" timeout="${4:-60}"
    local deadline=$((SECONDS + timeout))
    while [[ $SECONDS -lt $deadline ]]; do
        local actual
        actual=$($KUBECTL get "$resource" -n "$TEST_NAMESPACE" -o jsonpath="$jsonpath" 2>/dev/null || echo "")
        if [[ -n "$actual" && "$actual" != "$old_value" ]]; then
            echo "$actual"
            return 0
        fi
        sleep 2
    done
    echo "Timed out waiting for $resource $jsonpath to change from $old_value" >&2
    return 1
}

# ── kubectl helpers ─────────────────────────────────────────────────

# Get a jsonpath value from a resource.
kubectl_json() {
    local resource="$1" jsonpath="$2"
    $KUBECTL get "$resource" -n "$TEST_NAMESPACE" -o jsonpath="$jsonpath" 2>/dev/null || echo ""
}

# Get a field via jq from resource JSON.
kubectl_jq() {
    local resource="$1" jq_filter="$2"
    $KUBECTL get "$resource" -n "$TEST_NAMESPACE" -o json 2>/dev/null | jq -r "$jq_filter" 2>/dev/null || echo ""
}

# ── Namespace management ────────────────────────────────────────────

setup_namespace() {
    local ns="${1:-$TEST_NAMESPACE}"
    if ! $KUBECTL get namespace "$ns" >/dev/null 2>&1; then
        $KUBECTL create namespace "$ns"
    fi
}

cleanup_namespace() {
    local ns="${1:-$TEST_NAMESPACE}"
    if $KUBECTL get namespace "$ns" >/dev/null 2>&1; then
        $KUBECTL delete namespace "$ns" --wait=false 2>/dev/null || true
        local deadline=$((SECONDS + 60))
        while [[ $SECONDS -lt $deadline ]]; do
            if ! $KUBECTL get namespace "$ns" >/dev/null 2>&1; then
                return 0
            fi
            sleep 2
        done
        echo "Warning: namespace $ns not fully deleted within 60s" >&2
    fi
}

# ── Fixture management ──────────────────────────────────────────────

# Apply a fixture YAML with variable substitution.
# Replaces ${NAMESPACE}, ${GIT_REPO_URL}, ${GIT_SERVER_HOST} in the template.
apply_fixture() {
    local fixture="$1"
    shift
    local filepath="${FIXTURES_DIR}/${fixture}"
    if [[ ! -f "$filepath" ]]; then
        echo "Fixture not found: $filepath" >&2
        return 1
    fi
    sed \
        -e "s|\${NAMESPACE}|${TEST_NAMESPACE}|g" \
        -e "s|\${GIT_REPO_URL}|${GIT_REPO_URL}|g" \
        -e "s|\${GIT_REPO_URL_SSH}|${GIT_REPO_URL_SSH}|g" \
        "$filepath" | $KUBECTL apply -n "$TEST_NAMESPACE" -f - "$@"
}

# Delete a fixture's resources.
delete_fixture() {
    local fixture="$1"
    local filepath="${FIXTURES_DIR}/${fixture}"
    if [[ ! -f "$filepath" ]]; then
        return 0
    fi
    sed \
        -e "s|\${NAMESPACE}|${TEST_NAMESPACE}|g" \
        -e "s|\${GIT_REPO_URL}|${GIT_REPO_URL}|g" \
        -e "s|\${GIT_REPO_URL_SSH}|${GIT_REPO_URL_SSH}|g" \
        "$filepath" | $KUBECTL delete -n "$TEST_NAMESPACE" --ignore-not-found -f - 2>/dev/null || true
}

# ── Port-forward ────────────────────────────────────────────────────

# Start a port-forward in the background.
# Usage: port_forward_bg <resource> <local_port:remote_port>
# Returns the local port.
port_forward_bg() {
    local resource="$1" ports="$2" ns="${3:-$TEST_NAMESPACE}"
    $KUBECTL port-forward "$resource" -n "$ns" "$ports" >/dev/null 2>&1 &
    local pid=$!
    PORT_FORWARD_PIDS+=("$pid")
    sleep 2  # Give port-forward time to establish
    echo "$pid"
}

# ── Phase cleanup ───────────────────────────────────────────────────

# Clean all test CRs, pods, configmaps, secrets created during a phase.
# Leaves infrastructure (git server, controller) intact.
phase_cleanup() {
    log_info "Cleaning up test resources..."
    # Delete all IgnitionSync CRs in test namespace
    $KUBECTL delete ignitionsyncs --all -n "$TEST_NAMESPACE" --wait=false 2>/dev/null || true
    # Wait for CRs to be gone (finalizers need to run)
    local deadline=$((SECONDS + 30))
    while [[ $SECONDS -lt $deadline ]]; do
        local count
        count=$($KUBECTL get ignitionsyncs -n "$TEST_NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d ' ')
        if [[ "$count" == "0" ]]; then
            break
        fi
        sleep 2
    done
    # Clean up remaining test resources
    $KUBECTL delete pods -n "$TEST_NAMESPACE" -l app=gateway-test --ignore-not-found 2>/dev/null || true
    $KUBECTL delete configmaps -n "$TEST_NAMESPACE" -l ignition-sync.io/cr-name --ignore-not-found 2>/dev/null || true
    $KUBECTL delete secrets -n "$TEST_NAMESPACE" -l app=func-test --ignore-not-found 2>/dev/null || true
    sleep 2
}

# ── Trap-based cleanup ──────────────────────────────────────────────

_cleanup_on_exit() {
    # Kill port-forwards
    for pid in "${PORT_FORWARD_PIDS[@]:-}"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    # Remove temp files
    for f in "${TEMP_FILES[@]:-}"; do
        if [[ -n "$f" && -f "$f" ]]; then
            rm -f "$f"
        fi
    done
}

trap _cleanup_on_exit EXIT

# ── Summary ─────────────────────────────────────────────────────────

print_summary() {
    echo ""
    echo -e "${BOLD}═══════════════════════════════════════════════════${RESET}"
    echo -e "${BOLD}  Test Summary${RESET}"
    echo -e "${BOLD}═══════════════════════════════════════════════════${RESET}"
    echo -e "  Total:   ${TESTS_TOTAL}"
    echo -e "  ${GREEN}Passed:  ${TESTS_PASSED}${RESET}"
    echo -e "  ${RED}Failed:  ${TESTS_FAILED}${RESET}"
    echo -e "  ${YELLOW}Skipped: ${TESTS_SKIPPED}${RESET}"
    echo -e "${BOLD}═══════════════════════════════════════════════════${RESET}"
    if [[ $TESTS_FAILED -gt 0 ]]; then
        echo -e "\n${RED}${BOLD}FAILED${RESET}"
        return 1
    else
        echo -e "\n${GREEN}${BOLD}ALL PASSED${RESET}"
        return 0
    fi
}

# ── HTTP helpers ────────────────────────────────────────────────────

# Send a POST request and capture status code + body.
# Usage: http_post <url> <data> [extra_curl_args...]
# Sets HTTP_STATUS and HTTP_BODY.
http_post() {
    local url="$1" data="$2"
    shift 2
    local tmpfile
    tmpfile=$(mktemp)
    TEMP_FILES+=("$tmpfile")
    HTTP_STATUS=$(curl -s -o "$tmpfile" -w '%{http_code}' \
        -X POST -H "Content-Type: application/json" \
        -d "$data" "$@" "$url" 2>/dev/null || echo "000")
    HTTP_BODY=$(cat "$tmpfile" 2>/dev/null || echo "")
    rm -f "$tmpfile"
}

# Send a POST with HMAC signature.
# Usage: http_post_hmac <url> <data> <secret>
http_post_hmac() {
    local url="$1" data="$2" secret="$3"
    local sig
    sig=$(echo -n "$data" | openssl dgst -sha256 -hmac "$secret" | awk '{print $NF}')
    http_post "$url" "$data" -H "X-Hub-Signature-256: sha256=${sig}"
}
