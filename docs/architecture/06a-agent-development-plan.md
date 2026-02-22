<!-- Part of: Stoker Architecture (v3) -->
<!-- See also: 00-overview.md, 01-crd.md, 02-controller.md, 04-sync-profile.md, 06-stoker-agent.md -->

# Sync Agent — Development Plan

This document is the meticulous implementation plan for the sync agent sidecar. It synthesizes findings from four expert reviews (K8s Principal Engineer, Ignition Platform Expert, Security Engineer, File Sync Architect) and organizes them into build-order phases with concrete code guidance.

**Guiding principle:** The sync engine is generic (any source → any destination). Ignition-specific behavior is injected via post-sync hooks, not baked into the engine.

---

## Table of Contents

1. [Package Layout](#1-package-layout)
2. [Phase 0 — Prerequisites (Controller & CRD Changes)](#2-phase-0--prerequisites-controller--crd-changes)
3. [Phase 1 — Agent Bootstrap & Identity](#3-phase-1--agent-bootstrap--identity)
4. [Phase 2 — Sync Engine Core](#4-phase-2--sync-engine-core)
5. [Phase 3 — Git Integration](#5-phase-3--git-integration)
6. [Phase 4 — ConfigMap Communication](#6-phase-4--configmap-communication)
7. [Phase 5 — Ignition Hooks (Post-Sync)](#7-phase-5--ignition-hooks-post-sync)
8. [Phase 6 — Health & Observability](#8-phase-6--health--observability)
9. [Phase 7 — Security Hardening](#9-phase-7--security-hardening)
10. [Phase 8 — Controller Integration](#10-phase-8--controller-integration)
11. [Deferred to v1.1](#11-deferred-to-v11)
12. [Expert Findings Cross-Reference](#12-expert-findings-cross-reference)

---

## 1. Package Layout

```
cmd/agent/
  main.go                    # Entrypoint: wires everything, starts run loop

internal/
  syncengine/                # Generic sync engine — no K8s, no Ignition knowledge
    engine.go                # Engine interface + DefaultEngine
    plan.go                  # SyncPlan building (template resolution, validation)
    staging.go               # Build staging directory from resolved mappings
    merge.go                 # Compare staging vs live, copy diffs, delete orphans
    exclude.go               # shouldExclude(), mergeExcludes(), hardcoded patterns
    copy.go                  # copyFile(), copyDir(), filesEqual(), sha256File()
    template.go              # TemplateContext, ResolvePath()
    types.go                 # SyncPlan, SyncResult, SyncError, ResolvedMapping
    engine_test.go           # Unit tests with temp directories

  agent/                     # Agent orchestration — K8s aware, Ignition agnostic
    agent.go                 # Agent struct, main run loop, syncOnce()
    config.go                # Read identity from env/downward API/annotations
    configmap.go             # Read metadata CM, write status CM
    watcher.go               # ConfigMap informer + fallback timer
    health.go                # HTTP health endpoints (/healthz, /readyz)
    agent_test.go

  ignition/                  # Ignition-specific hooks — pluggable
    scan.go                  # POST scan/projects, scan/config (fire-and-forget)
    health.go                # Gateway health check, startup grace period
    designer.go              # Designer session detection + policy
    verify.go                # Post-sync project/tag-provider verification
    ignition_test.go
```

`★ Insight ─────────────────────────────────────`
The 3-layer split (`syncengine` → `agent` → `ignition`) is the key architectural decision. The engine knows nothing about Kubernetes or Ignition — it takes a plan, builds staging, merges to live. The agent layer handles K8s primitives (ConfigMaps, informers). The ignition layer implements `PostSyncHook` functions. A non-Ignition user could replace the ignition layer entirely.
`─────────────────────────────────────────────────`

---

## 2. Phase 0 — Prerequisites (Controller & CRD Changes)

These changes must land **before** agent development begins. They fix gaps the agent depends on.

### 2.0.1 — Agent RBAC (CRITICAL)

The agent needs its own ServiceAccount, Role, and RoleBinding. Currently none exist.

**Create:** `config/rbac/agent_role.yaml`

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: stoker-agent
  namespace: system  # Helm-templated
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: stoker-agent
  namespace: system
rules:
  # Read metadata ConfigMap (controller → agent trigger)
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "watch"]
  # Write status + changes ConfigMaps (agent → controller report)
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["create", "update", "patch"]
  # Read Stoker CR (for git URL, auth ref, excludes, gateway spec)
  - apiGroups: ["stoker.io"]
    resources: ["stokers"]
    verbs: ["get", "list", "watch"]
  # Read SyncProfile CR
  - apiGroups: ["stoker.io"]
    resources: ["syncprofiles"]
    verbs: ["get", "list", "watch"]
  # Read git auth + API key secrets (scoped at injection time)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: stoker-agent
  namespace: system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: stoker-agent
subjects:
  - kind: ServiceAccount
    name: stoker-agent
    namespace: system
```

**Decision:** Since Kubernetes doesn't support per-container ServiceAccounts, the gateway pod shares the agent's SA. Scope the Role to minimum needed. Document this as a known limitation.

### 2.0.2 — Metadata ConfigMap: Add Missing Fields

The current metadata ConfigMap only has `commit`, `ref`, `trigger`. The agent needs more to bootstrap without reading the CR directly.

**Update** `ensureMetadataConfigMap()` in [stoker_controller.go](internal/controller/stoker_controller.go) to include:

```go
Data: map[string]string{
    "commit":          resolvedCommit,
    "ref":             spec.Git.Ref,
    "trigger":         triggerSource,
    // NEW fields for agent bootstrap:
    "gitURL":          spec.Git.Repo,
    "authType":        authType,          // "ssh", "token", "githubApp", "none"
    "authSecretName":  authSecretName,    // Secret name for git credentials
    "authSecretKey":   authSecretKey,     // Key within the Secret
    "apiKeySecret":    spec.Gateway.APIKeySecretRef.Name,
    "apiKeySecretKey": spec.Gateway.APIKeySecretRef.Key,
    "gatewayPort":     fmt.Sprintf("%d", spec.Gateway.Port),
    "gatewayTLS":      fmt.Sprintf("%t", boolValue(spec.Gateway.TLS)),
    "excludePatterns": strings.Join(spec.ExcludePatterns, ","),
    "paused":          fmt.Sprintf("%t", spec.Paused),
},
```

**Rationale:** The agent reads the metadata ConfigMap on startup. Without these fields, the agent would need direct CR read access AND would need to parse CRD types — coupling it tightly to the CRD schema.

### 2.0.3 — Agent Identity via Downward API + Environment Variables

The agent needs to know: who am I? which CR? which profile? The webhook injector will set:

**Fixed identity (env vars — set once at injection):**
```yaml
env:
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  - name: CR_NAME
    value: "my-stoker"        # from AnnotationCRName
  - name: GATEWAY_NAME
    value: "site-gateway"    # from AnnotationGatewayName or app.kubernetes.io/name label
```

**Mutable identity (Downward API projected volume — updates on annotation change):**
```yaml
volumes:
  - name: pod-annotations
    downwardAPI:
      items:
        - path: "annotations"
          fieldRef:
            fieldPath: metadata.annotations
```

The agent reads `AnnotationSyncProfile` and `AnnotationRefOverride` from the projected annotations file, which updates live when annotations change. This enables `ref-override` without pod restart.

### 2.0.4 — New Condition Types

**Add to** [conditions.go](pkg/conditions/conditions.go):

```go
// Agent lifecycle conditions (set on Stoker CR by controller, reading status CM)
TypeAgentReady     = "AgentReady"     // Agent sidecar is running and healthy
TypeRefSkew        = "RefSkew"        // Agent synced a different ref than controller expects
TypeDependenciesMet = "DependenciesMet" // All dependsOn profiles are Synced (SyncProfile condition)

// Agent-specific reasons
ReasonAgentStarting       = "AgentStarting"
ReasonAgentHealthy        = "AgentHealthy"
ReasonAgentUnreachable    = "AgentUnreachable"
ReasonRefSkewDetected     = "RefSkewDetected"
ReasonDependencyWaiting   = "DependencyWaiting"
ReasonDependenciesSatisfied = "DependenciesSatisfied"
ReasonTemplateResolutionFailed = "TemplateResolutionFailed"
ReasonSyncPartial         = "SyncPartial"
```

### 2.0.5 — SyncProfile Printcolumn Fix

The `Mappings` printcolumn uses JSONPath `.spec.mappings` which returns an array, not a count. There is no JSONPath `length()` function in Kubernetes. Options:

- **Option A:** Remove the Mappings column (simplest)
- **Option B:** Change to priority=1 (hidden by default)
- **Recommendation:** Option A — remove it, rely on `kubectl describe` for mapping details

### 2.0.6 — SyncProfile Validation: dependsOn Cycle Detection

**Add to** [syncprofile_controller.go](internal/controller/syncprofile_controller.go):

```go
func validateDependsOnCycles(ctx context.Context, c client.Client, profile *stokerv1alpha1.SyncProfile) error {
    visited := map[string]bool{profile.Name: true}
    queue := make([]string, 0, len(profile.Spec.DependsOn))
    for _, dep := range profile.Spec.DependsOn {
        queue = append(queue, dep.ProfileName)
    }
    for len(queue) > 0 {
        name := queue[0]
        queue = queue[1:]
        if visited[name] {
            return fmt.Errorf("dependency cycle detected: %s", name)
        }
        visited[name] = true
        var dep stokerv1alpha1.SyncProfile
        if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: profile.Namespace}, &dep); err != nil {
            continue // missing profile is a different validation
        }
        for _, d := range dep.Spec.DependsOn {
            queue = append(queue, d.ProfileName)
        }
    }
    return nil
}
```

### 2.0.7 — Fix Redundant SyncProfile Self-Watch

The SyncProfile controller's `SetupWithManager` has a redundant `.Watches(&stokerv1alpha1.SyncProfile{}, ...)` on its own `For` resource. The `For()` already watches SyncProfile changes. The Watches call should enqueue **Stoker** CRs, not SyncProfile CRs.

**Fix:** The `findStokersForProfile` function is correct (returns Stoker requests), but it's wired to the SyncProfile controller. It should be wired to the **Stoker controller** instead, or the SyncProfile controller should re-reconcile Stoker CRs via a cross-controller watch.

**Simplest fix:** Remove the `.Watches()` from the SyncProfile controller entirely. The Stoker controller already watches SyncProfile changes via its own `Watches` clause.

---

## 3. Phase 1 — Agent Bootstrap & Identity

### 3.1.1 — Config Loading

**File:** `internal/agent/config.go`

```go
type AgentConfig struct {
    // Fixed identity (from env vars, set at injection)
    PodName     string
    Namespace   string
    CRName      string
    GatewayName string

    // Paths (hardcoded constants — not configurable via env)
    RepoPath     string // "/repo"
    TargetPath   string // "/ignition-data"
    StagingDir   string // "/ignition-data/.sync-staging"
    GitAuthPath  string // "/etc/stoker/git-credentials"
    APIKeyPath   string // "/etc/stoker/api-key"
    AnnotationsPath string // "/etc/podinfo/annotations"

    // Read from metadata ConfigMap (mutable)
    GitURL         string
    AuthType       string
    ExcludePatterns []string
    GatewayPort    int32
    GatewayTLS     bool
}
```

**Decision: Hardcoded secret mount paths.** The Security review (H-2) found that exposing secret paths via env vars (`API_KEY_FILE`) leaks information through `/proc/{pid}/environ`. Instead, use well-known constants in the agent binary. The webhook injector mounts secrets at these fixed paths.

### 3.1.2 — Annotation Reader (Mutable Identity)

```go
// ReadAnnotation reads a specific annotation from the Downward API projected file.
// The file updates live when pod annotations change (no restart needed).
func ReadAnnotation(annotationsPath, key string) (string, error) {
    data, err := os.ReadFile(annotationsPath)
    if err != nil {
        return "", fmt.Errorf("reading annotations: %w", err)
    }
    // Downward API format: key="value"\n
    for _, line := range strings.Split(string(data), "\n") {
        if k, v, ok := strings.Cut(line, "="); ok && k == key {
            return strings.Trim(v, "\""), nil
        }
    }
    return "", nil // annotation not present
}
```

This enables `ref-override` and `sync-profile` to be changed without pod restart.

### 3.1.3 — Main Entry Point

**File:** `cmd/agent/main.go`

```go
func main() {
    // 1. Load config from env + downward API
    // 2. Create K8s client (scoped informer for ConfigMaps in this namespace)
    // 3. Create git client (reuse internal/git)
    // 4. Create sync engine (internal/syncengine)
    // 5. Create Ignition hooks (internal/ignition) — only if gateway port > 0
    // 6. Create agent (internal/agent) with engine + hooks
    // 7. Start health server on :8082
    // 8. Run initial sync (blocking)
    // 9. Start ConfigMap watcher + fallback timer
    // 10. Block on context cancellation (SIGTERM → graceful shutdown)
}
```

---

## 4. Phase 2 — Sync Engine Core

This is the heart of the agent. It is **completely Kubernetes-unaware and Ignition-unaware**.

### 4.2.1 — Core Types

**File:** `internal/syncengine/types.go`

```go
// SyncPlan is a fully resolved, ready-to-execute sync operation.
type SyncPlan struct {
    RepoRoot   string            // absolute path to git clone (/repo)
    TargetRoot string            // absolute path to live directory (/ignition-data)
    StagingDir string            // absolute path to staging directory
    Mappings   []ResolvedMapping // ordered, templates already resolved
    Excludes   []string          // merged exclude patterns
    DryRun     bool              // build staging only, don't merge to live
}

// ResolvedMapping is a SyncMapping with all templates resolved and validated.
type ResolvedMapping struct {
    Source      string // absolute path under RepoRoot
    Destination string // relative path under TargetRoot (and StagingDir)
    Type        string // "dir" or "file"
    Required    bool
}

// SyncResult captures the outcome of a sync operation.
type SyncResult struct {
    FilesAdded    []string
    FilesModified []string
    FilesDeleted  []string
    FilesSkipped  []string      // excludes, symlinks
    Errors        []SyncError   // non-fatal file-level errors
    Duration      time.Duration
}

// SyncError represents a non-fatal error during sync.
type SyncError struct {
    Path string
    Op   string // "copy", "delete", "checksum"
    Err  error
}

// TemplateContext holds all variables available during template resolution.
type TemplateContext struct {
    Vars        map[string]string // from SyncProfile.spec.vars
    GatewayName string
    Namespace   string
    Ref         string
    Commit      string
}
```

### 4.2.2 — Engine Interface

**File:** `internal/syncengine/engine.go`

```go
// Engine is the core sync engine interface.
type Engine interface {
    BuildPlan(ctx context.Context, opts PlanOptions) (*SyncPlan, error)
    Execute(ctx context.Context, plan *SyncPlan) (*SyncResult, error)
}

// PlanOptions are the inputs to plan building.
type PlanOptions struct {
    RepoRoot    string
    TargetRoot  string
    StagingDir  string
    Mappings    []MappingInput     // raw mappings with possible templates
    Excludes    []string           // merged excludes (hardcoded + global + profile)
    DryRun      bool
    TemplateCtx *TemplateContext
    DeploymentMode *DeploymentModeInput // optional, becomes final mapping
}

// MappingInput is a raw mapping before template resolution.
type MappingInput struct {
    Source      string
    Destination string
    Type        string
    Required    bool
}

// DeploymentModeInput is syntactic sugar — expanded to a final mapping.
type DeploymentModeInput struct {
    Source      string // repo-relative overlay directory
    Destination string // default: "config/resources/core"
}

// PostSyncHook is called after a successful sync.
// Ignition-specific behavior (scan API, health checks) is a hook.
type PostSyncHook func(ctx context.Context, result *SyncResult) error
```

`★ Insight ─────────────────────────────────────`
`DeploymentModeSpec` is treated as syntactic sugar — the engine expands it into a final mapping appended after all explicit mappings. This keeps the engine generic: it only knows about ordered mappings. Ignition's "overlay on top of core" is just "last mapping to `config/resources/core`".
`─────────────────────────────────────────────────`

### 4.2.3 — Template Resolution

**File:** `internal/syncengine/template.go`

Key security requirements (from Security review C-4):
- Use `text/template` with `Option("missingkey=error")` — hard error on missing var
- Template context is a simple struct with string fields only — no methods, no live clients
- Re-validate resolved paths for traversal/absolute after resolution
- Maximum rendered path length: 4096 characters

```go
func ResolvePath(raw string, ctx *TemplateContext) (string, error) {
    if !strings.Contains(raw, "{{") {
        return raw, nil // fast path
    }
    tmpl, err := template.New("path").Option("missingkey=error").Parse(raw)
    if err != nil {
        return "", fmt.Errorf("parsing template %q: %w", raw, err)
    }
    var buf strings.Builder
    if err := tmpl.Execute(&buf, ctx); err != nil {
        return "", fmt.Errorf("resolving template %q: %w", raw, err)
    }
    resolved := buf.String()
    if len(resolved) > 4096 {
        return "", fmt.Errorf("resolved path exceeds 4096 chars: %q", resolved[:100])
    }
    if filepath.IsAbs(resolved) {
        return "", fmt.Errorf("resolved path is absolute: %q", resolved)
    }
    if containsTraversal(resolved) {
        return "", fmt.Errorf("resolved path contains traversal: %q", resolved)
    }
    return resolved, nil
}
```

### 4.2.4 — Staging Build

**File:** `internal/syncengine/staging.go`

The staging phase applies all mappings in order to a clean staging directory. File-level deep overlay semantics: later mappings overwrite files at the same path but merge directories.

```go
func (e *DefaultEngine) buildStaging(ctx context.Context, plan *SyncPlan) error {
    // 1. Clean staging directory (RemoveAll + MkdirAll)
    // 2. For each mapping in order:
    //    a. Resolve source to absolute path: filepath.Join(plan.RepoRoot, mapping.Source)
    //    b. Check source exists. If not and Required: return error. If not and !Required: skip.
    //    c. If Type == "dir": walkAndCopy(src, filepath.Join(plan.StagingDir, mapping.Destination), plan.Excludes)
    //    d. If Type == "file": copyFile(src, filepath.Join(plan.StagingDir, mapping.Destination))
    // 3. Return nil
}
```

### 4.2.5 — Merge to Live

**File:** `internal/syncengine/merge.go`

Two sub-operations:
1. **Copy diffs:** Walk staging, compare each file against live. Only copy if content differs (size-check then SHA256).
2. **Orphan cleanup:** Walk live within **managed paths only**, delete files not in staging and not excluded.

```go
func (e *DefaultEngine) mergeToLive(ctx context.Context, plan *SyncPlan) (*SyncResult, error) {
    result := &SyncResult{}

    // 1. Compute managed paths from mapping destinations
    managedPaths := make([]string, 0, len(plan.Mappings))
    for _, m := range plan.Mappings {
        managedPaths = append(managedPaths, m.Destination)
    }

    // 2. Walk staging, copy changed files to live
    //    - filesEqual() uses size-check then SHA256
    //    - New file (not in live) → result.FilesAdded
    //    - Changed file → result.FilesModified
    //    - Unchanged file → skip (no write to live)

    // 3. Walk live within managed paths only, delete orphans
    //    - File not in staging AND not excluded → result.FilesDeleted, os.Remove()
    //    - File excluded → skip (leave untouched)
    //    - CRITICAL: only walk within managedPaths, not all of TargetRoot

    // 4. Clean empty directories left by orphan deletion

    return result, nil
}
```

**Why managed-path-only cleanup (from Ignition Expert, Finding 8):** `/ignition-data/` contains directories NOT managed by git: `logs/`, `backups/`, `db/`, `user-lib/`, `.uuid`, `metro-keystore`. Walking all of TargetRoot and deleting everything not in staging would destroy these. The agent must only delete within directories it is responsible for, based on the resolved mapping destinations.

### 4.2.6 — File Operations

**File:** `internal/syncengine/copy.go`

```go
// copyFile creates parent dirs, copies content, preserves source permissions.
func copyFile(src, dst string) error { ... }

// copyDir walks srcDir, copies to dstDir, applying excludes.
// Uses filepath.WalkDir (not Walk — avoids extra os.Stat per entry).
// Skips symlinks with warning log.
func copyDir(srcDir, dstDir string, excludes []string) error { ... }

// filesEqual returns true if both files have identical content.
// Fast path: different sizes → false without reading content.
func filesEqual(a, b string) (bool, error) { ... }

// sha256File computes SHA256 of a file.
func sha256File(path string) (string, error) { ... }
```

**Symlink handling (Security C-1, File Sync Architect):** Use `os.Lstat()` everywhere. Never follow symlinks. Skip with a warning log. After resolving any path, verify canonical path is still under the allowed root.

```go
// In the file walker:
if d.Type()&fs.ModeSymlink != 0 {
    log.Info("skipping symlink", "path", relPath)
    result.FilesSkipped = append(result.FilesSkipped, relPath)
    return nil
}
```

### 4.2.7 — Exclude Patterns

**File:** `internal/syncengine/exclude.go`

Uses `github.com/bmatcuk/doublestar/v4` for `**` glob support.

Hardcoded excludes (always present, cannot be removed):
```go
var hardcodedExcludes = []string{
    "**/.sync-staging/**",
}
```

Everything else (including `**/.resources/**`) comes from the merged global + profile exclude patterns. Per the user's direction, `.resources/` is a recommendation, not a hard block.

Excludes serve **dual duty**: skip during copy to staging AND skip during orphan deletion. One mechanism, applied everywhere.

### 4.2.8 — Execute Flow

```go
func (e *DefaultEngine) Execute(ctx context.Context, plan *SyncPlan) (*SyncResult, error) {
    start := time.Now()

    // 1. Build staging
    if err := e.buildStaging(ctx, plan); err != nil {
        return nil, fmt.Errorf("building staging: %w", err)
    }

    // 2. If dry-run, compute diff and return without merging
    if plan.DryRun {
        return e.computeDryRunDiff(ctx, plan)
    }

    // 3. Merge staging to live
    result, err := e.mergeToLive(ctx, plan)
    if err != nil {
        return result, fmt.Errorf("merging to live: %w", err)
    }

    // 4. Clean up staging
    os.RemoveAll(plan.StagingDir)

    result.Duration = time.Since(start)
    return result, nil
}
```

---

## 5. Phase 3 — Git Integration

### 5.3.1 — Reuse Existing Git Client

The [internal/git/client.go](internal/git/client.go) `CloneOrFetch` function already handles the agent's needs. The agent calls it with the commit SHA from the metadata ConfigMap.

### 5.3.2 — Ref Override Support

When `AnnotationRefOverride` is present on the pod, the agent resolves the ref independently (using `LsRemote`) instead of using the metadata ConfigMap's commit. The agent writes `syncedRef` to the status ConfigMap so the controller can detect skew.

```go
func (a *Agent) resolveRef(ctx context.Context, meta *MetadataConfig) (commit string, ref string, err error) {
    override := ReadAnnotation(a.config.AnnotationsPath, types.AnnotationRefOverride)
    if override != "" {
        // Resolve independently via ls-remote
        commit, err := a.gitClient.LsRemote(ctx, a.config.GitURL, override, a.auth)
        return commit, override, err
    }
    return meta.Commit, meta.Ref, nil
}
```

### 5.3.3 — SSH Host Key Verification (Security C-2)

The current code uses `ssh.InsecureIgnoreHostKey()`. For v1:

- If `knownHostsSecretRef` is provided in the CRD, parse and validate server fingerprints
- If omitted, log a WARNING on every connection: "SSH host key verification disabled — MITM risk"
- Add `spec.git.auth.sshKey.knownHostsSecretRef` to the CRD (optional field)

**This is a CRD change — add the field in Phase 0 but implement enforcement in Phase 3.**

### 5.3.4 — Shallow Clone for Disk Savings

Use `--depth 1` equivalent in go-git to minimize disk usage on initial clone:

```go
cloneOpts := &git.CloneOptions{
    URL:   repoURL,
    Depth: 1, // shallow clone
}
```

On subsequent fetches, fetch only the needed commit. This addresses Security H-1 (disk space DoS).

---

## 6. Phase 4 — ConfigMap Communication

### 6.4.1 — Read Metadata ConfigMap

The agent watches `stoker-metadata-{crName}` for changes using a scoped informer (only ConfigMaps in the agent's namespace, filtered by label `stoker.io/cr-name`).

```go
func (a *Agent) readMetadataConfigMap(ctx context.Context) (*MetadataConfig, error) {
    var cm corev1.ConfigMap
    key := types.NamespacedName{
        Namespace: a.config.Namespace,
        Name:      fmt.Sprintf("stoker-metadata-%s", a.config.CRName),
    }
    if err := a.client.Get(ctx, key, &cm); err != nil {
        return nil, err
    }
    return &MetadataConfig{
        Commit:  cm.Data["commit"],
        Ref:     cm.Data["ref"],
        Trigger: cm.Data["trigger"],
        Paused:  cm.Data["paused"] == "true",
        // ... other fields from Phase 0 additions
    }, nil
}
```

### 6.4.2 — Write Status ConfigMap

Each agent writes its own key (gateway name) within the shared status ConfigMap. Use optimistic concurrency (resourceVersion) for conflict handling.

```go
func (a *Agent) writeStatus(ctx context.Context, result *SyncResult, ref, commit string) error {
    cmName := fmt.Sprintf("stoker-status-%s", a.config.CRName)

    // Read current ConfigMap
    var cm corev1.ConfigMap
    key := types.NamespacedName{Namespace: a.config.Namespace, Name: cmName}
    err := a.client.Get(ctx, key, &cm)

    // Marshal this gateway's status to JSON
    status := GatewayStatusReport{
        SyncedAt:      time.Now().UTC().Format(time.RFC3339),
        Commit:        commit,
        Ref:           ref,
        FilesAdded:    len(result.FilesAdded),
        FilesModified: len(result.FilesModified),
        FilesDeleted:  len(result.FilesDeleted),
        Duration:      result.Duration.String(),
        Errors:        len(result.Errors),
    }
    data, _ := json.Marshal(status)

    if apierrors.IsNotFound(err) {
        // Create new ConfigMap
        cm = corev1.ConfigMap{
            ObjectMeta: metav1.ObjectMeta{
                Name:      cmName,
                Namespace: a.config.Namespace,
                Labels:    map[string]string{types.LabelCRName: a.config.CRName},
            },
            Data: map[string]string{a.config.GatewayName: string(data)},
        }
        return a.client.Create(ctx, &cm)
    }

    // Update existing — merge this gateway's key
    if cm.Data == nil {
        cm.Data = make(map[string]string)
    }
    cm.Data[a.config.GatewayName] = string(data)
    return a.client.Update(ctx, &cm) // Uses resourceVersion for optimistic concurrency
}
```

**Conflict handling:** If `Update` returns a conflict error (409), read the latest ConfigMap, merge this gateway's key, and retry. Maximum 3 retries.

### 6.4.3 — ConfigMap Watcher with Fallback Timer

```go
func (a *Agent) startWatcher(ctx context.Context) {
    // Primary: K8s informer on metadata ConfigMap
    informer := cache.NewInformerWithOptions(cache.InformerOptions{
        ListerWatcher: /* filtered by label */,
        ObjectType:    &corev1.ConfigMap{},
        Handler: cache.ResourceEventHandlerFuncs{
            UpdateFunc: func(old, new interface{}) {
                a.triggerSync()
            },
        },
    })

    // Fallback: periodic timer (syncPeriod from profile, default 30s)
    ticker := time.NewTicker(time.Duration(a.syncPeriod) * time.Second)

    go informer.Run(ctx.Done())
    for {
        select {
        case <-a.syncTrigger:
            a.syncOnce(ctx)
        case <-ticker.C:
            a.syncOnce(ctx)
        case <-ctx.Done():
            return
        }
    }
}
```

---

## 7. Phase 5 — Ignition Hooks (Post-Sync)

These are pluggable `PostSyncHook` functions. They run after a successful merge-to-live. Hook failure does NOT invalidate the file sync — it's logged and reported in status.

### 7.5.1 — Gateway Health Check

**File:** `internal/ignition/health.go`

```go
func NewHealthCheckHook(gatewayURL string, client *http.Client) PostSyncHook {
    return func(ctx context.Context, result *SyncResult) error {
        // GET /data/api/v1/gateway-info
        // Verify response includes valid ignitionVersion field
        // Startup grace period: 10s after health check passes before scan
    }
}
```

**Initial sync:** Skip health check entirely. Files must be in place BEFORE the gateway starts. The agent syncs first (blocking), then the gateway container starts.

**Subsequent syncs:** Check health, wait for gateway to be responsive before calling scan API.

### 7.5.2 — Scan API (Fire-and-Forget)

**File:** `internal/ignition/scan.go`

```go
func NewScanHook(gatewayURL string, apiKey string, client *http.Client) PostSyncHook {
    return func(ctx context.Context, result *SyncResult) error {
        if result.isInitialSync {
            return nil // skip scan on initial sync — Ignition auto-scans on first boot
        }
        if len(result.FilesAdded)+len(result.FilesModified)+len(result.FilesDeleted) == 0 {
            return nil // nothing changed, skip scan
        }

        // Order matters: projects MUST be scanned before config
        scanProjects(ctx, gatewayURL, apiKey) // POST /data/api/v1/scan/projects
        scanConfig(ctx, gatewayURL, apiKey)    // POST /data/api/v1/scan/config

        // Fire-and-forget: accept any 2xx. 3 retries with exponential backoff.
        // Do NOT poll for completion — there is no reliable endpoint.
    }
}
```

### 7.5.3 — Designer Session Detection

**File:** `internal/ignition/designer.go`

```go
func NewDesignerCheckHook(gatewayURL string, apiKey string, policy string) PostSyncHook {
    return func(ctx context.Context, result *SyncResult) error {
        // GET /data/api/v2/sessions (or /data/api/v2/design/sessions)
        // Policy: "wait" (default) → wait up to 5min for sessions to close
        //         "proceed" → log warning, continue
        //         "fail" → return error, abort sync
    }
}
```

**Note:** This hook runs BEFORE sync (pre-sync hook), not after. Add a `PreSyncHook` type:

```go
type PreSyncHook func(ctx context.Context, plan *SyncPlan) error
```

### 7.5.4 — Post-Sync Verification

**File:** `internal/ignition/verify.go`

```go
func NewVerifyHook(gatewayURL string, apiKey string) PostSyncHook {
    return func(ctx context.Context, result *SyncResult) error {
        // GET /data/api/v1/projects/list — compare against synced project directories
        // GET /data/api/v1/resources/list/ignition/tag-provider — verify tag providers
        // Short timeout: 5-10s
        // If project missing → return error (sets SyncStatus: Error)
    }
}
```

---

## 8. Phase 6 — Health & Observability

### 8.6.1 — Health Endpoints

**File:** `internal/agent/health.go`

Port: `:8082` (distinct from gateway's 8043 and controller's 8081)

| Endpoint | Purpose | Logic |
|----------|---------|-------|
| `/healthz` | Liveness probe | Always 200 (process alive) |
| `/readyz` | Readiness probe | 200 after initial sync completes; 503 before |
| `/startupz` | Startup probe | 200 after git clone + initial sync; 503 before |

```yaml
# Pod spec for probes:
startupProbe:
  httpGet:
    path: /startupz
    port: 8082
  failureThreshold: 30
  periodSeconds: 10   # allows 5 minutes for initial clone + sync
livenessProbe:
  httpGet:
    path: /healthz
    port: 8082
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /readyz
    port: 8082
  periodSeconds: 5
```

### 8.6.2 — Structured Logging

Use `slog` (Go stdlib) or `logr` (controller-runtime compatible). Log key events:

- Sync triggered (commit, ref, source)
- Staging built (mapping count, file count, duration)
- Merge completed (added/modified/deleted/skipped counts)
- Scan API called (response code)
- Errors at every level with full context

### 8.6.3 — Kubernetes Events

Emit K8s Events for major state transitions:

```go
recorder.Event(stk, corev1.EventTypeNormal, "SyncCompleted",
    fmt.Sprintf("Gateway %s synced to %s (%d files changed)", gwName, commit[:8], changedCount))
recorder.Event(stk, corev1.EventTypeWarning, "SyncFailed",
    fmt.Sprintf("Gateway %s sync failed: %s", gwName, err.Error()))
```

### 8.6.4 — Graceful Shutdown

On SIGTERM:
1. Stop accepting new sync triggers
2. If a sync is in-flight, let it complete (with a 30s deadline)
3. Write final status to ConfigMap
4. Exit

```go
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer cancel()
// ... run agent with ctx
// When ctx is cancelled, syncOnce checks ctx.Done() at each step
```

---

## 9. Phase 7 — Security Hardening

### 9.7.1 — Symlink Guard (CRITICAL)

Every file operation in `copyDir` must use `os.Lstat()` and reject symlinks:

```go
if d.Type()&fs.ModeSymlink != 0 {
    log.Info("skipping symlink", "path", relPath)
    return nil
}
```

Additionally, after any path resolution (template, filepath.Join), verify the canonical path is still under the allowed root:

```go
func verifyPathSafe(path, allowedRoot string) error {
    resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
    if err != nil {
        return err
    }
    if !strings.HasPrefix(resolved+"/", allowedRoot+"/") {
        return fmt.Errorf("path escapes root: %s resolves to %s", path, resolved)
    }
    return nil
}
```

### 9.7.2 — Template Injection Prevention (CRITICAL)

The `TemplateContext` struct exposes ONLY simple string fields. No methods, no pointers to live clients, no `os` package references:

```go
type TemplateContext struct {
    Vars        map[string]string
    GatewayName string
    Namespace   string
    Ref         string
    Commit      string
}
```

The `text/template` FuncMap is empty — no custom functions. If the user tries `{{ call .SomeMethod }}`, it fails because there are no methods on the struct.

### 9.7.3 — readOnlyRootFilesystem Compatibility

The distroless image has no writable `/tmp`. Mount:
- `/repo` — emptyDir (writable) for git clone
- `/ignition-data` — gateway PVC (writable) for sync target
- `/tmp` — emptyDir with `sizeLimit: 50Mi` (for go-git temp files)

Set `TMPDIR=/tmp` env var so Go's `os.TempDir()` uses the writable emptyDir.

### 9.7.4 — Container Security Context

```yaml
securityContext:
  runAsNonRoot: true
  # RunAsUser intentionally omitted — inherits pod-level UID (e.g., 2003
  # for Ignition) so files on the shared data volume have correct ownership.
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
  seccompProfile:
    type: RuntimeDefault
```

**UID inheritance note:** The agent omits `RunAsUser` so it inherits the pod-level UID set by the Ignition Helm chart (typically 2003). This means files written by the agent to the shared data volume are owned by the same UID as the gateway container — no `fsGroup` workaround or special file permissions needed.

---

## 10. Phase 8 — Controller Integration

After the agent is functional, update the controller to:

### 10.8.1 — Read Agent Status from ConfigMap

The controller reads `stoker-status-{crName}` and updates `DiscoveredGateway` status fields:

```go
func (r *Reconciler) updateGatewayStatusFromConfigMap(ctx context.Context, stk *v1alpha1.Stoker) error {
    var statusCM corev1.ConfigMap
    key := types.NamespacedName{
        Namespace: stk.Namespace,
        Name:      fmt.Sprintf("stoker-status-%s", stk.Name),
    }
    if err := r.Get(ctx, key, &statusCM); err != nil {
        return client.IgnoreNotFound(err)
    }

    for i, gw := range stk.Status.DiscoveredGateways {
        if data, ok := statusCM.Data[gw.Name]; ok {
            var report GatewayStatusReport
            json.Unmarshal([]byte(data), &report)
            stk.Status.DiscoveredGateways[i].SyncStatus = "Synced"
            stk.Status.DiscoveredGateways[i].SyncedCommit = report.Commit
            stk.Status.DiscoveredGateways[i].SyncedRef = report.Ref
            stk.Status.DiscoveredGateways[i].LastSyncTime = &metav1.Time{Time: report.SyncedAt}
            // ... etc
        }
    }
    return nil
}
```

### 10.8.2 — Detect RefSkew

If `gw.SyncedRef != stk.Status.LastSyncRef`, set `TypeRefSkew` condition as a warning. This detects gateways using `ref-override`.

### 10.8.3 — SyncProfile GatewayCount

The controller counts gateways referencing each SyncProfile and updates `SyncProfileStatus.GatewayCount`.

### 10.8.4 — DependenciesMet Condition

For SyncProfiles with `dependsOn`, the controller checks whether all gateways using the dependency profile report `Synced` status before allowing dependent gateways to proceed.

---

## 11. Deferred to v1.1

These features are documented but not implemented in v1:

| Feature | Reason to Defer |
|---------|-----------------|
| Delta sync (persistent checksum manifest) | Full staging rebuild is fast enough for v1 |
| Bidirectional sync (gateway → git) | Complex, experimental, needs more design |
| Snapshots & rollback | Requires snapshot storage backend |
| Canary deployment stages | Requires health check feedback loop |
| ConfigMap integrity signing (HMAC) | Important but not blocking for initial release |
| JSON field-order-preserving patching | Use `tidwall/sjson` — implement when config normalization is added |
| `includeMappingsFrom` (profile composition) | v1beta1 feature |
| Conditional mappings (`condition.podLabel`) | v1beta1 feature |
| Maintenance windows | v1beta1 feature |

---

## 12. Expert Findings Cross-Reference

### Legend
- **K** = K8s Principal Engineer
- **I** = Ignition Platform Expert
- **S** = Security Engineer
- **F** = File Sync Architect

### Critical Findings

| ID | Source | Finding | Plan Section |
|----|--------|---------|--------------|
| K-C1 | K | Agent RBAC doesn't exist | 2.0.1 |
| K-C2 | K | Metadata ConfigMap missing fields for agent bootstrap | 2.0.2 |
| K-C3 | K | Agent has no way to discover its own identity | 2.0.3 |
| S-C1 | S | Symlink traversal — filesystem escape | 9.7.1 |
| S-C2 | S | SSH host key verification disabled | 5.3.3 |
| S-C3 | S | No agent RBAC separation (overlaps K-C1) | 2.0.1 |
| S-C4 | S | Go template injection via vars | 9.7.2, 4.2.3 |
| I-1 | I | Scan API semantics: fire-and-forget, initial sync skip | 7.5.2 |
| I-2 | I | Initial sync timing: files before gateway starts | 7.5.1 |
| I-3 | I | systemName normalization | Deferred (v1.1) |
| I-4 | I | Deployment mode overlay (always recomposed) | 4.2.2 |
| I-5 | I | .resources/ protection | 4.2.7 (recommendation not hard block) |
| I-6 | I | config-mode.json fallback | 7.5.2 (doc only for v1) |
| I-7 | I | JSON patching must preserve field order | Deferred (v1.1 with tidwall/sjson) |
| I-8 | I | Managed-path-only cleanup | 4.2.5 |

### High Findings

| ID | Source | Finding | Plan Section |
|----|--------|---------|--------------|
| K-H1 | K | GatewayStatus missing SyncProfileName, generation | 10.8.1 |
| K-H2 | K | Missing condition types | 2.0.4 |
| K-H3 | K | Helm chart ClusterRole out of sync | Phase 0 (manual fix) |
| K-H4 | K | SyncProfile printcolumn bug | 2.0.5 |
| S-H1 | S | No disk space limits on git clone | 5.3.4 |
| S-H2 | S | API key path disclosure via env vars | 3.1.1 |
| S-H3 | S | ConfigMap tampering | Deferred (v1.1, HMAC) |
| S-H4 | S | Bidirectional data exfiltration | Deferred (bidir is v1.1) |
| S-H5 | S | readOnlyRootFilesystem vs go-git | 9.7.3 |
| I-9 | I | MQTT Engine tag provider exclusion | 4.2.7 (via user exclude patterns) |
| I-10 | I | .uuid file must never be synced | 4.2.5 (managed-path-only) |

### Medium Findings

| ID | Source | Finding | Plan Section |
|----|--------|---------|--------------|
| K-M1 | K | Agent health probes | 8.6.1 |
| K-M2 | K | Graceful shutdown | 8.6.4 |
| K-M3 | K | Concurrent ConfigMap writes | 6.4.2 |
| K-M4 | K | dependsOn cycle detection | 2.0.6 |
| K-M5 | K | Scoped informer for ConfigMap watch | 6.4.3 |
| K-M6 | K | Redundant SyncProfile self-watch | 2.0.7 |
| S-M1 | S | UID mismatch agent vs Ignition | 9.7.4 |
| S-M2 | S | ConfigMap size limits | 6.4.2 (cap status data) |
| S-M3 | S | Webhook TLS | Deferred (existing controller issue) |
| I-11 | I | Designer session detection | 7.5.3 |
| I-12 | I | Post-sync project verification | 7.5.4 |

### Design Decisions Summary

| Decision | Chosen Approach | Rationale |
|----------|----------------|-----------|
| Staging strategy | Staging + selective merge | Protects live from partial failures; handles protected dirs |
| Mapping overlap | File-level deep overlay | Supports composition — ordered mappings overwrite files, merge dirs |
| Orphan cleanup | Managed-path-only | Prevents deleting logs, backups, db, keystore |
| Protected paths | Via exclude patterns, not separate concept | One mechanism, generalizable |
| Change detection (v1) | Size-check then SHA256 at merge time | Simple, always correct, fast enough |
| Template missing var | Hard error | Silent wrong paths are dangerous for SCADA |
| Deployment mode | Syntactic sugar for final mapping | Keeps engine generic |
| Symlinks | Skip with warning | Security risk, unnecessary for config sync |
| Mid-sync failure | Continue, report partial, retry next cycle | No rollback in v1 |
| Delta sync | Defer to v1.1 | Full staging rebuild is fast enough |
| Config normalization | Defer to v1.1 | Core file sync first, JSON patching later |
| .resources/ | Recommendation via default excludes, not hard block | User requested flexibility |

---

## Build Order Summary

```
Phase 0: Prerequisites (controller/CRD changes)
  ├── 0.1 Agent RBAC (ServiceAccount, Role, RoleBinding)
  ├── 0.2 Metadata ConfigMap: add missing fields
  ├── 0.3 Agent identity: Downward API + env vars
  ├── 0.4 New condition types
  ├── 0.5 SyncProfile printcolumn fix
  ├── 0.6 dependsOn cycle detection
  └── 0.7 Fix redundant SyncProfile self-watch

Phase 1: Agent Bootstrap
  ├── 1.1 Config loading (env + downward API + hardcoded paths)
  ├── 1.2 Annotation reader (mutable identity)
  └── 1.3 Main entrypoint skeleton

Phase 2: Sync Engine Core
  ├── 2.1 Types (SyncPlan, SyncResult, TemplateContext)
  ├── 2.2 Engine interface
  ├── 2.3 Template resolution (with security guards)
  ├── 2.4 Staging build
  ├── 2.5 Merge to live (managed-path-only cleanup)
  ├── 2.6 File operations (copy, symlink guard, SHA256)
  ├── 2.7 Exclude patterns (doublestar)
  └── 2.8 Execute flow (staging → merge → cleanup)

Phase 3: Git Integration
  ├── 3.1 Reuse existing CloneOrFetch
  ├── 3.2 Ref override support
  ├── 3.3 SSH host key verification warning
  └── 3.4 Shallow clone

Phase 4: ConfigMap Communication
  ├── 4.1 Read metadata ConfigMap
  ├── 4.2 Write status ConfigMap (optimistic concurrency)
  └── 4.3 ConfigMap watcher + fallback timer

Phase 5: Ignition Hooks
  ├── 5.1 Gateway health check (pre-sync)
  ├── 5.2 Scan API (post-sync, fire-and-forget)
  ├── 5.3 Designer session detection (pre-sync)
  └── 5.4 Post-sync verification

Phase 6: Health & Observability
  ├── 6.1 Health endpoints (/healthz, /readyz, /startupz)
  ├── 6.2 Structured logging
  ├── 6.3 Kubernetes Events
  └── 6.4 Graceful shutdown

Phase 7: Security Hardening
  ├── 7.1 Symlink guard (Lstat + EvalSymlinks)
  ├── 7.2 Template injection prevention
  ├── 7.3 readOnlyRootFilesystem + /tmp emptyDir
  └── 7.4 Container security context

Phase 8: Controller Integration
  ├── 8.1 Read agent status from ConfigMap
  ├── 8.2 Detect RefSkew
  ├── 8.3 SyncProfile GatewayCount
  └── 8.4 DependenciesMet condition
```

---

## New Dependencies

| Package | Purpose | Existing? |
|---------|---------|-----------|
| `github.com/bmatcuk/doublestar/v4` | `**` glob matching for exclude patterns | No — add |
| `github.com/go-git/go-git/v5` | Git operations | Yes |
| `k8s.io/client-go` | K8s API client, informers | Yes |
| `text/template` (stdlib) | Template resolution | Yes |
| `crypto/sha256` (stdlib) | File checksums | Yes |

Only **one new dependency**: `doublestar/v4`.
