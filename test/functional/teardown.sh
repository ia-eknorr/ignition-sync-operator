#!/usr/bin/env bash
# Tears down the functional test environment.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/lib.sh"

KIND_CLUSTER="${KIND_CLUSTER:-stoker-func-test}"
IMG="${IMG:-stoker-operator:func-test}"

echo "=== Functional Test Teardown ==="

# 1. Delete test namespace
echo "→ Deleting test namespace '${TEST_NAMESPACE}'..."
kubectl delete namespace "${TEST_NAMESPACE}" --ignore-not-found --wait=false 2>/dev/null || true

# 2. Undeploy controller
echo "→ Undeploying controller..."
cd "${PROJECT_ROOT}"
make undeploy ignore-not-found=true 2>/dev/null || true

# 3. Uninstall CRDs
echo "→ Uninstalling CRDs..."
make uninstall ignore-not-found=true 2>/dev/null || true

# 4. Delete kind cluster
echo "→ Deleting kind cluster '${KIND_CLUSTER}'..."
kind delete cluster --name "${KIND_CLUSTER}" 2>/dev/null || true

echo "=== Teardown Complete ==="
