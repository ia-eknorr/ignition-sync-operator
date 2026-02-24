---
sidebar_position: 10
title: Roadmap
description: Planned features and milestones for Stoker.
---

# Roadmap

## v0.1.0 — MVP

The minimum viable release: controller + agent sidecar can sync Ignition gateway configuration from a Git repository. End-to-end flow works but operational polish is limited.

### Completed

- GatewaySync CRD with git ref resolution via `ls-remote`
- Embedded sync profiles with declarative file mappings
- Agent sidecar with sync engine (clone, staging, merge, orphan cleanup)
- MutatingWebhook for automatic sidecar injection
- Gateway discovery via pod annotations
- Status aggregation from agent ConfigMaps
- Webhook receiver for push-event-driven sync
- CI/CD: release workflow (Docker images + Helm chart OCI push)
- Helm chart with cert-manager TLS, agent image configuration, agent RBAC
- Helm chart values documentation via helm-docs
- Webhook receiver HMAC signature validation
- Agent health endpoint (liveness/readiness for sidecar)
- Structured logging alignment (controller uses `logr`, agent matches)

### Remaining

- Integration test for full sync cycle (controller + agent in kind)

## v0.2.0 — Reliability

Focus on observability, conflict handling, and recovery.

- Prometheus metrics for controller (reconcile duration, ref resolution latency, error counts)
- Prometheus metrics for agent (sync duration, files changed, error counts)
- Conflict detection when multiple profiles map to the same destination path
- Exponential backoff for transient git errors
- Post-sync verification against Ignition REST API
- Sync diff report in changes ConfigMap
- K8s informer-based ConfigMap watch (replace polling with scoped informer)
- In-flight sync completion deadline on shutdown

## v0.3.0 — Observability & Conditions

Focus on condition types, multi-tenancy, and dependency ordering.

- New condition types: `AgentReady`, `RefSkew`, `DependenciesMet`
- `RefSkew` detection (controller detects gateway drift from CR)
- `DependenciesMet` condition enforcement for `dependsOn` profiles
- Downward API annotation reader (enables ref-override without pod restart)
- Per-gateway sync status conditions on the GatewaySync CR
- Namespace-scoped agent RBAC automation
- Resource quotas and rate limiting for concurrent syncs

## v0.4.0+ — Enterprise & Future

- Rollback support: snapshot before sync, revert on failure
- Bidirectional sync: watch gateway for designer changes, push back to git
- Deployment strategy: canary rollouts with staged gateway selectors
- External validation webhook before applying a sync
- Config normalization via JSON path replacement
- Drift detection: periodic comparison of live state vs. Git
- Approval gates for production gateways
- Multi-cluster support via hub-spoke model
- Designer session awareness
- Web UI dashboard for sync status visualization
