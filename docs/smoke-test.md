# Sandbox Lifecycle Smoke Test

## Status

**In progress.** A live lifecycle attempt reached manager creation but did not complete. The lab public manager route is signed by a lab CA that local curl does not trust by default; this transport friction is explicit without weakening identity-route verification.

## Prerequisites

- A targeted, authenticated CF CLI v8 session with an org and space selected.
- Permission to inspect apps, domains, routes, and route policies; SSH access to the manager and probe apps; and permission to remove sandbox resources.
- An identity-routing domain and CAPI `GET /v3/route_policies` support. The currently targeted foundation lacks that endpoint, so live execution remains deferred.
- A deployed manager whose `MANAGER_APP_NAME`, `MANAGER_APP_GUID`, and identity-route `MANAGER_ROUTE_HOST` are known.
- A pre-provisioned `WRONG_IDENTITY_APP` in the same target. It must be SSH-enabled, include curl, and have a GUID distinct from the manager and generated sandbox app.
- Portable Bun, Herdr, and Collie runtime binaries in the manager package.
- A harmless Git repository supported by an allowed buildpack.
- `bash`, `curl`, `jq`, and `cf`. The Devbox environment supplies these dependencies.

The harness discovers `cf` from `PATH`, `LAB_PROFILE_BIN_DIR`, the per-user NixOS profile, or the home Nix profile. Set `CF_BIN` to an explicit executable path when Devbox isolation hides those profiles or strips `USER`.

There is no reduced-coverage or skip mode. Both app-identity checks are mandatory.

## Invocation

```bash
SMOKE_LIVE=1 \
CF_BIN=/etc/profiles/per-user/$USER/bin/cf \
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
SMOKE_PUBLIC_CA_CERT=/absolute/path/to/lab-ca.pem \
bash scripts/smoke.sh
```

`SMOKE_NAME` may override the generated unique name. The script refuses to invoke CF or curl unless `SMOKE_LIVE=1` is present. It sends the manager token from an owner-only temporary JSON file, obtains an owner-only session cookie jar, and removes both through the cleanup trap. Credentials and certificate/key contents are never printed or placed in curl process arguments.

`SMOKE_CLEANUP_TIMEOUT` independently bounds cleanup polling and defaults to 120 seconds. Lifecycle failure requests manager deletion immediately, directly removes only resources for the uniquely preflighted smoke name, and reports any residual manager record when that bound expires rather than reusing the longer lifecycle timeout.

`SMOKE_PUBLIC_CA_CERT` is the preferred lab trust mode. It must name a readable regular file that is not a symlink, and the harness passes it as curl `--cacert` only for calls through `MANAGER_URL`. If the lab CA file is unavailable, `SMOKE_INSECURE_PUBLIC_TLS=1` is an explicit lab-only fallback for those same public manager calls and prints `smoke: public-route TLS verification disabled for lab` once. Any other insecure value, or configuring both modes, is rejected before network activity. Neither mode affects the local no-client-certificate identity assertion, `cf ssh`, or the in-container curl that presents `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY`; identity-route TLS continues to use platform/system trust and remains fully verified.

Before arming destructive cleanup, the harness verifies that no app or identity-domain route already uses the stable sandbox name. It also performs an authenticated, strictly validated manager sandbox listing and requires that name to have no manager record. It then arms cleanup immediately before creation. A concurrent creator can still race this check; that limitation is accepted for this POC.

## Exact Assertions

- `cf target` names an authenticated user, org, and space; the identity domain exists; route-policy commands are available.
- Manager and wrong-identity app GUIDs resolve uniquely, match the configured manager GUID where applicable, and differ from each other and the sandbox GUID.
- Manager lifecycle JSON is valid and recursively omits internal hostnames, app GUIDs, certificate/key paths, tokens, passwords, and secret keys.
- The sandbox reaches `ready` with a Pack member ID and has no ordinary public route.
- A local request without an instance certificate cannot reach the identity route.
- `cf ssh $WRONG_IDENTITY_APP` calls the identity route with that app's `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY` and receives exactly HTTP 403.
- `cf ssh $MANAGER_APP_NAME` runs the packaged Collie `pack status` command with the manager's explicit config, state, socket, and loopback settings. Its authenticated Pack probe must report the sandbox member reachable without exposing Pack secrets.
- Authenticated `GET /collie/api/snapshot?host=<member>` through the public manager gateway reports the reachable member.
- Authenticated POST `/collie/api/workspace?host=<member>` with `{cwd:<configured path>,label:"smoke"}` succeeds and returns a pane ID.
- The host-scoped snapshot reports that workspace and pane. The harness sends `printf 'smoke-ready\n'` through the pane reply route and confirms the marker through a host-scoped pane read.
- Deletion removes the manager record. Authenticated Pack and snapshot reads then omit the member, while CAPI reports no app or route. The harness resolves both route GUIDs, then uses filtered `GET /v3/route_policies?route_guids=<guid>&sources=cf%3Aapp%3A<guid>` requests to verify that the manager-to-sandbox and sandbox-to-manager tuples are absent.
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

The lab public manager curl initially failed certificate verification with curl code 60 because the lab CA is not in the local trust store. An explicit lab trust mode reached the login endpoint and received HTTP 204. Identity-aware route requests remained strictly verified.

The lifecycle attempt failed during create with `resolve physical work root: lstat /home/vcap/app/data/work: no such file or directory`; manager startup had loaded missing state as empty without work root initialization. Direct CF cleanup removed the smoke app and routes, but the failed residual manager record remained because reconciliation refused deletion, and the harness continued polling it under the 20-minute lifecycle timeout. These are observed failures, not a completed smoke result.

## Recommendations

Pending live evidence. Recommendations will remain separate from observed facts after the first approved run.
