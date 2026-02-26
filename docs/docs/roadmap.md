---
sidebar_position: 10
title: Roadmap
description: Planned features and milestones for Stoker.
---

# Roadmap

Current version: **v0.4.0** — [see the changelog](https://github.com/ia-eknorr/stoker-operator/blob/main/CHANGELOG.md) for release history.

## v0.4.0 — Content Templating & Security

Multi-site GitOps and security hardening — the prerequisites for production multi-gateway deployments.

- ✅ **Content templating** (`template: true`) — resolve `{{.GatewayName}}`, `{{.PodName}}`, `{{.Vars.key}}`, and all context variables inside file **contents** at sync time; binary files rejected with a clear error
- ✅ **`vars` in `spec.sync.defaults`** — define default template variables shared across all profiles; profile `vars` override per-key
- ✅ **`{{.PodName}}` in TemplateContext** — enables unique system names for StatefulSet replicas
- ✅ **GitHub App tokens → Secret** — installation tokens written to a controller-managed Secret (`stoker-github-token-{crName}`) and mounted into agent pods; no longer stored in ConfigMap
- Prometheus metrics for controller (reconcile duration, ref resolution latency, gateway counts, error rates)
- Prometheus metrics for agent (sync duration, files changed, git fetch duration, error counts) with dedicated metrics endpoint
- Grafana dashboard JSON shipped in Helm chart
- SSH host key verification with optional `knownHostsSecretRef` (fix `InsecureIgnoreHostKey`)
- Exponential backoff for transient git and API errors (30s → 60s → 120s → 5m cap)

## v0.5.0 — Config Transforms

Deep config transformation without modifying source files in git.

- **JSON path patches** — per-mapping patches that modify specific values in JSON files at sync time using jq-style paths (e.g., `.systemName`, `.remoteGateways[0].host`); patch values are templates that resolve against `{{ .Vars.* }}`, `{{ .GatewayName }}`, `{{ .PodName }}`, and pod labels; source files stay unmodified in git
- Designer session project-level granularity (sync Project B while designer has Project A open)

## v0.6.0 — Scale & Operability

Remove scaling walls and make the agent more reactive.

- Informer-based ConfigMap watch replacing 3s polling in agent
- Downward API annotation reader — enables `stoker.io/ref-override` and profile switching without pod restart
- Per-gateway status ConfigMap sharding (eliminate write contention at 10+ gateways)
- `emptyDir` size limit on agent repo volume (prevent node disk pressure from large repos)
- Webhook receiver rate limiting
- In-flight sync completion deadline on graceful shutdown

## v0.7.0 — Observability & Conditions

Operational visibility for fleet management.

- New condition types: `AgentReady`, `RefSkew`
- Drift detection (re-sync same commit reports unexpected changes)
- Post-sync health verification (project state, tag providers — not just scan 200)
- Sync diff report in changes ConfigMap
- Conflict detection when multiple profiles map to the same destination path
- Validating admission webhook for GatewaySync CRs (reject invalid CRs at apply time)
- Structured audit logging (per-sync JSON record: timestamp, commit, author, gateway, files, result)

## Future Ideas

These are valuable but not yet scoped into versioned milestones. They'll be prioritized based on user feedback.

**Safety & Trust:**
- Pre-sync backup with auto-rollback on scan failure
- Module management (`.modl` sync to `modules/` with `postAction: restart`)
- Per-CR webhook HMAC secrets (replace global HMAC)
- Git commit signature verification (GPG/SSH, IEC 62443 compliance)

**Reach:**
- Standalone agent mode (systemd/Windows service for bare-metal Ignition servers)
- Approval annotation gate for production gateways

**Enterprise:**
- Maintenance windows and change freeze schedules
- External audit sink (SIEM integration via webhook/syslog)
- Drift detection with configurable action (report / restore / alert)
- Resource quotas and rate limiting for concurrent syncs
