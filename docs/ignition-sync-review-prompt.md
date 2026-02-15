# Ignition Sync Operator — Architecture Review Prompt

You are reviewing and improving an architecture document for the **Ignition Sync Operator**, a Kubernetes operator being designed as a first-class product by Inductive Automation. It will be published alongside the `ignition` Helm chart at `charts.ia.io` and replaces the current `git-sync` sidecar pattern.

## Your Task

Dispatch **6 specialized agents in parallel** to review the architecture document from different expert perspectives. Each agent should deeply explore the codebase files listed below, understand the current implementation, read the architecture document, and produce structured findings. After all agents complete, dispatch a **7th moderator agent** that synthesizes the best findings from all 6 into a revised architecture document.

---

## Context

### What is Ignition?
Ignition is an industrial automation platform (SCADA/HMI/IIoT) made by Inductive Automation. It runs as a Java-based "gateway" server that manages tags, projects, alarm pipelines, Perspective (web UI) sessions, and industrial protocols (OPC-UA, MQTT Sparkplug via Cirrus Link modules). Gateways come in two types: **Standard** (full-featured, site/HQ) and **Edge** (lightweight, area-level). In Kubernetes, each gateway runs as a StatefulSet with a data PVC at `/ignition-data/`.

### What is the current git-sync pattern?
Today, the `registry.k8s.io/git-sync/git-sync:v4.4.0` image is used as a sidecar/init container on each Ignition gateway pod. A custom shell script (`sync-files.sh`) runs inside the sidecar to copy files from the cloned git repo to the Ignition data directory, apply exclude patterns, normalize configs (systemName via sed), sync shared resources (scripts, UDTs, external resources), and trigger Ignition's scan API. This pattern has significant limitations — see the architecture document's "Problem Statement" section.

### What is the Ignition Sync Operator?
The proposed replacement: a purpose-built K8s operator with a CRD (`IgnitionSync`), a controller manager, a mutating admission webhook for sidecar injection, and a lightweight sync agent. It should be production-ready, cloud-agnostic, feature-rich, and designed as a standalone product (not tied to any specific deployment like ProveIt).

### Key deployment patterns to understand:
1. **ProveIt 2026** — 5 gateways per site namespace (1 site + 4 areas), using the `ignition` chart as a dependency with aliases. ArgoCD + Kargo for GitOps promotion.
2. **Public Demo** — 2 gateways (frontend with 5 replicas + backend), different naming convention (`ignition-frontend`/`ignition-backend`).
3. **Simple deployments** — Single gateway with a single repo.

---

## Files to Read

### Architecture Document (the file under review):
- `~/ignition-sync-operator-architecture.md`

### Current git-sync implementation (CRITICAL — understand what exists today):
- `~/IA/code/projects/conf-proveit26-platform/charts/site/scripts/sync-files.sh` — The main sync script. 230+ lines of bash. Pay close attention to `mapGatewayTypeToPath()`, `syncFiles()`, `applyExcludePatterns()`, `normalizeConfigs()`, `triggerScan()`, and how shared resources are handled.
- `~/IA/code/projects/conf-proveit26-platform/charts/site/scripts/sync-entrypoint.sh` — The entrypoint that wraps git-sync's exec hook model.
- `~/IA/code/projects/conf-proveit26-platform/charts/site/values.yaml` — The site chart values. Look at the `x-git-sync` YAML anchors, `x-common` volumes, git-sync env vars, area defaults, and how 5 gateways share config. This file IS the current user experience — the operator must be simpler than this.
- `~/IA/code/projects/conf-proveit26-platform/charts/site/templates/git-sync-configmap.yaml` — How ConfigMaps are generated per-gateway today.
- `~/IA/code/projects/conf-proveit26-platform/charts/site/Chart.yaml` — Shows 5 ignition chart dependencies (site + area1-4).

