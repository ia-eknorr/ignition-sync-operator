<!-- Part of: Ignition Sync Operator Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 08-deployment-operations.md, 09-security-testing-roadmap.md, 10-enterprise-examples.md -->

# Ignition Sync Operator — Sync Agent & Ignition-Aware Sync

## Sync Agent

### Image & Implementation Strategy

```dockerfile
# Build stage
FROM golang:1.23 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o sync-agent ./cmd/agent/

# Production image — distroless for minimal attack surface (~20MB)
FROM gcr.io/distroless/static-debian12:nonroot

# Primary: Go binary implements all sync logic (file sync, JSON/YAML transforms, metadata handling)
COPY --from=builder /app/sync-agent /sync-agent

ENTRYPOINT ["/sync-agent"]
```

**Why distroless over Alpine:** The agent is a Go-first binary that handles all file operations in-process. No rsync, jq, curl, or shell needed in production. Distroless eliminates the shell attack surface entirely — there's no `sh` to exec into, reducing CVE exposure. Optional shell hooks (for advanced users) require a separate Alpine-based variant image.


The agent is **Go-first**: the sync agent binary is a self-contained executable that handles:
- Git clone and fetch operations (using `github.com/go-git/go-git` or shelling out to git binary)
- File synchronization (rsync-equivalent operations using Go's `filepath` and `io` packages)
- Glob pattern matching with `**` support (using `github.com/bmatcuk/doublestar` — Go's `filepath.Match` does not support `**`)
- JSON/YAML manipulation (using Go's `encoding/json` and `gopkg.in/yaml.v2`)
- Metadata and ConfigMap-based signaling
- Health endpoints and status reporting
- Bidirectional change detection

Optional shell scripts (`/opt/ignition-sync/hooks/`) are available as an **escape hatch** for advanced users who need custom sync logic, not as the primary mechanism. This approach:
- Eliminates the operational complexity of coordinating Go + shell
- Provides better performance (no subprocess overhead for rsync/jq)
- Improves debuggability (single binary, structured logging)
- Reduces image size and attack surface

Image size: ~20MB (distroless + static Go binary). An Alpine-based variant (`-alpine` tag) is available for users who need shell hooks.

### Agent Sync Flow

#### Trigger Mechanism: K8s ConfigMap

The controller signals sync availability via ConfigMap:

- **ConfigMap Watch** — Controller writes sync metadata to a ConfigMap (`ignition-sync-metadata-{crName}`); agent uses K8s informer to watch for changes. When the controller updates the ConfigMap with a new commit SHA, the agent receives a push notification and initiates a git fetch + checkout to bring its local clone up to date.
- **Fallback: Polling Timer** — Agent polls the ConfigMap at `spec.polling.interval` (default: 60s) as a safety net in case the informer watch is disrupted.

ConfigMap is the single communication mechanism between controller and agents. The agent owns its own git clone in a local emptyDir volume — no shared storage between controller and agent. This simplifies the architecture, eliminates cross-pod volume dependencies, and keeps controller-agent communication on well-understood K8s primitives.

#### Full Sync Flow

```
Startup:
  1. Mount /repo (RW) — local emptyDir on the gateway pod (NOT a shared PVC)
  2. Mount /ignition-data (RW) — gateway's data PVC (shared with ignition container)
  3. Mount git auth secret (RO) — injected by webhook as a volume mount
  4. Read config from environment variables (set by webhook injection)
  5. Establish K8s API connection for ConfigMap watch
  6. Read metadata ConfigMap to get current commit SHA and ref
  7. Clone git repository to /repo using commit SHA from metadata ConfigMap
  8. Perform initial sync (blocking — gateway doesn't start until initial sync succeeds)
  9. Start watching metadata ConfigMap for changes (preferred) or periodic fallback timer
  10. If bidirectional: start inotify on configured watch paths
  11. Expose health endpoint on :8082

On trigger (metadata ConfigMap updated with new commit):
  1. Read ref + commit from metadata ConfigMap data
  2. Compare against last-synced commit — skip if unchanged
  3. git fetch + checkout new commit in /repo (local emptyDir)
  4. **Project-Level Granular Sync**: Compute file-level SHA256 checksums of all files under {servicePath}
     - Compare against previous sync checksums (stored in ConfigMap)
     - If no file checksums changed, skip sync entirely (fast path)
     - If some files changed, identify changed PROJECT directories (not individual files)
     - Build sync scope: only include changed projects + shared resources that changed
  5. Create staging directory: /ignition-data/.sync-staging/
  6. Sync project files:
     - Source: /repo/{servicePath}/projects/{changedProject}/
     - Dest: staging/projects/{changedProject}/
     - Exclude patterns applied (using doublestar library for ** support)
  7. Sync config/resources/core (ALWAYS — overlay depends on fresh core):
     - Source: /repo/{servicePath}/config/resources/core/
     - Dest: staging/config/resources/core/
  8. Apply deployment mode overlay ON TOP OF core (ALWAYS recomposed):
     - Source: /repo/{servicePath}/config/resources/{deploymentMode}/
     - Dest: staging/config/resources/core/   <- overlay files overwrite core files
     - NOTE: overlay is ALWAYS applied after core, even if only core changed.
       This preserves correct precedence — overlay files override core defaults.
       Skipping overlay when "unchanged" would lose overrides on core updates.
  9. Sync shared resources:
     - External resources -> staging/config/resources/external/ (if enabled)
       - If source dir doesn't exist in repo AND createFallback=true,
         create minimal dir with default config-mode.json
     - Scripts -> staging/projects/{projectName}/{destPath}/ (if enabled)
     - UDTs -> staging/config/resources/core/ignition/tag-type-definition/{tagProvider}/ (if enabled)
     - Additional files -> staging/{dest} (if enabled)
  10. **Pre-Sync Validation**:
     - JSON syntax validation on all config.json files
     - Verify no .resources/ directory included in staging
     - Checksum verification on critical files
  11. Apply exclude patterns (combine global + per-gateway, doublestar matching)
  12. Normalize configs (recursive — ALL config.json files):
      - Use filepath.Walk to discover EVERY config.json in staging
      - Apply systemName replacement to each (not just top-level)
      - Use targeted JSON patching (modify field in-place) to avoid
        reformatting the entire file (prevents false diffs from re-serialization)
      - YAML manipulation (if needed) using in-process YAML library
      - Support Go template syntax for advanced field mappings
  13. Selective merge to live directory:
      - Walk staging/, copy files to /ignition-data/
      - Delete files in /ignition-data/ that are NOT in staging
        AND NOT in protected list (.resources/)
      - .resources/ is NEVER touched — not deleted, not overwritten, not modified
      - This is NOT an atomic swap — it is a merge with protected directories
  14. **Ignition API Health Check**:
      - GET /data/api/v1/status to verify gateway is responsive
      - If not ready, wait up to 5s before proceeding
  15. Trigger Ignition scan API (SKIP on initial sync):
      - On initial sync (first sync after pod startup): SKIP scan entirely.
        Ignition auto-scans its data directory on first boot. Calling the scan
        API during startup causes race conditions (duplicate project loads,
        partial config reads). The agent tracks an `initialSyncDone` flag.
      - On subsequent syncs:
        a. POST /data/api/v1/scan/projects (fire-and-forget — returns 200 immediately)
        b. POST /data/api/v1/scan/config (fire-and-forget)
        c. Order matters: projects MUST be scanned before config
        d. HTTP retry logic: 3 retries with exponential backoff on connection failures
        e. Accept any 2xx response as success — the scan runs asynchronously
           inside the gateway. Do NOT attempt to poll for completion.
  16. Write status to ConfigMap ignition-sync-status-{crName}:
      {
        "gateway": "{gatewayName}",
        "syncedAt": "2026-02-12T10:30:00Z",
        "commit": "abc123f",
        "ref": "2.0.0",
        "filesChanged": 47,
        "projectsSynced": ["site", "area1"],
        "scanResult": "projects=200 config=200",
        "duration": "3.2s",
        "checksums": { "projects/site/...": "sha256:...", ... }
      }
  17. Clean up staging directory

On filesystem change (bidirectional):
  1. inotify detects change in /ignition-data/{watchPath}
  2. Debounce (wait for configured quiet period)
  3. Diff changed files against /repo/{servicePath}/
  4. Write change manifest to ConfigMap ignition-sync-changes-{crName}:
     {
       "gateway": "site",
       "timestamp": "2026-02-12T10:30:00Z",
       "commit": "abc123f",
       "files": [
         { "path": "projects/site/com.inductiveautomation.perspective/views/MainView/view.json",
           "action": "modified",
           "content": "<base64 encoded>",
           "checksum": "sha256:..." }
       ]
     }
  5. Controller picks this up on next reconciliation
```

### File Sync & Transformation Approach (Go-Native)

The agent implements all file operations in Go using the standard library, eliminating external tool dependencies:

```go
// Pseudo-code: file synchronization using Go's filepath and io packages
func SyncDirectory(src, dst string, excludePatterns []string, protected []string) error {
  // Walk src, compute SHA256 for each file
  // Compare against dst checksums
  // Copy only changed files (delta sync)
  // Delete files in dst that are NOT in src AND NOT in protected list
  // Use github.com/bmatcuk/doublestar for ** glob matching in excludePatterns
  // Go's filepath.Match does NOT support ** — doublestar is required
}

// Pseudo-code: targeted JSON patching (NOT full re-serialization)
func NormalizeConfig(configPath string, rules map[string]string) error {
  // Read raw bytes
  // Parse JSON into map[string]interface{}
  // Apply field replacements (systemName, custom fields)
  // Use targeted patching: only modify the specific field values
  // Do NOT re-serialize the entire file — this would reorder keys,
  // change whitespace, and cause false diffs in checksum-based detection.
  // Instead, use byte-level replacement of field values.
}

// Pseudo-code: recursive config.json discovery
func NormalizeAllConfigs(stagingDir string, rules map[string]string) error {
  // filepath.Walk to find ALL files named config.json at any depth
  // Apply NormalizeConfig to each — not just top-level config.json
  // Ignition modules store their own config.json in nested directories
}

// Pseudo-code: YAML manipulation using gopkg.in/yaml.v2
func ParseYAML(yamlPath string) (map[string]interface{}, error) {
  // Structured YAML parsing for config files
}
```

Advantages over shell tools:
- **rsync-equivalent**: Delta sync with checksums, clean deletion, exclude patterns — all in-process
- **jq/yq-equivalent**: Structured JSON/YAML parsing safe from edge cases (nested quotes, multiline values, reordered keys)
- **Performance**: No subprocess overhead, no shell escaping issues
- **Portability**: Single static binary, works on any Linux distro
- **Debuggability**: Clear Go error messages, no cryptic rsync exit codes

Optional shell hooks can be used for advanced transformations: users drop a script in `/opt/ignition-sync/hooks/pre-sync.sh` or `post-sync.sh`, and the agent will execute them if present (shell scripts are opt-in, not mandatory).

---


## Ignition-Aware Sync

The operator embeds deep knowledge of Ignition architecture to make sync safer and smarter.

### Gateway Health & Readiness

**Pre-Sync Health Check**
- Before syncing, agent checks gateway health: `GET /data/api/v1/status`
- Waits up to 5 seconds for gateway to become responsive
- If gateway is down or not ready, agent delays sync and retries (backoff: 5s, 10s, 30s, 60s)
- If gateway remains unavailable after 5 retries, agent logs warning but continues (prevents indefinite stall)

**Designer Session Detection**
- Agent queries `GET /data/api/v2/projects/{projectName}` to check if active design sessions exist
- If Designer is actively editing, agent can:
  - Option A: Wait for session to close (configurable timeout)
  - Option B: Proceed with sync and reload projects (may disconnect Designer)
  - Option C: Fail the sync with condition "DesignerSessionActive"
- User chooses behavior via `spec.ignition.designerSessionPolicy` (wait | proceed | fail)

**Scan API Semantics (Fire-and-Forget)**
- Ignition's scan API is fire-and-forget: `POST /data/api/v1/scan/projects` returns HTTP 200 immediately, confirming the scan was queued. The actual scan runs asynchronously inside the gateway's Java runtime.
- **Do not poll for scan completion** — there is no reliable `scan/status` endpoint in current Ignition versions. Accept 2xx as success.
- Order matters: `scan/projects` MUST be called before `scan/config`
- Agent uses HTTP retry logic (3 retries, exponential backoff) for connection failures
- **Initial sync exception:** On first sync after pod startup, skip scan API calls entirely. Ignition auto-scans its data directory on first boot. Calling the scan API during startup causes race conditions.
- If scan call returns non-2xx after retries, agent reports error condition with reason

### Module Awareness

**Installed Module Detection**
```
Agent queries GET /data/api/v2/modules to detect:
- MQTT Engine (tag provider exclusion logic)
- Sepasoft MES (reporting considerations)
- Transmission (schedule handling)
- Custom modules in user-lib/modules/
```

**MQTT-Managed Tag Providers**
- If MQTT Engine is installed, agent auto-detects MQTT-managed tag providers
- Excludes these from UDT sync (MQTT manages them dynamically)
- Annotation: `ignition-sync.io/exclude-tag-providers: "mqtt-engine,custom-mqtt"`
- Prevents conflicts between git-synced and MQTT-synced tags

**Module JAR Installation Support**
- If `spec.shared.modules` is configured, agent syncs JAR files to `user-lib/modules/`
- Modules are installed in order (dependencies first)
- After sync, agent triggers `POST /data/api/v1/restart` if new modules detected
- Gateway restarts with new modules loaded

### Tag Provider Hierarchy & Inheritance

**Ignition Tag Model**
```
HQ Gateway
├── Default Tag Provider (system-level)
│   └── All sites inherit from this
│
Site Gateway
├── Site Tag Provider (derives from HQ default)
│   └── All areas inherit from this
│
Area Gateway
├── Area Tag Provider (derives from site)
│   └── Leaf level (no children)
```

**Tag Inheritance Strategy Annotation**
```yaml
ignition-sync.io/tag-inheritance-strategy: "full"  # or "leaf-only"
```

- `full`: Sync all UDTs; assume this gateway is responsible for the full tag hierarchy
- `leaf-only`: Sync only leaf-level UDTs; skip inherited tags from parent gateways

Agent behavior:
- Query gateway tag provider hierarchy via API: `GET /data/api/v2/tag-providers`
- Detect parent/child relationships (site -> area)
- If `leaf-only`, exclude UDTs that exist in parent gateway tag provider
- Prevents duplicate definitions and inheritance conflicts

**Tag Provider Normalization**
- Config files may hardcode tag provider names (e.g., "default", "mqtt-engine")
- Agent normalizes via annotation: `ignition-sync.io/tag-provider: "site-tags"`
- Replaces provider references in config.json from git default to gateway-specific provider

### Redundancy & Primary/Backup Coordination

**Primary/Backup Detection**
```yaml
ignition-sync.io/redundancy-role: "primary"  # or "backup"
ignition-sync.io/peer-gateway-name: "site2-backup"
```

- If backup, agent reads last-synced commit from primary gateway (via API)
- Backup waits for primary to sync before starting its own sync
- Prevents out-of-sync primaries and backups

**Sync Ordering for Redundancy**
- Primary syncs first, waits for scan completion
- Backup queries primary's last successful sync via API
- If backup's commit differs from primary's, backup syncs
- If same, backup skips sync (already in sync via replication)

**Failover Handling**
- If primary is down, backup automatically starts syncing
- Agent detects primary unavailability: `GET /data/api/v1/status` returns error
- After 2 failed health checks, backup promotes itself and begins independent syncing
- Logs failover event with severity "warning"

### Perspective Session Management

**Graceful Session Closure Before Sync**
```yaml
spec:
  ignition:
    perspectiveSessionPolicy: "graceful"  # or "immediate" or "none"
```

- `graceful`: Send close message to all connected Perspective clients; wait 10s for disconnect
- `immediate`: Force close all sessions (may appear as disconnect to users)
- `none`: Proceed with sync without closing sessions (users may see stale data briefly)

**Session Tracking**
- Agent queries `GET /data/api/v2/sessions` before sync
- Logs active session count and duration
- If sync disrupts sessions, logs which modules were affected

### Config Normalization Beyond systemName

**Advanced Field Mapping with Go Templates**

```yaml
spec:
  normalize:
    systemName: true
    # Template-based normalization
    templates:
      - jsonPath: ".gateways[0].hostname"
        valueTemplate: "ignition-{{.Namespace}}-{{.GatewayName}}.local"
      - jsonPath: ".defaultTimeZone"
        valueTemplate: "America/Chicago"  # Static, or {{.TimeZone}} from annotation
      - jsonPath: ".locale"
        valueTemplate: "{{.Locale}}"
      - jsonPath: ".instanceIdentifier"
        valueTemplate: "{{.SiteNumber}}-{{.GatewayName}}"
```

Context variables available to templates:
- `{{.Namespace}}` — pod's namespace
- `{{.GatewayName}}` — pod label `app.kubernetes.io/name`
- `{{.SiteNumber}}` — from CR spec.siteNumber
- `{{.ClusterName}}` — K8s cluster name (from kubeconfig context)
- `{{.Timestamp}}` — Unix timestamp of sync

**YAML Config Normalization**
- If YAML config files exist, agent can apply similar templates
- Uses Go's `text/template` package for consistency (same syntax as systemName template)

### Protection of .resources/ Directory

**Critical Safety Guardrail**
```
/ignition-data/
├── .resources/               <- NEVER SYNC THIS
│   ├── ...runtime caches...
│   └── ...temporary files...
├── config/                   <- Git-managed
├── projects/                 <- Git-managed
└── ...
```

The `.resources/` directory contains:
- Runtime caches (perspective resources compiled by gateway)
- Temporary files generated during operation
- State that should NEVER be version controlled or synced

**Agent Safeguards**

1. **Mandatory Exclude Pattern** — `.resources/` is always in the exclude list, enforced by the agent regardless of CRD config:
   ```yaml
   excludePatterns:
     - "**/.resources/**"  # Enforced by agent even if user omits it
   ```
   If missing from CRD, agent adds it automatically and warns in logs.

2. **Pre-Sync Check** — Agent verifies staging directory doesn't contain `.resources/`
   ```
   If staging/.resources/ exists -> ERROR
   Fail sync with reason "StagingDirectoryContainsRuntimeFiles"
   ```

3. **Selective Merge (NOT Atomic Swap)** — The sync uses a merge strategy, not a directory swap:
   ```go
   // Walk staging/, copy each file to /ignition-data/
   // Then walk /ignition-data/ and delete files that:
   //   a) Do NOT exist in staging, AND
   //   b) Are NOT in the protected list (.resources/)
   // .resources/ is NEVER touched — not deleted, not overwritten, not listed
   ```
   A literal `mv staging/ /ignition-data/` would destroy `.resources/`. The merge approach
   preserves the directory entirely while replacing all git-managed content.

4. **Documentation** — Helm chart includes prominent note in README:
   ```
   CRITICAL: Never add .resources/ to git. It contains runtime-generated files
   that will cause conflicts and data corruption. The operator automatically
   prevents this, but manual git adds will corrupt your sync.
   ```

5. **Bidirectional Exclusion** — When watching for gateway changes (bidirectional mode), exclude `.resources/` entirely:
   - Agent's inotify watch excludes this directory
   - Changes in `.resources/` are never captured as "gateway changes" for PR creation

---
