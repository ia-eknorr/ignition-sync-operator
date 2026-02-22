# Roadmap

## v0.1.0 — MVP

The minimum viable release: controller + agent sidecar can sync Ignition gateway
configuration from a Git repository. End-to-end flow works but operational polish
is limited.

### Completed

- Stoker CRD with git ref resolution via `ls-remote`
- SyncProfile CRD with declarative file mappings and deployment mode overlays
- Agent sidecar with sync engine (clone, staging, merge, orphan cleanup)
- MutatingWebhook for automatic sidecar injection
- Gateway discovery via pod annotations
- Status aggregation from agent ConfigMaps
- Webhook receiver for push-event-driven sync
- CI/CD: release workflow (Docker images + Helm chart OCI push)
- Helm chart with cert-manager TLS, agent image configuration, agent RBAC
- Helm chart values documentation via helm-docs
- Webhook receiver HMAC signature validation
- Agent Dockerfile health endpoint (liveness/readiness for sidecar)
- Structured logging alignment (controller uses `logr`, agent matches)

### Remaining for v0.1.0

- M5: Integration test for full sync cycle (controller + agent in kind)

## v0.2.0 — Reliability

Focus on observability, conflict handling, and recovery.

- Prometheus metrics for controller (reconcile duration, ref resolution latency, error counts)
- Prometheus metrics for agent (sync duration, files changed, error counts)
- Conflict detection when multiple profiles map to the same destination path
- Exponential backoff for transient git errors
- Post-sync verification against Ignition REST API
- Sync diff report in changes ConfigMap
- SSH host key verification warning (currently `InsecureIgnoreHostKey` with no warning)
- K8s informer-based ConfigMap watch (replace 3s polling with scoped informer)
- In-flight sync completion deadline (30s) on shutdown
- Max rendered template path length check (4096 chars)

## v0.3.0 — Observability & Conditions

Focus on condition types, multi-tenancy, and dependency ordering.

- New condition types: `AgentReady`, `RefSkew`, `DependenciesMet`
- `RefSkew` detection (controller detects gateway `syncedRef` drift from CR `lastSyncRef`)
- `DependenciesMet` condition enforcement for `dependsOn` profiles
- Downward API annotation reader (enables ref-override and profile switching without pod restart)
- Ref override support via `stoker.io/ref-override` annotation
- Per-gateway sync status conditions on the Stoker CR
- Namespace-scoped agent RBAC automation (controller creates RoleBindings)
- Resource quotas and rate limiting for concurrent syncs
- Audit logging for all sync operations

## v0.4.0+ — Enterprise & Future Considerations

- Rollback support: snapshot `/ignition-data/` before sync, revert on failure
- Bidirectional sync: watch gateway filesystem for designer changes, push back to git as PRs
- Deployment strategy: canary rollouts with staged gateway selectors and auto-rollback
- External validation webhook: call external HTTP endpoint before applying a sync
- Config normalization: in-file content replacement (e.g., `systemName` in `config.json` via JSON path)
- Sync history in status: bounded list of recent sync results per gateway
- GitHub App authentication (installation token refresh, repository-scoped access)
- Drift detection: periodic comparison of live gateway state vs. Git
- Approval gates: require manual approval before syncing to production gateways
- Multi-cluster support via hub-spoke model
- Designer session awareness: Perspective sessions, redundancy roles, session policies
- Tag provider and database connection sync via Ignition REST API
- Web UI dashboard for sync status visualization