### App repo structure (what the operator syncs):
- `~/IA/code/projects/conf-proveit26-app/services/` — Gateway-specific directories: `hq/`, `site/`, `area/`. Each has `projects/` and `config/resources/`.
- `~/IA/code/projects/conf-proveit26-app/shared/` — Shared resources: `scripts/`, `udts/`, `modules/`, `config/`.
- Browse `~/IA/code/projects/conf-proveit26-app/services/site/` and `~/IA/code/projects/conf-proveit26-app/services/area/` to understand what gateway-specific content looks like (projects, config resources, deployment modes).

### Alternative deployment pattern (Public Demo — different from ProveIt):
- `~/IA/code/projects/publicdemo-k8s/charts/public-demo/` — 2-gateway chart (frontend/backend), 5 frontend replicas.
- `~/IA/code/projects/publicdemo-all/services/` — `ignition-frontend/` and `ignition-backend/` naming.

### Platform infrastructure (for understanding ArgoCD/Kargo/Keycloak integration):
- `~/IA/code/projects/conf-proveit26-platform/` — Browse the top-level structure to understand the GitOps promotion pipeline, ApplicationSets, Kargo stages, and how `git.ref` flows through the system.

---

## Agent Personas

Dispatch these 6 agents **in parallel**. Each agent must read the architecture document AND the relevant codebase files before producing findings.

### Agent 1: K8s Operator Best Practices Engineer

**Focus:** Does this operator follow established Kubernetes operator patterns? Compare against cert-manager, external-secrets-operator, Flux, ArgoCD, Istio, and other production operators.

