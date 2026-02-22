#!/usr/bin/env bash
# Verify an Ignition gateway's configuration via REST API.
# Usage: verify-gateway.sh <base-url> <gateway-name> <project-name> <cobranding-color>
set -euo pipefail

BASE_URL="${1:?Usage: verify-gateway.sh <base-url> <gateway-name> <project-name> <cobranding-color>}"
EXPECTED_NAME="${2:?Missing gateway name (e.g. ignition-blue)}"
EXPECTED_PROJECT="${3:?Missing project name (e.g. blue)}"
EXPECTED_COLOR="${4:?Missing cobranding color (e.g. #00a3d7)}"
API_TOKEN="${API_TOKEN:-ignition-api-key:<your-api-token>}"

PASS=0; FAIL=0
check() {
  local label="$1" actual="$2" expected="$3"
  if [ "$actual" = "$expected" ]; then
    echo "  PASS  $label: $actual"
    PASS=$((PASS + 1))
  else
    echo "  FAIL  $label: got '$actual', expected '$expected'"
    FAIL=$((FAIL + 1))
  fi
}

api() { curl -sf -H "X-Ignition-API-Token: $API_TOKEN" "$BASE_URL$1"; }

echo "=== Verifying $EXPECTED_NAME at $BASE_URL ==="

# Phase 1: Gateway Identity
echo "-- Phase 1: Gateway Identity --"
info=$(api "/data/api/v1/gateway-info")
check "name"           "$(echo "$info" | jq -r .name)"            "$EXPECTED_NAME"
check "deploymentMode" "$(echo "$info" | jq -r .deploymentMode)"  "dev"
check "version"        "$(echo "$info" | jq -r '.ignitionVersion | split(" ") | .[0]')" "8.3.3"

# Phase 2: Projects
echo "-- Phase 2: Projects --"
projects=$(api "/data/api/v1/projects/list")
proj_name=$(echo "$projects" | jq -r ".items[] | select(.name==\"$EXPECTED_PROJECT\") | .name")
proj_enabled=$(echo "$projects" | jq -r ".items[] | select(.name==\"$EXPECTED_PROJECT\") | .enabled")
check "project exists"  "$proj_name"    "$EXPECTED_PROJECT"
check "project enabled" "$proj_enabled" "true"

# Phase 3: Cobranding
echo "-- Phase 3: Cobranding --"
cobranding=$(api "/data/api/v1/resources/singleton/ignition/cobranding")
check "backgroundColor" "$(echo "$cobranding" | jq -r .config.backgroundColor)" "$EXPECTED_COLOR"

# Phase 4: Database Connection
echo "-- Phase 4: Database Connection --"
db=$(api "/data/api/v1/resources/find/ignition/database-connection/db")
check "connectURL"  "$(echo "$db" | jq -r .config.connectURL)" "jdbc:postgresql://db:5432/db"
check "collection"  "$(echo "$db" | jq -r .collection)"        "external"

# Phase 5: API Token
echo "-- Phase 5: API Token --"
token=$(api "/data/api/v1/resources/find/ignition/api-token/ignition-api-key")
check "api-token name"    "$(echo "$token" | jq -r .name)"    "ignition-api-key"
check "api-token enabled" "$(echo "$token" | jq -r .enabled)" "true"

# Phase 6: Tag Provider
echo "-- Phase 6: Tag Provider --"
tags=$(api "/data/api/v1/resources/list/ignition/tag-provider")
check "default provider" "$(echo "$tags" | jq -r '.items[] | select(.name=="default") | .config.profile.type')" "STANDARD"
check "System provider"  "$(echo "$tags" | jq -r '.items[] | select(.name=="System") | .config.profile.type')"  "MANAGED"

# Phase 7: Collection Hierarchy
echo "-- Phase 7: Collection Hierarchy --"
sig_ext=$(api "/data/api/v1/resources/find/ignition/database-connection/db?collection=external" | jq -r .signature)
sig_core=$(api "/data/api/v1/resources/find/ignition/database-connection/db?collection=core" | jq -r .signature)
check "external sig exists" "$([ -n "$sig_ext" ] && echo "yes" || echo "no")" "yes"
check "sigs differ"         "$([ "$sig_ext" != "$sig_core" ] && echo "yes" || echo "no")" "yes"

# Phase 8: System Properties
echo "-- Phase 8: System Properties --"
sysprops=$(api "/data/api/v1/resources/singleton/ignition/system-properties")
check "systemName" "$(echo "$sysprops" | jq -r .config.systemName)" "$EXPECTED_NAME"

# Phase 9: Scan (trigger + poll for completion)
echo "-- Phase 9: Project Scan --"
scan_before=$(api "/data/api/v1/scan/projects")
check "scan idle" "$(echo "$scan_before" | jq -r .scanActive)" "false"
curl -sf -X POST -H "X-Ignition-API-Token: $API_TOKEN" "$BASE_URL/data/api/v1/scan/projects" > /dev/null
for i in $(seq 1 10); do
  scan=$(api "/data/api/v1/scan/projects")
  if [ "$(echo "$scan" | jq -r .scanActive)" = "false" ]; then break; fi
  sleep 0.5
done
ts_after=$(echo "$scan" | jq -r .lastScanTimestamp)
ts_before=$(echo "$scan_before" | jq -r .lastScanTimestamp)
check "scan completed" "$([ "$ts_after" -ge "$ts_before" ] && echo "yes" || echo "no")" "yes"

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
