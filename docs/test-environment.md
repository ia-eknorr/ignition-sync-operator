# Test Environment Reference

This document covers the Ignition test environment used for validating the ignition-sync-operator: gateway access, API authentication, repository structure, and a verification plan using the Ignition 8.3 REST API.

---

## 1. Test Repository

- **GitHub:** `ia-eknorr/test-ignition-project` (private)
- **Local clone:** `/Users/eknorr/IA/code/personal/test-ignition-project/`
- **Structure:** 2 services (`ignition-blue`, `ignition-red`), shared config, PostgreSQL
- **Commit history:** 6 commits, 4 PRs merged

## 2. Gateway Access

| Gateway | URL | Cobranding Color |
|---------|-----|-----------------|
| Blue | `http://ignition-blue.localtest.me` | `#00a3d7` |
| Red | `http://ignition-red.localtest.me` | `#ff4013` |

- Both run **Ignition 8.3.3** in `dev` deployment mode
- Docker Compose with Traefik proxy on external `proxy` network
- `localtest.me` resolves to `127.0.0.1` — no `/etc/hosts` changes needed

## 3. API Authentication

API key stored in operator repo at `secrets/ignition-api-key.txt`.

**Header:**
```
X-Ignition-API-Token: ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY
```

- Both gateways share the same API key (same `tokenHash` in config)
- Security levels: `apiKeys` added to access, read, and write permissions

**Shell variable for curl:**
```bash
TOKEN_HEADER="X-Ignition-API-Token: ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY"
```

## 4. Configuration Hierarchy

The test project uses Ignition's config collection system with three layers:

| Collection | Scope | Purpose |
|-----------|-------|---------|
| `external` | Shared across gateways | Database connections, factory config |
| `core` | Per-gateway | Cobranding, system properties, tags, API token, security |
| `dev` | Development overlay | Inherits `core`, overrides db connection, historian, tag-provider |

**Load order:** `external → core → dev` (dev wins when deployment mode = `dev`)

## 5. What's Deployed Per Gateway

### Both Gateways (via shared/external)

- Database connection `db` → `jdbc:postgresql://db:5432/db` (primary collection: `external`)
- Factory config → `shared/files/factory.json` (2 lines, 2 shifts)

### ignition-blue (core)

- Cobranding: backgroundColor `#00a3d7`, homepage notes "Ignition Blue"
- System name: `ignition-blue`
- API token: `ignition-api-key`
- Project: `blue` (Perspective project)

### ignition-red (core)

- Cobranding: backgroundColor `#ff4013`, homepage notes "Ignition Red"
- System name: `ignition-red`
- API token: `ignition-api-key`
- Project: `red` (Perspective project)

## 6. API Verification Plan

Use these API calls to confirm that file sync is working correctly. Each phase validates a different aspect of the configuration.

All examples use environment variables for portability across environments (Docker Compose, kind, etc.):

```bash
# Docker Compose (localtest.me)
export BLUE_URL="http://ignition-blue.localtest.me"
export RED_URL="http://ignition-red.localtest.me"

# kind cluster (port-forwarded)
export BLUE_URL="http://localhost:8088"
export RED_URL="http://localhost:8089"

# API token (same for both gateways)
export API_TOKEN="ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY"
```

### Expected Outcomes

Per-gateway expected values:

| Field | jq Path | Blue | Red |
|-------|---------|------|-----|
| Gateway name | `.name` | `ignition-blue` | `ignition-red` |
| Deployment mode | `.deploymentMode` | `dev` | `dev` |
| Ignition version | `.ignitionVersion` | `8.3.3 (b...)` | `8.3.3 (b...)` |
| Project name | `.items[].name` | `blue` | `red` |
| Cobranding color | `.config.backgroundColor` | `#00a3d7` | `#ff4013` |
| System name | `.config.systemName` | `ignition-blue` | `ignition-red` |
| Homepage notes | `.config.homepageNotes` | `Ignition Blue - Dev` | `Ignition Red - Dev` |

Shared expected values (both gateways):

