# Sync Agent — Implementation Gaps

Checklist of remaining work from [06a-agent-development-plan.md](architecture/06a-agent-development-plan.md), compared against current implementation on `phase-6-sync-agent`.

---

## Phase 0 — Prerequisites (Controller & CRD Changes)

- [x] 0.1 Agent RBAC (`config/rbac/agent_role.yaml`)
- [x] 0.2 Metadata ConfigMap core fields (`gitURL`, `authType`, `paused`, `excludePatterns`, `gatewayPort`, `gatewayTLS`)
- [ ] 0.2 Metadata ConfigMap auth detail fields (`authSecretName`, `authSecretKey`, `apiKeySecret`, `apiKeySecretKey`) — may not be needed since agent reads from mounted files
- [ ] 0.3 Downward API annotation reader — agent reads `SYNC_PROFILE` from env var; no projected annotations volume, so profile/ref-override changes require pod restart
- [ ] 0.4 New condition types — `AgentReady`, `RefSkew`, `DependenciesMet` and associated reasons not in `pkg/conditions/conditions.go`
- [ ] 0.5 SyncProfile printcolumn fix — `JSONPath=.spec.mappings` returns an array, not a count (`api/v1alpha1/syncprofile_types.go:180`)
- [ ] 0.6 `dependsOn` cycle detection — no `validateDependsOnCycles()` in SyncProfile controller
- [ ] 0.7 Redundant SyncProfile self-watch — `.Watches(&SyncProfile{}, ...)` still present on `For` resource (`internal/controller/syncprofile_controller.go:167`)

## Phase 1 — Agent Bootstrap & Identity

- [x] 1.1 Config loading (`internal/agent/config.go`)
- [ ] 1.2 Annotation reader (mutable identity) — no `ReadAnnotation()` from Downward API projected file
- [x] 1.3 Main entrypoint (`cmd/agent/main.go`)

## Phase 2 — Sync Engine Core

- [x] 2.1 Core types (`SyncPlan`, `SyncResult`, `ResolvedMapping`, `DryRunDiff`)
- [ ] 2.1 `SyncError` type for non-fatal file-level errors — engine currently fails hard on any file error
- [ ] 2.2 Engine as interface — currently a concrete struct, no `Engine` interface + `DefaultEngine` pattern
- [ ] 2.2 `PostSyncHook` / `PreSyncHook` function types — Ignition hooks are called inline in `agent.go`
- [x] 2.3 Template resolution with `missingkey=error` (`internal/agent/profile.go`)
- [ ] 2.3 Max rendered path length check (4096 chars) — missing from `resolveTemplate()`
- [x] 2.4 Staging build (`stageSingleFile`, `stageDirectory`)
- [x] 2.5 Merge to live with managed-path-only orphan cleanup
- [x] 2.6 File operations (`copyFile`, `filesEqual`, `sha256File`)
- [x] 2.7 Exclude patterns with doublestar
- [x] 2.8 `ExecutePlan()` flow (staging -> merge -> cleanup)
- [x] 2.8 Dry-run diff computation

## Phase 3 — Git Integration

- [x] 3.1 Reuse existing `CloneOrFetch`
- [ ] 3.2 Ref override support — no annotation reading for `AnnotationRefOverride`
- [ ] 3.3 SSH host key verification — still `ssh.InsecureIgnoreHostKey()` with no warning log (`internal/agent/agent.go:354`)
- [ ] 3.4 Shallow clone (`Depth: 1`) — not confirmed in `CloneOrFetch`

## Phase 4 — ConfigMap Communication

- [x] 4.1 Read metadata ConfigMap (`internal/agent/configmap.go`)
- [x] 4.2 Write status ConfigMap with optimistic concurrency + 3 retries
- [x] 4.3 Watcher with fallback timer
- [ ] 4.3 K8s informer-based watch — currently uses 3s polling instead of a scoped informer

## Phase 5 — Ignition Hooks (Post-Sync)

- [x] 5.1 Gateway health check — basic `HealthCheck()` exists
- [ ] 5.1 Startup grace period (10s delay between health pass and scan)
- [x] 5.2 Scan API — fire-and-forget, projects then config, 3 retries (`internal/ignition/client.go`)
- [ ] 5.3 Designer session detection — not implemented (no `designer.go`)
- [ ] 5.4 Post-sync verification — not implemented (no `verify.go`)

## Phase 6 — Health & Observability

- [x] 6.1 Health endpoints (`/healthz`, `/readyz`, `/startupz` on `:8082`)
- [x] 6.2 Structured logging (logr/zap)
- [ ] 6.3 Kubernetes Events — agent doesn't emit K8s Events
- [x] 6.4 Graceful shutdown via `signal.NotifyContext`
- [ ] 6.4 In-flight sync completion deadline (30s) on shutdown

## Phase 7 — Security Hardening

- [ ] **7.1 Symlink guard (CRITICAL)** — uses `filepath.Walk` (follows symlinks) and `os.Stat` everywhere; no `Lstat`/`WalkDir`/`ModeSymlink` checks in the engine
- [ ] 7.2 Max template path length (4096 chars)
- [ ] 7.2 Secret file paths via env vars — plan recommended hardcoded mount paths to avoid `/proc` leakage (S-H2); current impl exposes `API_KEY_FILE`, `GIT_TOKEN_FILE`, `GIT_SSH_KEY_FILE`
- [ ] 7.3 `readOnlyRootFilesystem` compatibility — `/tmp` emptyDir, `TMPDIR` env var (deployment config)
- [ ] 7.4 Container security context — `runAsNonRoot`, `readOnlyRootFilesystem`, drop `ALL` caps (deployment config)

## Phase 8 — Controller Integration

- [x] 8.1 Read agent status from ConfigMap — `collectGatewayStatus()` exists
- [ ] 8.2 Detect RefSkew condition
- [x] 8.3 SyncProfile GatewayCount — `updateProfileGatewayCounts()` exists
- [ ] 8.4 `DependenciesMet` condition for `dependsOn` profiles

---

## Priority Order

High-impact items to address first:

1. **Symlink guard (7.1)** — critical security gap; every file walk needs `WalkDir` + `Lstat`
2. **Hook abstraction (2.2)** — `PostSyncHook`/`PreSyncHook` types unblock 5.3 and 5.4 without growing `agent.go`
3. **Downward API annotation reader (0.3 / 1.2)** — enables ref-override and profile switching without pod restart
4. **New condition types (0.4)** — needed before 8.2 and 8.4
5. **dependsOn cycle detection (0.6)** — safety net for SyncProfile graph
6. **Designer session detection (5.3)** — prevents sync conflicts with active designers
7. **Post-sync verification (5.4)** — confirms Ignition picked up changes
8. **SyncProfile printcolumn fix (0.5)** and **self-watch cleanup (0.7)** — low-effort fixes
