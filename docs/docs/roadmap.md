---
sidebar_position: 10
title: Roadmap
description: Planned features and milestones for Stoker.
---

# Roadmap

## v0.1.0 — MVP ✓

Controller + agent sidecar for Git-driven Ignition gateway configuration sync. GatewaySync CRD, mutating webhook for sidecar injection, webhook receiver, designer session awareness, Helm chart with cert-manager TLS.

## v0.2.0 — Stability ✓

CRD consolidation, bug fixes, and developer experience.

- Merged `SyncProfile` into `GatewaySync` CRD as embedded profiles
- Automatic agent RBAC binding via controller
- Namespace injection label optional (default off)
- 5 bug fixes across controller, agent, and webhook
- Documentation site with quickstart, guides, and CRD reference
- Chainsaw e2e test suite replacing shell functional tests

## v0.3.0 — Reliability

Focus on observability, conflict handling, and recovery.

- Prometheus metrics for controller (reconcile duration, ref resolution latency, error counts)
- Prometheus metrics for agent (sync duration, files changed, error counts)
- Conflict detection when multiple profiles map to the same destination path
- Exponential backoff for transient git errors
- K8s informer-based ConfigMap watch (replace polling with scoped informer)
- In-flight sync completion deadline on shutdown

## v0.4.0 — Observability & Conditions

Focus on condition types, multi-tenancy, and dependency ordering.

- New condition types: `AgentReady`, `RefSkew`, `DependenciesMet`
- `RefSkew` detection (controller detects gateway drift from CR)
- `DependenciesMet` condition enforcement for `dependsOn` profiles
- Downward API annotation reader (enables ref-override without pod restart)
- Per-gateway sync status conditions on the GatewaySync CR
- Resource quotas and rate limiting for concurrent syncs

## v0.5.0+ — Enterprise & Future

- Rollback support: snapshot before sync, revert on failure
- Bidirectional sync: watch gateway for designer changes, push back to git
- Deployment strategy: canary rollouts with staged gateway selectors
- External validation webhook before applying a sync
- Config normalization via JSON path replacement
- Drift detection: periodic comparison of live state vs. Git
- Approval gates for production gateways
- Multi-cluster support via hub-spoke model
- Web UI dashboard for sync status visualization
