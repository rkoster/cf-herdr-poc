# Sandbox Lifecycle Smoke Test

## Status

**Deferred.** The live smoke test has not been run. Execution is deferred until a new Cloud Foundry environment provides identity-based routing and the manager package contains portable Bun, Herdr, and Collie binaries.

## Prerequisites

- A targeted and authenticated `cf` CLI session with permission to inspect and remove apps, routes, and route policies.
- A deployed manager on a Cloud Foundry foundation with identity-based routing enabled.
- Portable Bun, Herdr, and Collie runtime binaries in the deployed manager package.
- `bash`, `curl`, `jq`, and Cloud Foundry CLI v8. `devbox.json` supplies `jq` and `cf`.
- A harmless Git repository supported by an allowed buildpack.
- A wrong-identity probe executable running in a separate CF app. It must read `SMOKE_IDENTITY_URL`, call it using that app's instance identity, and exit successfully only for HTTP 403. Set `SMOKE_SKIP_WRONG_IDENTITY=1` only to record an explicit reduced-coverage run.

## Invocation

```bash
SMOKE_LIVE=1 \
MANAGER_URL=https://manager.example.com \
MANAGER_API_TOKEN='set-outside-shell-history' \
MANAGER_APP_GUID='manager-app-guid' \
IDENTITY_DOMAIN=apps.internal.example.com \
SMOKE_REPOSITORY=https://example.com/organization/harmless-smoke.git \
SMOKE_BUILDPACK=binary_buildpack \
WRONG_IDENTITY_PROBE_CMD=/path/to/wrong-identity-probe \
bash scripts/smoke.sh
```

The script generates a unique sandbox name unless `SMOKE_NAME` is set. It refuses to execute without `SMOKE_LIVE=1`. The manager token is submitted from an owner-only temporary JSON file to obtain an owner-only cookie jar; it is never placed in curl arguments. Temporary authentication files are removed by the cleanup trap.

## Assertions

- The manager reaches `ready`, returns a `packMemberId`, and reports only sanitized lifecycle errors and timing.
- The sandbox app has only its identity route; no ordinary public route is mapped.
- The identity route rejects a request without a CF instance certificate.
- The external wrong-app probe observes HTTP 403, unless the run explicitly records `SKIP`.
- Lead Collie's snapshot and Pack status report the sandbox member as reachable. Together with lifecycle readiness, this is the available non-destructive evidence that the manager identity reached Pack hello and snapshot traffic.
- Manager API JSON recursively omits internal hostnames, app GUIDs, certificate/key paths, tokens, passwords, and secret keys.
- Deletion removes the manager record, Collie Pack member, app, route, and route policy. If manager deletion fails or times out, the exit trap attempts direct idempotent CF cleanup.

The user-flow check is deliberately read-only: `GET /collie/api/snapshot?sessions=all` verifies the host member without creating a workspace or sending a prompt. Exact Collie write authentication and a universally harmless workspace action are not stable enough for this deferred POC run.

## Expected Timing

All values are unmeasured until the deferred live run.

| Phase | Expected/Observed |
| --- | --- |
| Clone | Unmeasured |
| Upload | Unmeasured |
| Stage | Unmeasured |
| Start | Unmeasured |
| Policy propagation | Unmeasured |
| Pack enrollment | Unmeasured |
| Ready | Unmeasured |
| Delete | Unmeasured |

## Observations

None yet. No environment version, retries, failed assumptions, manual interventions, or timings have been observed because live execution is deferred.

## Recommendations

Pending live evidence. Keep recommendations separate from observations and add them only after the first execution identifies concrete Cloud Foundry workflow friction.