| Field | jq Path | Expected |
|-------|---------|----------|
| DB connect URL | `.config.connectURL` | `jdbc:postgresql://db:5432/db` |
| DB primary collection | `.collection` | `external` |
| DB collections | `.collections` | `["external","core","dev"]` |
| API token name | `.name` | `ignition-api-key` |
| API token enabled | `.enabled` | `true` |
| Tag provider "default" type | `.config.profile.type` | `STANDARD` |
| Tag provider "System" type | `.config.profile.type` | `MANAGED` |
| Scan idle before trigger | `.scanActive` | `false` |
| Scan completes after POST | `.scanActive` returns to `false` | `lastScanTimestamp` advances |

### Phase 1: Gateway Identity

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/gateway-info" | jq '{name, deploymentMode, ignitionVersion}'
```

**Expected:** `name` matches the gateway service name, `deploymentMode` = `dev`, `ignitionVersion` starts with `8.3.3`

### Phase 2: Project Verification

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/projects/list" | jq '[.items[] | {name, enabled}]'
```

> **Note:** `projects/list` wraps results in `{"items": [...], "metadata": {...}}`.

**Expected:** Blue has project `blue` (enabled), Red has project `red` (enabled)

### Phase 3: Cobranding (Per-Gateway Uniqueness)

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/singleton/ignition/cobranding" | jq -r .config.backgroundColor
```

**Expected:** Blue = `#00a3d7`, Red = `#ff4013`. Confirms each gateway loaded its own core config.

### Phase 4: External/Shared Resources

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/find/ignition/database-connection/db" | jq '{url: .config.connectURL, collection, collections}'
```

**Expected:** `connectURL` = `jdbc:postgresql://db:5432/db`, `collection` = `external`, `collections` = `["external","core","dev"]`

### Phase 5: API Token Verification

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/find/ignition/api-token/ignition-api-key" | jq '{name, enabled}'
```

**Expected:** `name` = `ignition-api-key`, `enabled` = `true`

### Phase 6: Tag Provider

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/list/ignition/tag-provider" | jq '[.items[] | {name, type: .config.profile.type}]'
```

> **Note:** `resources/list` wraps results in `{"items": [...], "metadata": {...}}`. Provider type is at `.config.profile.type`.

**Expected:** `default` (STANDARD) and `System` (MANAGED)

### Phase 7: Config Collection Hierarchy

```bash
# Resource from external collection
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/find/ignition/database-connection/db?collection=external" | jq '{name, collection, signature}'

# Same resource from core collection (different config version)
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/find/ignition/database-connection/db?collection=core" | jq '{name, collection, signature}'
```

**Expected:** Both return the `db` resource, but with different `collection` values and **different signatures** — proving collection-level config resolution works.

### Phase 8: System Properties (Singleton)

```bash
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/resources/singleton/ignition/system-properties" | jq '{systemName: .config.systemName, homepageNotes: .config.homepageNotes}'
```

> **Note:** System properties is a singleton resource — use `/resources/singleton/`, not `/resources/find/`.

**Expected:** Blue = `ignition-blue` / `Ignition Blue - Dev`, Red = `ignition-red` / `Ignition Red - Dev`

### Phase 9: Project Scan (Trigger + Verify)

```bash
# Check current scan status
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/scan/projects" | jq .

# Trigger a scan
curl -s -X POST -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/scan/projects" | jq .

# Poll until complete (scanActive returns to false)
curl -s -H "X-Ignition-API-Token: $API_TOKEN" "$BLUE_URL/data/api/v1/scan/projects" | jq .
```

**Response shape:**
```json
{"scanActive": false, "lastScanTimestamp": 1771144628653, "lastScanDuration": 1}
```

**Expected:** POST triggers scan (`scanActive: true`), completes within a few seconds (`scanActive: false`), `lastScanTimestamp` advances. This is the endpoint the operator calls after syncing files to tell the gateway to reload config.

## 7. Key API Endpoints Reference