**Review checklist:**
- CRD design: Is `v1alpha1` the right starting point? Is the CRD too monolithic? Should it be split into multiple CRDs (like Flux splits `GitRepository`, `Kustomization`, `HelmRelease`)?
- Status reporting: Are conditions implemented correctly per the [K8s API conventions](https://github.com/kubernetes/community/blob/master/contributors/dede/sig-architecture/api-conventions.md)? Is `observedGeneration` used properly?
- Controller patterns: Leader election, exponential backoff, requeue strategies, watch predicates, informer caching — are these all correct?
- Webhook design: Is the mutating webhook separate from the controller (cert-manager pattern)? Is `failurePolicy: Ignore` appropriate? How does cert-manager manage the TLS certs?
- RBAC: Is the ClusterRole minimal? Should it support namespace-scoped Roles for restricted environments?
- Finalizers: Are they needed for cleanup (PVC deletion, git repo cleanup)?
- Conversion webhooks: How will CRD versioning work (v1alpha1 → v1beta1 → v1)?
- Helm chart: Does the operator chart follow best practices? CRDs in `crds/` directory? Proper upgrade story?
- Owner references and garbage collection: Are PVCs properly owned by the CR?
- Event recording: Are Kubernetes Events emitted for important state transitions?
- Multi-tenancy: Can one controller safely manage CRs from different teams/namespaces?

**Deliver:** A prioritized list of deviations from best practices, with specific code/YAML examples showing the correct pattern.

---

### Agent 2: Ignition Platform Expert

**Focus:** Does the operator actually understand Ignition's runtime behavior? Read the sync-files.sh script carefully — it contains hard-won knowledge about how Ignition works on disk.

**Review checklist:**
- **Scan API behavior:** The current script calls `POST /data/api/v1/scan/projects` and `POST /data/api/v1/scan/config`. Is this API synchronous or async? What happens if it's called during an active Designer session? What HTTP status codes does it return? Does the architecture account for scan failures?
- **`.resources/` directory:** This is Ignition's runtime resource directory. The current script explicitly preserves it during sync (`rm -rf` skips `.resources/`). Does the architecture document protect this directory? If the operator rsyncs with `--delete`, it would destroy `.resources/` — this would be catastrophic.
- **`config-mode.json`:** The script creates a fallback `config-mode.json` in external resources if the directory doesn't exist. What is this file? Why does it need to exist? Does the operator handle this edge case?
- **Tag provider paths:** UDTs are synced to `config/resources/core/ignition/tag-type-definition/{tagProvider}/`. The tag provider name varies per gateway type (e.g., `default` for site, `edge` for areas). Is this flexible enough?
- **Project structure:** Each project lives in `projects/{projectName}/`. Projects contain module-specific subdirectories like `com.inductiveautomation.perspective/views/` and `ignition/script-python/`. Does the operator understand this hierarchy?
- **Gateway types:** Standard vs Edge gateways have different capabilities. Edge can't run certain modules. Does the operator account for this?
- **MQTT Engine/Transmission:** These Cirrus Link modules create tag providers that should NOT be synced from git (they're managed by the module runtime). The current exclude patterns handle this (`**/tag-*/MQTT Engine/`). Is this documented in the architecture?
- **Redundancy:** Ignition supports primary/backup gateway pairs. What happens if you sync both independently?
- **Gateway Network:** HQ gateways use the Gateway Network to communicate with site gateways. Config changes on HQ can cascade. Is this considered?
- **Ignition 8.3 specifics:** Are there any Ignition 8.3-specific behaviors the operator should handle differently from 8.1?

**Deliver:** A list of Ignition-specific gotchas, with references to specific lines in sync-files.sh and the app repo structure that prove each point.

---

### Agent 3: Security & Compliance Engineer

**Focus:** Is this operator safe to deploy in an industrial automation environment? Think IEC 62443, NIST 800-82, SOC 2.

**Review checklist:**
- **Supply chain:** Are container images signed (COSIGN)? Is there an SBOM? Are image digests pinned?
- **Secrets:** Read sync-files.sh — it exports `IGNITION_API_KEY` as an environment variable (line ~35). This is visible via `/proc/{pid}/environ`. The architecture document must not repeat this mistake. Are secrets ever written to disk or PVC in plaintext?
- **Network isolation:** What NetworkPolicies are needed? The webhook must only accept traffic from the K8s API server. The controller needs egress to git hosts only. Agents need localhost access to Ignition only.
- **Shared PVC attack surface:** If a rogue pod mounts the shared PVC read-write, it can poison the repo content, write fake status files, or exfiltrate git credentials. How is this prevented?
- **Mutating webhook blast radius:** If an attacker adds `ignition-sync.io/inject: "true"` to any pod, the webhook injects a sidecar with PVC access. The webhook must validate that the pod is actually an Ignition gateway (check labels, owner references).
- **Emergency stop:** Is there a kill switch to halt all syncing immediately? What if a malicious commit is pushed to git?
- **Audit trail:** Every sync must be logged with who triggered it, what ref, what changed, and the result. Is this sufficient for compliance?
- **Runtime hardening:** Non-root containers, read-only root filesystem, dropped capabilities, seccomp profiles.
- **Data integrity:** Are there checksums to verify files weren't tampered with between git and gateway?
- **Bi-directional risks:** If enabled, bi-directional sync lets any gateway operator create PRs in the app repo. What guardrails exist?

**Deliver:** A severity-rated findings list (Critical/High/Medium/Low) with attack scenarios and mitigations.

---

### Agent 4: Developer Experience & API Design Engineer

**Focus:** Is this operator pleasant to use? Is the CRD intuitive? Is the migration path from git-sync realistic?

**Review checklist:**
- **CRD ergonomics:** Read the full CRD spec in the architecture document. Is it too verbose? Too many required fields? Could sensible defaults eliminate 50% of the YAML? Compare to how ArgoCD's `Application` CRD or Flux's `GitRepository` CRD feel.
- **Annotation overload:** There are 10+ annotations listed. Is this too many? Could some be derived from the CRD or pod labels instead of requiring explicit annotation?
- **Migration path:** The document shows a before/after comparison. Is this realistic? Read the current `values.yaml` — can a user actually migrate in one PR, or are there hidden dependencies?
- **Error messages:** When something goes wrong (bad git ref, scan API failure, PVC full), what does the user see? Is it a cryptic condition, or a helpful error message?
- **kubectl experience:** The `additionalPrinterColumns` in the CRD — are they useful? Can you quickly diagnose problems from `kubectl get ignitionsyncs -A`?
- **Documentation:** Would a new user understand how to set this up from the architecture document alone? What's missing?
- **Local development:** Is there a story for developing against this operator locally? Can you test sync behavior without a full K8s cluster?
- **Onboarding:** How does someone go from "I have an Ignition gateway in K8s" to "I'm using the sync operator" in < 30 minutes?

**Deliver:** A UX-focused review with specific suggestions for simplifying the CRD, improving error messages, and streamlining onboarding.

---

### Agent 5: Systems Architect (Scale & Extensibility)

**Focus:** Will this operator work at scale (100+ sites, 500+ gateways) and be extensible for 3-5 years?

**Review checklist:**
- **PVC as communication bus:** The shared PVC carries repo content AND metadata (trigger files, status files, change manifests). At 50+ gateways, is this a bottleneck? What's the I/O pattern on NFS/EFS?
- **Trigger mechanism:** The architecture mentions both inotify on trigger files AND K8s ConfigMap signaling. Which is primary? Are they redundant? Could one be removed?
- **Multi-repo support:** One controller handles N CRs across N namespaces, each with its own repo and PVC. What's the memory footprint? Does the controller cache all git clones?
- **Plugin architecture:** Can users add custom pre-sync/post-sync logic without forking the operator? Custom content transformers? Custom webhook formats?
- **Source abstraction:** The operator is tightly coupled to git. Could it sync from OCI registries, S3, or HTTP endpoints in the future? Is there a source abstraction layer?
- **CRD evolution:** The CRD is monolithic (git config + storage + gateway + sync rules + bidirectional all in one resource). Should it be split? How does versioning work?
- **Config inheritance:** At 100+ sites, each site duplicates 90% of config. Is there a base/override inheritance model?
- **Rate limiting:** What happens when 100 CRs all receive webhooks simultaneously? Is there a sync queue or rate limiter?
- **Agent versioning:** Can you upgrade the sync agent image without restarting all gateways? Can you canary-roll a new agent version?
- **Observability at scale:** Do Prometheus metrics have proper cardinality? Are there dashboards?
- **Air-gapped deployments:** Can this work without internet access? Local git mirrors, internal registries?

**Deliver:** A scalability analysis with concrete failure modes at 10, 50, 100, and 500 gateways, plus extensibility recommendations.

---

### Agent 6: Legacy Codebase Auditor

**Focus:** Read the CURRENT implementation deeply and find things that the architecture document gets wrong, misses, or oversimplifies. The current codebase has battle-tested logic that must not be lost.

**Review checklist — read these files line by line:**

**sync-files.sh:**
- `mapGatewayTypeToPath()` (lines ~41-52): Hardcoded `site`/`area*` mapping. The operator claims to replace this with `servicePath` annotations. But the current function also handles error cases — does the operator?
- `syncFiles()` (lines ~113-197): The staging/swap logic. Note the specific ORDER of operations: create staging dir → copy projects → copy core config → copy deployment mode overlay → copy shared resources → apply excludes → normalize → atomic swap → trigger scan. Does the operator's agent sync flow match this exact order? Missing a step could break Ignition.
- `applyExcludePatterns()` (lines ~54-82): Uses bash globbing with `shopt -s globstar nullglob`. The operator uses rsync `--exclude-from`. Are the semantics identical? Bash `**` glob and rsync exclude patterns behave differently.
- `normalizeConfigs()` (lines ~84-111): Finds ALL `config.json` files recursively and replaces `systemName` via sed. The operator proposes jq. But note: the current script processes EVERY config.json, not just the top-level one. Does the operator do this?
- `triggerScan()` (lines ~205-232): Calls TWO endpoints: `/data/api/v1/scan/projects` AND `/data/api/v1/scan/config`. Uses `IGNITION_API_KEY` for auth. Has retry logic with `MAX_RETRIES=3`. Skips scan on initial sync (`INITIAL_SYNC_DONE` flag). Does the operator replicate all of this behavior?
- The `INITIAL_SYNC_DONE` flag (line ~102): On first sync after pod startup, the script does NOT trigger the scan API because Ignition auto-scans on startup. Does the operator handle this?
- External resources fallback (lines ~148-166): If the external resources directory doesn't exist in the repo, the script creates a minimal one with `config-mode.json`. This is critical — without it, Ignition fails to start properly. Does the operator do this?

**values.yaml:**
- YAML anchors `x-git-sync` and `x-common`: These define the init container and volumes shared across all 5 gateways. The operator must be simpler than this. Count the lines of YAML the user writes today vs what the operator requires.
- `siteExcludePatterns` and `areaExcludePatterns`: Different exclude patterns per gateway type. The operator puts these in annotations. Is comma-separated annotation values the right UX for complex glob patterns?
- `git.ref` and `git.repo`: Currently managed by Kargo (updates `values/site-common/{env}/values.yaml`). The operator's webhook receiver replaces this. But does Kargo also need to update the `IgnitionSync` CR's `spec.git.ref`? How does this integrate?
- The `factoryConfig` section: There's a `factory-config.json` synced as an additional file. The operator has `additionalFiles` for this. But note the current implementation copies it to the Ignition data root, not into a project. Does the operator's `additionalFiles.dest` path handle this correctly?

**git-sync-configmap.yaml:**
- This template generates 5 ConfigMaps (one per gateway) with environment variables. Each has `GATEWAY_TYPE`, `GATEWAY_NAME`, `SITE_NUMBER`, exclude patterns, etc. The operator replaces this with annotations. Verify the mapping is complete — no env var should be lost.

**App repo structure:**
- `services/site/config/resources/` has subdirectories like `core/`, `prd-cloud/`, `dev-cloud/`. The deployment mode overlay mechanism copies from the mode-specific directory ON TOP of core. Does the operator's sync flow handle overlays correctly, or does it treat them as separate syncs?
- `services/area/` is a TEMPLATE — all area gateways (area1-4) share this same directory. The `systemName` normalization is what makes each instance unique. If normalization fails, all areas would have the same systemName — they'd collide on the Gateway Network.
- `shared/scripts/` contains Python scripts synced to a deeply nested path inside each project. The destination path (`ignition/script-python/exchange/proveit2026`) is project-specific. The operator has `shared.scripts.destPath` for this. But what if different projects need different script paths?
- `shared/udts/` is synced per tag provider. The provider name differs between site (`default`) and area (`edge`). The current script handles this. Does the operator's annotation `ignition-sync.io/tag-provider` work correctly for this?
- `shared/modules/` — module JARs. These go to `user-lib/modules/`. The current sync-files.sh does NOT handle this (it's done separately). Should the operator handle module installation?

**Deliver:** A line-by-line audit of functionality that the architecture document must account for, with specific "the operator says X but the current code does Y" comparisons.

---

## Agent 7: Moderator (run AFTER agents 1-6 complete)

**Input:** All findings from agents 1-6.

**Task:**
1. Read all 6 agents' findings.
2. Read the current architecture document.
3. Categorize findings into: **Must Fix (v1)**, **Should Add (v1)**, **Nice to Have (v1.1+)**, and **Reject (not applicable or over-engineered)**.
4. For "Reject" items, explain why they're being excluded.
5. Update the architecture document at `~/ignition-sync-operator-architecture.md` with all accepted findings. Add new sections, enhance existing ones, fix errors.
6. Ensure the document remains readable and well-organized — don't let it become a dump of ideas.
7. Add a "Review Changelog" section at the bottom listing every significant change made and which agent suggested it.

**Rules for the moderator:**
- Don't add features just because they're cool. Every addition must serve a real use case.
- Don't over-engineer. This is v1 of a product, not a thesis project.
- Do fix every genuine error (especially Ignition-specific gotchas from Agent 2 and Agent 6).
- Do add security controls that are standard for K8s operators (from Agent 3).
- Do simplify the CRD if Agent 4 identifies unnecessary complexity.
- Do document extension points even if they're not implemented in v1 (from Agent 5).
- Preserve the existing document's voice and formatting style.

---

## Output

The final deliverable is an updated `~/ignition-sync-operator-architecture.md` that:
1. Fixes all errors and oversimplifications identified by the agents
2. Adds missing Ignition-specific knowledge
3. Follows K8s operator best practices
4. Has a comprehensive security section
5. Has a clear, intuitive CRD design
6. Accounts for scale and extensibility
7. Includes a changelog of all changes made during this review
