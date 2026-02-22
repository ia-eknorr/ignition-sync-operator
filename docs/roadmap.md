# Roadmap

## v0.1.0 — MVP

The minimum viable release: controller + agent sidecar can sync Ignition gateway
configuration from a Git repository. End-to-end flow works but operational polish
is limited.

### Completed

- IgnitionSync CRD with git ref resolution via `ls-remote`
- SyncProfile CRD with declarative file mappings and deployment mode overlays
- Agent sidecar with sync engine (clone, staging, merge, orphan cleanup)
- MutatingWebhook for automatic sidecar injection
- Gateway discovery via pod annotations
- Status aggregation from agent ConfigMaps
- Webhook receiver for push-event-driven sync
- CI/CD: release workflow (Docker images + Helm chart OCI push)
- Helm chart with cert-manager TLS, agent image configuration, agent RBAC

### Remaining for v0.1.0

- M1: Webhook receiver HMAC signature validation (currently accepts all requests)
- M2: Agent Dockerfile health endpoint (liveness/readiness for the sidecar)
- M3: Structured logging alignment (controller uses `logr`, agent should match)
- ~~M4: Helm chart values documentation via helm-docs~~ — done (PR #27)
- M5: Integration test for full sync cycle (controller + agent in kind)

## v0.2.0 — Reliability

Focus on observability, conflict handling, and recovery.

- Prometheus metrics for controller (reconcile duration, ref resolution latency, error counts)
- Prometheus metrics for agent (sync duration, files changed, error counts)
- Conflict detection when multiple profiles map to the same destination path
- Rollback support: agent can revert to previous commit on sync failure
- Exponential backoff for transient git errors
- Agent health checks against Ignition REST API after sync
- Sync diff report in changes ConfigMap

## v0.3.0 — Multi-tenancy and Ordering

Focus on multi-team usage and dependency ordering.

- Namespace-scoped agent RBAC automation (controller creates RoleBindings)
- SyncProfile `dependsOn` enforcement (wait for upstream profile sync before downstream)
- Per-gateway sync status conditions on the IgnitionSync CR
- Resource quotas and rate limiting for concurrent syncs
- Audit logging for all sync operations

## v0.4.0+ — Enterprise

- GitHub App authentication (installation token refresh, repository-scoped access)
- Drift detection: periodic comparison of live gateway state vs. Git
- Approval gates: require manual approval before syncing to production gateways
- Multi-cluster support via hub-spoke model
- Designer session awareness: defer sync when active designer sessions detected
- Tag provider and database connection sync via Ignition REST API
- Web UI dashboard for sync status visualization
