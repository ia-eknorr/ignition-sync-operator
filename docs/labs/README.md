# Functional Validation Labs

Manual step-by-step labs executed against a real kind cluster with actual Ignition gateways. These complement the automated `test/functional/` scripts by providing deeper, observational validation that exercises the operator alongside production-like Ignition deployments.

## Philosophy

The automated functional tests assert pass/fail on specific conditions. These labs go further:

- **Real Ignition gateways** via the official `inductiveautomation/ignition` helm chart — not pause containers
- **Visual verification** of the Ignition web UI (projects appearing, gateway status, scan results)
- **Log inspection** of both operator and Ignition gateway logs for unexpected errors
- **Edge case exploration** that scripted tests can't easily cover (timing, race conditions, resource pressure)
- **Feedback loops** — when something fails, we debug interactively before moving to the next phase

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| `kind` | v0.20+ | `brew install kind` |
| `kubectl` | v1.29+ | `brew install kubectl` |
| `helm` | v3.14+ | `brew install helm` |
| `docker` | 24+ | Docker Desktop |
| `jq` | 1.7+ | `brew install jq` |
| `curl` | any | pre-installed |

**Resource requirements:** Docker Desktop should have at least 6 GB memory and 4 CPUs allocated. Ignition + operator together need ~3 GB.

## Lab Structure

Each phase doc follows this structure:

1. **Objective** — What we're validating and why
2. **Setup** — Phase-specific resource creation
3. **Labs** — Numbered step-by-step procedures, each with:
   - Commands to run
   - Expected output descriptions
   - Pass/fail criteria
   - What to look for in logs
4. **Edge cases** — Deliberately provocative scenarios
5. **Cleanup** — Restore state for next phase

## Execution Order

| Lab | Phase | Status |
|-----|-------|--------|
| [00 — Environment Setup](00-environment-setup.md) | Pre | Required first |
| [02 — Controller Core](02-controller-core.md) | 2 | CRD, ref resolution, metadata ConfigMap, finalizer |
| [03 — Gateway Discovery](03-gateway-discovery.md) | 3 | Pod annotation discovery, status, conditions |
| [03A — SyncProfile](03a-sync-profile.md) | 3A | SyncProfile CRD, 3-tier config, backward compat |
| [04 — Webhook Receiver](04-webhook-receiver.md) | 4 | HTTP handler, HMAC, payload formats |
| [05 — Sync Agent](05-sync-agent.md) | 5 | Git clone, file sync, scan API, status reporting |
| [06 — Sidecar Injection](06-sidecar-injection.md) | 6 | Mutating webhook, pod injection, git secret injection |
| [07 — Helm Chart](07-helm-chart.md) | 7 | Operator helm install, values, upgrades |
| [08 — Observability](08-observability.md) | 8 | Metrics, structured logging, events |

## Phase Gate Rule

**Do not proceed to the next phase until all labs in the current phase pass.** If a lab fails:

1. Examine operator logs: `kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=100`
2. Examine Ignition gateway logs: `kubectl logs -n lab -l app.kubernetes.io/name=ignition --tail=100`
3. Check events: `kubectl get events -n lab --sort-by=.lastTimestamp`
4. Identify root cause, fix code, rebuild image, reload into kind, re-run the failing lab
5. After fix, re-run ALL labs in the current phase (not just the failing one) to catch regressions

## Shared Variables

These are used throughout all labs:

```bash
export KIND_CLUSTER=ignition-sync-lab
export OPERATOR_IMG=ignition-sync-operator:lab
export OPERATOR_NS=ignition-sync-operator-system
export LAB_NS=lab
export GIT_REPO_URL=https://github.com/ia-eknorr/test-ignition-project.git
```