| Endpoint | Purpose |
|----------|---------|
| `GET /data/api/v1/gateway-info` | Gateway name, mode, version |
| `GET /data/api/v1/projects/list` | All projects with details |
| `GET /data/api/v1/projects/names` | Project names only |
| `GET /data/api/v1/resources/list/ignition/{type}` | All resources of a type (full config) |
| `GET /data/api/v1/resources/names/ignition/{type}` | Resource names + enabled status |
| `GET /data/api/v1/resources/find/ignition/{type}/{name}` | Single resource detail |
| `GET /data/api/v1/resources/find/ignition/{type}/{name}?collection=X` | Resource from specific collection |
| `GET /data/api/v1/resources/singleton/ignition/{type}` | Singleton resources (cobranding, system-properties) |
| `GET /data/api/v1/scan/projects` | Scan status (scanActive, lastScanTimestamp, lastScanDuration) |
| `POST /data/api/v1/scan/projects` | Trigger project scan (returns immediately, poll GET for completion) |
| `GET /data/api/v1/tags/export?provider=X&type=json` | Export tags as JSON |

**Response format notes:**
- List endpoints (`projects/list`, `resources/list`) return `{"items": [...], "metadata": {...}}`
- Find endpoints (`resources/find`) return flat resource objects
- Singleton endpoints (`resources/singleton`) return flat resource objects
- `names` endpoints (e.g., `resources/names`) may return 404 for singleton types like `system-properties`

## 8. Verification Script

A parameterized script that asserts expected outcomes against any gateway URL.

**Usage:**
```bash
# Docker Compose
./scripts/verify-gateway.sh http://ignition-blue.localtest.me ignition-blue blue "#00a3d7"
./scripts/verify-gateway.sh http://ignition-red.localtest.me  ignition-red  red  "#ff4013"

# kind (port-forwarded)
./scripts/verify-gateway.sh http://localhost:8088 ignition-blue blue "#00a3d7"
./scripts/verify-gateway.sh http://localhost:8089 ignition-red  red  "#ff4013"

# Override API token
API_TOKEN="my-token:secret" ./scripts/verify-gateway.sh http://localhost:8088 ignition-blue blue "#00a3d7"
```

**Script: `scripts/verify-gateway.sh`**

```bash
#!/usr/bin/env bash
# Verify an Ignition gateway's configuration via REST API.
# Usage: verify-gateway.sh <base-url> <gateway-name> <project-name> <cobranding-color>
set -euo pipefail

BASE_URL="${1:?Usage: verify-gateway.sh <base-url> <gateway-name> <project-name> <cobranding-color>}"
EXPECTED_NAME="${2:?Missing gateway name (e.g. ignition-blue)}"
EXPECTED_PROJECT="${3:?Missing project name (e.g. blue)}"
EXPECTED_COLOR="${4:?Missing cobranding color (e.g. #00a3d7)}"
API_TOKEN="${API_TOKEN:-ignition-api-key:CYCSdRgW6MHYkeIXhH-BMqo1oaqfTdFi8tXvHJeCKmY}"

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

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
```

## 9. Git Auth Setup (TODO)

When ready, the following auth methods will be configured for the operator to clone from `ia-eknorr/test-ignition-project`.

### SSH Deploy Key

```bash
# Generate key
ssh-keygen -t ed25519 -f secrets/deploy-key -N "" -C "ignition-sync-test"

# Add to repo
gh repo deploy-key add secrets/deploy-key.pub -R ia-eknorr/test-ignition-project

# Create K8s secret
kubectl create secret generic git-ssh-key --from-file=ssh-privatekey=secrets/deploy-key
```

### Token (Fine-Grained PAT)

- Create at: https://github.com/settings/personal-access-tokens/new
- Scope: `ia-eknorr/test-ignition-project`, Contents: read
- Store: `secrets/github-token`

```bash
kubectl create secret generic git-token-secret --from-file=token=secrets/github-token
```

### GitHub App (Future)

Not yet implemented in operator (`internal/git/auth.go` returns error).

```bash
# Store: secrets/github-app-key.pem
kubectl create secret generic github-app-key --from-file=privateKey=secrets/github-app-key.pem
```

All secrets stored in `secrets/` directory (`.gitignore` = `*` + `!.gitignore`).
