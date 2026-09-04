# CF Herdr POC

CF Herdr runs one public manager app that supervises the lead Collie process and creates short-lived Cloud Foundry sandbox apps. Browsers connect only through the manager and lead Collie. Sandbox apps never receive ordinary public routes; their identity routes are default-deny and restricted with Cloud Foundry route policies.

This is a deliberately dirty POC, not a production deployment. Live `cf push` validation is deferred: the current target has no identity domain, so identity routes and route policies cannot yet be exercised end to end.

## Architecture

- `manager` serves `/manager`, proxies lead Collie, reconciles sandbox state, and drives the CF CLI.
- Lead Collie runs from `dist/sandbox/runtime/collie` under manager supervision, using the bundled Bun and Collie executables.
- Each sandbox receives `dist/sandbox/runtime`, starts Herdr, Bun, Collie, and `sandbox-bootstrap`, and joins the lead through the manager identity route.
- The public manager route is for operators and browsers. The separate manager identity route is only for sandbox-to-manager Pack enrollment.

The nested Collie fork is pinned at `e6c7d8b80e70267439d768ffc5b9e3d408b84cd0`. Its source bridge requires the root Collie `node_modules`; `web/node_modules`, Git metadata, configuration, credentials, and runtime state are not packaged.

## Prerequisites

- Go 1.25, Bun 1.3, Node.js 24, Git, and CF CLI v8; `devbox shell` provides these tools.
- A Linux Cloud Foundry foundation with Diego, the binary buildpack, an identity domain, and app instance identity credentials.
- A space developer able to push, start, stop, delete, inspect, and set environment variables on apps; create, map, unmap, and delete routes; and add/remove identity route policies.
- Platform approval for the sandbox buildpack allow-list and enough app, route, and memory quota for the manager plus sandboxes.
- Independently obtained, portable Linux Bun and Herdr executables. Build scripts never download binaries. Do not use Nix-linked or unresolved ELF executables.

## Build

Choose the target architecture and explicitly supply portable runtime artifacts:

```bash
export BUN_RUNTIME_BIN=/absolute/path/to/portable-linux-bun
export HERDR_RUNTIME_BIN=/absolute/path/to/portable-linux-herdr
GOOS=linux GOARCH=amd64 bash scripts/build.sh
```

The build compiles the manager and sandbox bootstrap with `CGO_ENABLED=0`, builds both frontends, invokes `scripts/build-runtime.sh`, validates every required artifact, and transactionally replaces `dist/`. It stages first, renames the old tree to a backup, installs the new tree, and restores the backup on failure or interruption. Directory replacement is not fully atomic: there is a small rename window in which `dist/` is absent. `COLLIE_RUNTIME_BIN` may override the Collie CLI produced by the nested build, but must satisfy the same portable executable checks.

Expected layout includes `dist/manager`, `dist/web/`, `dist/sandbox/runtime/`, the runtime binaries, `collie/bridge`, Collie's root `package.json` and `node_modules`, and Collie's built `web/dist`. A constrained copier materializes internal Collie symlinks but rejects broken links and links escaping the Collie asset root. Only explicitly selected runtime trees are copied, excluding `.git`, environment files, credentials, state, and Collie's development-only `web/node_modules`. Generated `dist/`, `sandbox/runtime/`, and runtime state are ignored by Git.

## Configuration

Required at startup:

| Variable | Purpose |
| --- | --- |
| `CF_IDENTITY_DOMAIN` | Identity-aware routing domain. |
| `SANDBOX_BUILDPACKS` | Comma-separated allow-list, for example `binary_buildpack,nodejs_buildpack`. |
| `MANAGER_APP_NAME` | Manager CF app name. |
| `MANAGER_APP_GUID` | Manager app GUID. |
| `MANAGER_PACK_HOST` | Full direct-child FQDN for the manager identity route, for example `cf-herdr-manager-pack.apps.internal`. |
| `MANAGER_API_TOKEN` | Operator API bearer token; set out of band and never commit it. |

The manifest supplies nonsecret packaged paths: `MANAGER_WEB_DIR=./web`, `MANAGER_COLLIE_DIR=./sandbox/runtime/collie`, `MANAGER_RUNTIME_DIR=./sandbox/runtime`, `MANAGER_BUN_EXECUTABLE=./sandbox/runtime/bin/bun`, and `MANAGER_COLLIE_EXECUTABLE=./sandbox/runtime/bin/collie`. State defaults under `./data`; Collie listens only on `127.0.0.1:9191`.

## Deploy

Copy the example values into your shell, replacing every example domain and host. Manifest variable substitution is intentionally not used. `manifest.yml` has `no-route: true`; `scripts/deploy.sh` maps exactly one public manager route and one manager identity route, and no sandbox route.

```bash
export MANAGER_APP_NAME=cf-herdr-manager
export PUBLIC_DOMAIN=apps.example.com
export MANAGER_PUBLIC_HOST=cf-herdr-manager
export CF_IDENTITY_DOMAIN=apps.internal
export MANAGER_PACK_HOST=cf-herdr-manager-pack.apps.internal
export MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
export SANDBOX_BUILDPACKS=binary_buildpack,nodejs_buildpack
export MANAGER_API_TOKEN="$(openssl rand -hex 32)"

bash scripts/deploy.sh
export MANAGER_APP_GUID="$(cf app "$MANAGER_APP_NAME" --guid)"
```

For each manager-created sandbox, obtain its GUID and apply both exact route policies after its identity route exists:

```bash
export SANDBOX_APP_NAME=replace-with-sandbox-name
export SANDBOX_HOST="$SANDBOX_APP_NAME"
export SANDBOX_GUID="$(cf app "$SANDBOX_APP_NAME" --guid)"
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_HOST" --source "cf:app:$MANAGER_APP_GUID"
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID"
```

Confirm the binary buildpack and every sandbox buildpack are allow-listed by platform operators before pushing. The manifest's `binary_buildpack` is required because the manager Go binary is prebuilt.

## Cleanup

Remove policies before deleting routes and apps:

```bash
cf remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_HOST" --source "cf:app:$MANAGER_APP_GUID"
cf remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID"
cf delete "$SANDBOX_APP_NAME" -f -r
cf unmap-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf unmap-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf delete-route "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST" -f
cf delete-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" -f
cf delete "$MANAGER_APP_NAME" -f
rm -rf dist
```

## Verification

```bash
TMPDIR=/tmp go test -race ./...
TMPDIR=/tmp go vet ./...
(cd web && bun run test && bun run typecheck && bun run build)
bash -n scripts/build.sh scripts/build-runtime.sh scripts/deploy.sh sandbox/start.sh
```

Artifact tests use fake executable fixtures only; they do not establish that any real Bun or Herdr binary is portable. Real portable artifact execution and live CF behavior remain pending.

## POC Friction

- Deferred runtime and identity spike procedures are in [`docs/spikes/cf-buildpack-runtime.md`](docs/spikes/cf-buildpack-runtime.md) and [`docs/spikes/cf-pack-identity.md`](docs/spikes/cf-pack-identity.md). The original design and task log remain in `docs/superpowers/`.
- The nested Collie checkout is intentionally a fork with POC changes and may be dirty; packaging must not modify its source or `.envrc`.
- The source runtime requires a large copied Collie dependency tree, and portable Bun/Herdr acquisition is deliberately outside this repository.
- The current CF target lacks an identity domain. Live route-policy, instance-identity, manager health, sandbox lifecycle, and browser-through-lead spikes are deferred and no successful result is claimed.
