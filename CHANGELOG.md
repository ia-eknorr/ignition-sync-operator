# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and this project adheres to [Semantic Versioning](https://semver.org/).

## [v0.4.0] - 2026-02-27

### Added

- **Content templating** (`template: true`) — resolve Go template variables (`{{.GatewayName}}`, `{{.PodName}}`, `{{.Vars.key}}`, etc.) inside file **contents** at sync time, without modifying source files in git; binary files (null bytes) are rejected with a clear error (#82)
- **`vars` in `spec.sync.defaults`** — define default template variables shared across all profiles; profile `vars` override per-key (#82)
- **`{{.PodName}}` in TemplateContext** — enables unique system names for StatefulSet replicas with ordinal-suffix pod names (#82)
- **JSON path patches** — per-mapping `patches` blocks that set specific JSON fields at sync time using sjson dot-notation paths; patch values support Go template syntax; `file` field supports doublestar globs; type inference from filesystem when `type` field is omitted (#82)

### Changed

- **GitHub App tokens moved to dedicated Secret** — installation tokens are now written to `stoker-github-token-{crName}` (a controller-managed Secret) and mounted into agent pods; tokens are no longer stored in the metadata ConfigMap (#82)

### Fixed

- Agent now re-syncs when CR profiles change (new patches, vars, or mappings) even if the git commit has not changed; previously a profile change without a new commit was ignored until the next pod restart (#82)

## [v0.3.0] - 2026-02-25

### Breaking Changes

- **Renamed `gateway.apiKeySecretRef`** to `gateway.api.secretName` / `gateway.api.secretKey` — `secretKey` defaults to `"apiKey"` when omitted, reducing boilerplate (#79)
- Gateway port default changed from `8043` to `8088` and TLS default changed from `true` to `false`, matching Ignition Helm chart defaults (#76)
- Webhook receiver disabled by default — enable via `webhookReceiver.enabled: true` in Helm values (#76)

### Added

- **GitHub App authentication** — controller exchanges PEM for short-lived installation tokens with per-CR cache and 5-minute pre-expiry refresh; PEM never leaves controller namespace; supports GitHub Enterprise Server via `apiBaseURL` field (#76)

## [v0.2.0] - 2026-02-24

### Breaking Changes

- **Merged `SyncProfile` into `GatewaySync` CRD** — sync profiles are now embedded at `spec.sync.profiles` instead of a separate CRD; `spec.sync.defaults` provides inheritable baseline settings (#51)
- Removed `deploymentMode` field from sync profile spec (#48)
- Namespace injection label (`stoker.io/injection=enabled`) now optional, disabled by default — injection requires only the `stoker.io/inject: "true"` pod annotation (#64)

### Added

- **Automatic agent RBAC** — controller creates Role/RoleBinding for the agent ServiceAccount in each target namespace (#68)
- **Chainsaw e2e test suite** replacing shell functional tests with declarative Kyverno Chainsaw tests against a real kind cluster (#47, #50)
- **Documentation site** — Docusaurus-based docs with quickstart, guides (multi-gateway, webhook sync, git auth), and CRD reference (#41, #63, #67)

### Fixed

- Controller defers secret validation until after ref resolution, avoiding false errors on public repos (#58)
- Dry-run mode now reports `Synced` status on success instead of staying `Pending` (#59)
- Webhook writes discovered `cr-name` annotation back to pod spec (#60)
- Agent respects profile-level `syncPeriod` from metadata ConfigMap (#61)
- Suppress `NotFound` error log on CR deletion race (#62)

### Changed

- Quickstart: cert-manager moved to prerequisites, added example repo context (#71)
- Cleaned up CI workflow names and e2e trigger strategy (#66)
- Removed stale design docs, scripts, and assets (#70)

## [v0.1.2] - 2026-02-22

### Fixed

- Controller failed to match gateway status from ConfigMap when `stoker.io/gateway-name` annotation was unset, causing gateways to stay `Pending` indefinitely

## [v0.1.1] - 2026-02-22

### Fixed

- Webhook unconditionally mounted a `git-credentials` secret volume even when `spec.git.auth` was nil, causing pods using public repos to get stuck in Init

## [v0.1.0] - 2026-02-22

Initial release — controller + agent sidecar for Git-driven Ignition gateway configuration sync.

### Added

- **GatewaySync CRD** (`stoker.io/v1alpha1`) with git ref resolution via `ls-remote`, polling configuration, gateway connection settings, and embedded sync profiles with declarative source-to-destination file mappings, glob patterns, and template variables
- **Sync agent** with 3-layer architecture (syncengine → agent → ignition): clone/fetch, staged file sync with orphan cleanup, Ignition scan API integration
- **Mutating webhook** for automatic sidecar injection into annotated pods (native sidecar pattern, K8s 1.28+)
- **Gateway discovery** via pod annotations with status aggregation from agent ConfigMaps
- **Webhook receiver** (`POST /webhook/{namespace}/{crName}`) with auto-detection of GitHub release, ArgoCD, Kargo, and generic payloads; HMAC signature validation
- **Designer session detection** with configurable policy (`proceed`, `wait`, `fail`)
- **Dry-run mode** per profile for diffing without writing to gateway
- **Pause support** at defaults and per-profile levels
- **Helm chart** with cert-manager TLS, agent RBAC, configurable agent image, and helm-docs generated README
- **CI/CD**: lint, test, and release workflows; multi-arch Docker image builds (amd64/arm64)
- **Functional test suite** with phased kind cluster tests (phases 02-09)
- Unit tests with envtest for controller and syncengine

[v0.4.0]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.4.0
[v0.3.0]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.3.0
[v0.2.0]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.2.0
[v0.1.2]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.2
[v0.1.1]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.1
[v0.1.0]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.0
