# Sandbox Lifecycle Smoke Test

## Status

**Deferred.** Live execution is forbidden until a new Cloud Foundry environment provides identity-based routing and the deployed package contains portable Bun, Herdr, and Collie binaries. No live results are recorded.

## Prerequisites

- A targeted, authenticated CF CLI v8 session with an org and space selected.
- Permission to inspect apps, domains, routes, and route policies; SSH access to the manager and probe apps; and permission to remove sandbox resources.
- An identity-routing domain visible through CAPI and route-policy commands available in the CLI.
- A deployed manager whose `MANAGER_APP_NAME`, `MANAGER_APP_GUID`, and identity-route `MANAGER_ROUTE_HOST` are known.
- A pre-provisioned `WRONG_IDENTITY_APP` in the same target. It must be SSH-enabled, include curl, and have a GUID distinct from the manager and generated sandbox app.
- Portable Bun, Herdr, and Collie runtime binaries in the manager package.
- A harmless Git repository supported by an allowed buildpack.
- `bash`, `curl`, `jq`, and `cf`. The Devbox environment supplies these dependencies.

There is no reduced-coverage or skip mode. Both app-identity checks are mandatory.

## Invocation

```bash
SMOKE_LIVE=1 \
MANAGER_URL=https://manager.example.com \
MANAGER_API_TOKEN='set-without-shell-history' \
MANAGER_APP_NAME=cf-herdr-manager \
MANAGER_APP_GUID=00000000-0000-0000-0000-000000000000 \
MANAGER_ROUTE_HOST=manager-pack \
WRONG_IDENTITY_APP=cf-identity-probe \
IDENTITY_DOMAIN=apps.internal.example.com \
SMOKE_REPOSITORY=https://example.com/organization/harmless-smoke.git \
SMOKE_BUILDPACK=binary_buildpack \
SMOKE_WORKSPACE_CWD=/home/vcap/app \
bash scripts/smoke.sh
```

`SMOKE_NAME` may override the generated unique name. The script refuses to invoke CF or curl unless `SMOKE_LIVE=1` is present. It sends the manager token from an owner-only temporary JSON file, obtains an owner-only session cookie jar, and removes both through the cleanup trap. Credentials and certificate/key contents are never printed or placed in curl process arguments.

## Exact Assertions

- `cf target` names an authenticated user, org, and space; the identity domain exists; route-policy commands are available.
- Manager and wrong-identity app GUIDs resolve uniquely, match the configured manager GUID where applicable, and differ from each other and the sandbox GUID.
- Manager lifecycle JSON is valid and recursively omits internal hostnames, app GUIDs, certificate/key paths, tokens, passwords, and secret keys.
- The sandbox reaches `ready` with a Pack member ID and has no ordinary public route.
- A local request without an instance certificate cannot reach the identity route.
- `cf ssh $WRONG_IDENTITY_APP` calls the identity route with that app's `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY` and receives exactly HTTP 403.
- `cf ssh $MANAGER_APP_NAME` performs an authenticated `GET /pack/v1/hello` after readiness and receives HTTP 200 JSON with numeric `protocol` and the expected `member`.
- Authenticated `GET /collie/api/snapshot?host=<member>` through the public manager gateway reports the reachable member.
- Authenticated POST `/collie/api/workspace?host=<member>` with `{cwd:<configured path>,label:"smoke"}` succeeds and returns a pane ID.
- The host-scoped snapshot reports that workspace and pane. The harness sends `printf 'smoke-ready\n'` through the pane reply route and confirms the marker through a host-scoped pane read.
- Deletion removes the manager record. Authenticated Pack and snapshot reads then omit the member, while CAPI reports no app, route, or route policy.
- Any malformed response, timeout, lifecycle failure, identity mismatch, failed leak query, or residual resource fails the run. The armed trap requests manager deletion and directly removes residual policy, route, and app resources when reconciliation does not.

## Timing Output

The script prints only fixed timing labels. Manager operation durations are used for clone, upload, stage, start, policy, and Pack enrollment when the corresponding named operation exists; otherwise the value is `unavailable`. Client-observed ready and delete durations are also printed. No arbitrary operation names, summaries, or errors are dumped.

| Metric | Observation |
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

None. Live execution remains forbidden, so there are no environment versions, retries, failures, interventions, or timings to report.

## Recommendations

Pending live evidence. Recommendations will remain separate from observed facts after the first approved run.
