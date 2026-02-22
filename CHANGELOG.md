# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and this project adheres to [Semantic Versioning](https://semver.org/).

## [v0.1.2] - 2026-02-22

### Fixed

- Controller failed to match gateway status from ConfigMap when `stoker.io/gateway-name` annotation was unset, causing gateways to stay `Pending` indefinitely

## [v0.1.1] - 2026-02-22

### Fixed

- Webhook unconditionally mounted a `git-credentials` secret volume even when `spec.git.auth` was nil, causing pods using public repos to get stuck in Init

## [v0.1.0] - 2026-02-22

Initial release — controller + agent sidecar for Git-driven Ignition gateway configuration sync.

### Added

- **Stoker CRD** (`stoker.io/v1alpha1`) with git ref resolution via `ls-remote`, polling configuration, and gateway connection settings
- **SyncProfile CRD** with declarative source-to-destination file mappings, glob patterns, deployment mode overlays, template variables, and `dependsOn` ordering
- **Sync agent** with 3-layer architecture (syncengine → agent → ignition): clone/fetch, staged file sync with orphan cleanup, Ignition scan API integration
- **Mutating webhook** for automatic sidecar injection into annotated pods (native sidecar pattern, K8s 1.28+)
- **Gateway discovery** via pod annotations with status aggregation from agent ConfigMaps
- **Webhook receiver** (`POST /webhook/{namespace}/{crName}`) with auto-detection of GitHub release, ArgoCD, Kargo, and generic payloads; HMAC signature validation
- **Designer session detection** with configurable policy (`proceed`, `wait`, `fail`)
- **Dry-run mode** on SyncProfile for diffing without writing to gateway
- **Pause support** on both Stoker CR and SyncProfile levels
- **Helm chart** with cert-manager TLS, agent RBAC, configurable agent image, and helm-docs generated README
- **CI/CD**: lint, test, and release workflows; multi-arch Docker image builds (amd64/arm64)
- **Functional test suite** with phased kind cluster tests (phases 02-09)
- Unit tests with envtest for controller and syncengine

[v0.1.2]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.2
[v0.1.1]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.1
[v0.1.0]: https://github.com/ia-eknorr/stoker-operator/releases/tag/v0.1.0
