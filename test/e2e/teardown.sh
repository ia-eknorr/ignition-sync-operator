#!/usr/bin/env bash
# Tears down the e2e test environment.
set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER:-stoker-e2e}"

echo "=== E2E Test Teardown ==="
echo "-> Deleting kind cluster '${KIND_CLUSTER}'..."
kind delete cluster --name "${KIND_CLUSTER}"
echo "=== Teardown Complete ==="
