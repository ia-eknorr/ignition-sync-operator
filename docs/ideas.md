# Feature Ideas — Future Consideration

Ideas captured from removed CRD types and architecture docs. These were defined in `v1alpha1` but never implemented. Preserved here so we don't lose the thinking if we decide to build them later.

---

## Bidirectional Sync (gateway → git)

**What:** Watch specific paths on the gateway filesystem for changes made by Ignition designers, then push those changes back to git as PRs.

**Why:** Designers make changes via Ignition Designer sessions (drag/drop views, edit scripts, configure tags). Today those changes are lost on pod restart unless manually committed. Bidirectional sync would capture them automatically.

**Design considerations:**
- Allowlisted watch paths (not everything should flow back to git)
- Debounce period to batch rapid changes before creating a PR
- Conflict strategy: git wins vs gateway wins vs manual resolution
- Guardrails: max file size, max files per PR, exclude patterns to prevent accidental data exfiltration
- Target branch should be configurable (e.g., `gateway-changes/{{.Namespace}}/{{.GatewayName}}`)
- Requires GitHub App auth (token auth can't create PRs)

**When to consider:** After v1 is stable and designer workflow feedback is collected from real users.

---

## Pre-Sync Snapshots & Rollback

**What:** Take a snapshot of the gateway's `/ignition-data/` before each sync. If the sync breaks the gateway, roll back to the snapshot.

**Why:** A bad commit could break tag providers, corrupt project configs, or cause Ignition to fail startup. Snapshots provide a safety net.

**Design considerations:**
- Storage backend: local PVC, S3, GCS
- Retention count (default: 5 snapshots)
- Snapshot size for large Ignition data dirs (could be hundreds of MB)
- Trigger: auto-snapshot before every sync, or only when the diff exceeds a threshold
- Rollback trigger: scan API failure, health check failure, manual annotation

**When to consider:** When deploying to production environments where sync failures have high blast radius.

---

## Deployment Strategy (canary rollouts)

**What:** Instead of syncing all gateways simultaneously, roll out changes in stages — canary gateway first, then broader rollout if healthy.

**Why:** At scale (50+ gateways), a bad commit synced to all gateways simultaneously could cause widespread outage. Canary rollouts limit blast radius.

**Design considerations:**
- Stages with gateway selectors (label-based)
- Sync ordering for dependency-aware rollout (e.g., HQ gateway before site gateways)
- Auto-rollback triggers: scan failure, health check degradation
- Pause between stages for verification
- Integration with SyncProfile `dependsOn` for cross-profile ordering

**When to consider:** When multi-site deployments (ProveIt scale: 100+ sites) are in production.

---

## External Validation Webhook

**What:** Call an external HTTP endpoint before applying a sync. The endpoint can accept or reject the change based on custom logic.

**Why:** Organizations may want to enforce policies (e.g., "no changes during maintenance windows", "all config changes must be approved in ServiceNow", "block syncs that remove safety-critical tags").

**Design considerations:**
- Request payload: ref, commit, diff summary, gateway list
- Response: accept/reject with reason
- Timeout with configurable deadline (default: 10s)
- Failure mode: fail-open or fail-closed (should be configurable)
- Could also support dry-run-before-sync as a simpler built-in validation

**When to consider:** When enterprise customers need compliance gates in the sync pipeline.

---

## Ignition Session Policies

**What:** Detect active Designer or Perspective sessions and adjust sync behavior accordingly.

**Design considerations:**
- **Designer sessions:** Syncing while a designer has a project open can cause conflicts. Options: wait for sessions to close, proceed anyway, or fail the sync. Ignition 8.3+ has a status endpoint that reports active designer sessions.
- **Perspective sessions:** Less risky than designer sessions, but syncing view changes while users are active could cause brief UI disruption. Options: wait, proceed (with warning), or ignore.
- **Redundancy role:** In primary/backup pairs, only sync to the active primary. The backup receives changes via gateway network replication, not direct sync.
- **Peer gateway name:** For redundancy-aware sync, know which gateway is the peer.

**When to consider:** Designer session detection should be one of the first post-v1 features — it prevents the most common real-world sync conflict.

---

## Config Normalization

**What:** Automatically replace fields in Ignition `config.json` files during sync. The classic use case is `systemName` — all area gateways share the same source directory, but each needs a unique system name.

**Why:** Without normalization, all area gateways sharing a template directory would have the same system name and collide on the Gateway Network.

**Current approach:** SyncProfile `vars` + Go template resolution handles the destination *path* templating. But in-file content replacement (like changing a JSON field value inside a `config.json`) is not yet implemented.

**Design considerations:**
- Use `jq`-style JSON path for field selection (e.g., `.systemName`)
- Value template using the same `{{.Vars.key}}` syntax as path templates
- Process ALL `config.json` files recursively (not just top-level — the current shell script does this)
- Could be a post-sync hook rather than part of the engine

**When to consider:** When multi-area deployments (shared template directories) are needed.

---

## CRD-Configurable Webhook Receiver

**What:** Allow users to configure the webhook receiver port, enable/disable it, and reference the HMAC secret via the IgnitionSync CRD spec.

**Current state:** The webhook receiver works but is hardcoded at controller startup. The port comes from a flag, the HMAC secret from an env var.

**Design considerations:**
- CRD fields: `webhook.enabled`, `webhook.port`, `webhook.secretRef`
- Per-CR webhook configuration vs global controller configuration
- Whether each CR should have its own webhook endpoint (current: `/webhook/{namespace}/{crName}`)

**When to consider:** If users need per-CR webhook configuration. The current hardcoded approach may be sufficient for most deployments.

---

## ~~Agent Image Configuration via CRD~~ (Implemented)

Implemented via 3-tier image resolution in the mutating webhook: pod annotation > CR `spec.agent.image` > env `DEFAULT_AGENT_IMAGE`. Supports repository, tag, pull policy, and resource limits/requests. See `internal/webhook/inject.go` and `api/v1alpha1/ignitionsync_types.go` (`AgentImageSpec`).

---

## Sync History in Status

**What:** Maintain a bounded list of recent sync results in the DiscoveredGateway status. Each entry records timestamp, commit SHA, result (success/error), and duration.

**Why:** Provides at-a-glance visibility into recent sync activity without needing to check logs or ConfigMaps.

**Design considerations:**
- Bounded list (e.g., last 10 entries) to avoid status bloat
- Written by controller from agent status ConfigMap data
- Useful for `kubectl describe ignitionsync` output
- Could also include the dry-run diff summary per sync

**When to consider:** After the core sync loop is stable and observability gaps are identified.
