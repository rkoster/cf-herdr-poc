# CF Herdr POC

CF Herdr runs one public manager app that supervises the lead Collie process and creates short-lived Cloud Foundry sandbox apps. Browsers connect only through the manager and lead Collie. Sandbox apps never receive ordinary public routes; their identity routes are default-deny and restricted with Cloud Foundry route policies.

This is a deliberately dirty POC, not a production deployment. Full live lifecycle validation remains deferred; observed lab connectivity friction and unverified end-to-end behavior are recorded separately below.

## Architecture

- `manager` serves `/manager`, proxies lead Collie, reconciles sandbox state, and drives the CF CLI.
- Lead Collie runs from `dist/sandbox/runtime/collie` under manager supervision, using executables from `dist/manager-runtime`.
- Each sandbox receives `dist/sandbox/runtime`, starts Herdr, Bun, Collie, and `sandbox-bootstrap`, and joins the lead through the manager identity route.
- The public manager route is for operators and browsers. The separate manager identity route is only for sandbox-to-manager Pack enrollment.
- The manager Collie stays on loopback and uses `cf-identity` for outbound Pack requests with its CF instance certificate and key. Sandbox Collie derives `COLLIE_PORT` from CF's runtime `PORT`, binds `0.0.0.0`, and enables the non-loopback escape hatch only in `sandbox/start.sh`; CF identity mode keeps its peer browser disabled.
- Gorouter terminates the identity-route TLS connection. Collie's backend listener is plain HTTP on the assigned app port; `sandbox-bootstrap` temporarily uses that same port and exits before Collie binds it.

The nested Collie fork is pinned at `e6c7d8b80e70267439d768ffc5b9e3d408b84cd0`. Its source bridge requires the root Collie `node_modules`; `web/node_modules`, Git metadata, configuration, credentials, and runtime state are not packaged.

## Prerequisites

- Docker/BuildKit, Go 1.25, Bun 1.3, Node.js 24, Git, patchelf, binutils, GCC, and CF CLI v8; `devbox shell` provides local tools.
- A Linux Cloud Foundry foundation with Diego, the binary buildpack, an identity domain, and app instance identity credentials.
- A space developer able to push, start, stop, delete, inspect, and set environment variables on apps; create, map, unmap, and delete routes; and add/remove identity route policies.
- Platform approval for the sandbox buildpack allow-list and enough app, route, and memory quota for the manager plus sandboxes.
- The default cflinuxfs5 builder downloads only the pinned, SHA-256-verified Bun, Herdr, and OpenCode Linux
  artifacts from `docker/cflinuxfs5-builder/artifacts.env`; it fails closed when required metadata
  is missing. The explicit legacy `BUILD_MODE=nix-relocation` path requires externally supplied
  runtime binaries.

## Build

The default build uses the pinned cflinuxfs5 Docker builder. Herdr v0.8.2 AMD64 and ARM64 URLs and
SHA-256 checksums are pinned in `docker/cflinuxfs5-builder/artifacts.env`:

```bash
HERDR_URL_AMD64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-x86_64
HERDR_SHA256_AMD64=976150a14d490c94b243ea2e1a7eb2dfb67f12e36b182db90936f6728e6aecf4
HERDR_URL_ARM64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-aarch64
HERDR_SHA256_ARM64=f55610658e1c2e0d2aaef730b4b2ab885f7f8ba00285ab372bfb14f2e3d5b40d
CF_URL_AMD64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_x86-64.tgz
CF_SHA256_AMD64=98268ab3134bb3a1c97ffce797b4e6d35590a82e006cd098ad7a29f0a5cae7d8
CF_URL_ARM64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_arm64.tgz
CF_SHA256_ARM64=454c29a44a51c8edc9696678403e2e40808357a397033af5a018e6ca8ee32117
GOOS=linux GOARCH=amd64 BUILD_MODE=cflinuxfs5 bash scripts/build.sh
```

Bun 1.3.13 and Cloud Foundry CLI v8.19.0 artifacts are verified against official GitHub release metadata and pinned with SHA-256 checksums. CF CLI v8.19.0 artifacts are verified. The builder extracts the CF tarball's `cf` binary into `dist/manager-runtime/bin/cf`, the stable executable path required by the manager provider.

The build compiles the manager and sandbox bootstrap with `CGO_ENABLED=0`, builds both frontends, invokes `scripts/build-runtime.sh`, validates every required artifact, and transactionally replaces `dist/`. It stages first, renames the old tree to a backup, installs the new tree, and restores the backup on failure or interruption. Directory replacement is not fully atomic: there is a small rename window in which `dist/` is absent. `COLLIE_RUNTIME_BIN` may override the Collie CLI produced by the nested build, but must satisfy the same portable executable checks.

The sandbox runtime includes pinned OpenCode v1.18.30 at `sandbox/runtime/bin/opencode` and
the verified Cloud Foundry CLI at `sandbox/runtime/bin/cf`. Manager-created sandboxes
receive the manager's `CF_API`, `CF_USERNAME`, `CF_PASSWORD`, `CF_ORG`, and `CF_SPACE`
during enrollment. Interactive shells initialize `cf api`, `cf auth`, and `cf target`
from the managed `.bashrc` block; direct sandboxes do not receive manager credentials
by default.
Sandbox apps do not receive a Cloud Foundry `PATH` override. The launcher prepends
`/home/vcap/app/sandbox-runtime/bin` for its own process and maintains the same runtime contract for
interactive shells: `HOME=/home/vcap` and `SHELL=/bin/bash`.

The launcher keeps runtime state under `/home/vcap/.sandbox-state` and creates `/home/vcap/app/.cfignore`
with `sandbox-runtime/`, `.sandbox-state/`, and `join-token` before starting child processes. The launcher
maintains one managed block in `/home/vcap/.bashrc` containing the runtime `PATH`, `SHELL=/bin/bash`,
and `HERDR_SOCKET_PATH=/home/vcap/.sandbox-state/herdr.sock`. Interactive
`cf ssh` shells source this `.bashrc`, exposing `opencode` and `herdr`, and Collie terminal
sessions use the same runtime contract; `herdr` attaches to the server started by the sandbox
launcher. Non-interactive `cf ssh -c` commands do not source `/home/vcap/.bashrc`, so verify
command lookup by explicitly starting an interactive Bash shell:

```bash
cf ssh <sandbox-name> -c '/bin/bash -ic "command -v opencode; command -v herdr"'
```

Alternatively, use the absolute runtime paths from a non-interactive command:

```bash
cf ssh <sandbox-name> -c '/home/vcap/app/sandbox-runtime/bin/opencode --version; /home/vcap/app/sandbox-runtime/bin/herdr --version'
```

For this POC lab only, `ALLOW_NIX_RUNTIME_RELOCATION=1` allows Linux Nix-linked Bun, Herdr, and built Collie inputs. Using the cflinuxfs loader with bundled Nix libc failed with undefined `__tunable_is_initialized@GLIBC_PRIVATE`; explicitly invoking the bundled Nix loader succeeded. The workaround therefore installs each runtime as a direct ELF whose absolute interpreter is its private bundled loader at its final CF path, with an origin-relative RPATH. Sandbox binaries target `/home/vcap/app/sandbox-runtime/bin` for manager-created and direct sandboxes, while independent manager Bun and Collie copies target `/home/vcap/app/manager-runtime/bin`; Collie assets remain shared. The visible runtime is launched with `./sandbox-runtime/start.sh`. The relocator recursively resolves startup `DT_NEEDED` libraries and includes available glibc NSS/DNS resolver modules. This preserves executable identity and performs no internet downloads, but duplicates executable private libraries and binds artifacts to exact CF layouts. It is deliberate deployment friction, not a production packaging strategy; production should use official static or portable runtime artifacts.

This is not a claim of a complete dynamic runtime closure. Libraries loaded through unobserved `dlopen` paths and absolute runtime asset lookups may still be missing even though packaged ELF interpreter/RPATH metadata is scanned for actionable `/nix/store/` references. A live cflinuxfs smoke test is required before relying on the workaround. The CF CLI is the one exception to direct ELF packaging: `manager-runtime/bin/cf` is an executable wrapper that invokes `.cf-libs/ld-linux-*.so.*` with `--library-path` and the relocated `cf.real` payload. CF CLI does not require `process.execPath` or self-spawn identity, so the wrapper preserves its loader isolation while allowing the manager provider's configured path to remain stable. Bun, Herdr, and Collie remain direct ELF files because their runtime process identity matters.

Expected layout includes `dist/manager`, `dist/web/`, `dist/manager-runtime/`, `dist/sandbox/runtime/`, the runtime binaries, `collie/bridge`, Collie's root `package.json` and `node_modules`, and Collie's built `web/dist`. A constrained copier materializes internal Collie symlinks but rejects broken links and links escaping the Collie asset root. Only explicitly selected runtime trees are copied, excluding `.git`, environment files, credentials, state, and Collie's development-only `web/node_modules`. Generated `dist/`, `sandbox/runtime/`, and runtime state are ignored by Git.

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
| `CF_ORG` | Cloud Foundry organization targeted by the manager CLI. |
| `CF_SPACE` | Cloud Foundry space targeted by the manager CLI. |

The manifest supplies nonsecret packaged paths: `MANAGER_WEB_DIR=./web`, `MANAGER_COLLIE_DIR=./sandbox/runtime/collie`, `MANAGER_RUNTIME_DIR=./manager-runtime`, `MANAGER_SANDBOX_RUNTIME_DIR=./sandbox/runtime`, `MANAGER_BUN_EXECUTABLE=./manager-runtime/bin/bun`, and `MANAGER_COLLIE_EXECUTABLE=./manager-runtime/bin/collie`. The manager validates the sandbox runtime at startup and requires executable `start.sh`, Bun, Herdr, Collie, and `sandbox-bootstrap` assets. State defaults under `./data`; manager Collie listens only on `127.0.0.1:9191` and explicitly uses `COLLIE_PACK_TRANSPORT=cf-identity`. The sandbox provider does not set `COLLIE_PORT` with `cf set-env`: Cloud Foundry assigns `PORT` at runtime and the launcher derives Collie's port from it.

## Deploy

First authenticate and target the Cloud Foundry CLI in the space where Herdr will run. Copy the example values into your shell, replacing every example domain and host. Manifest variable substitution is intentionally not used.

Run the foundation setup before deploying the Herdr application. `devbox run setup` sources `bosh.env`, deploys local `cf.yml` with `ops-enable-mtls-app-routing.yml`, relies on CredHub configured on the BOSH director instead of a local vars-store, and registers the `apps.identity` shared domain with route-policy enforcement and `any` source scope:

```bash
devbox run setup
```

Then deploy the Herdr manager with the application-only command:

```bash
export MANAGER_APP_NAME=cf-herdr-manager
export PUBLIC_DOMAIN=apps.example.com
export MANAGER_PUBLIC_HOST=cf-herdr-manager
export CF_IDENTITY_DOMAIN=apps.internal
export MANAGER_PACK_HOST=cf-herdr-manager-pack.apps.internal
export MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
export SANDBOX_BUILDPACKS=binary_buildpack,nodejs_buildpack
export MANAGER_API_TOKEN=replace-with-externally-supplied-secret
export CF_API=https://api.example.com
export CF_USERNAME=manager
export CF_PASSWORD=replace-with-externally-supplied-secret
export CF_ORG=poc
export CF_SPACE=demo

devbox run deploy
```

`manifest.yml` documents the general application configuration. `devbox run deploy` first builds `dist/` with Docker/BuildKit using `BUILD_MODE=cflinuxfs5`, then uses an explicit `cf push --no-manifest ... --redact-env` so redeployment preserves existing runtime environment variables and redacts environment values from push output. `BUILD_MODE=nix-relocation` remains an explicit lab-only fallback. The Docker build receives artifact URLs and checksums only, never deployment tokens or environment files. It maps exactly one public manager route and one manager identity route, and no sandbox route. It does not generate or persist `MANAGER_API_TOKEN`; supply that ephemeral secret externally.

`devbox run deploy` is the deployment route and handles CF CLI discovery internally. For the manual post-deployment and cleanup commands below, set `CF_BIN` to the same executable path (for example, the result of that discovery or an operator-selected CF CLI); these examples intentionally do not duplicate the NixOS-specific discovery logic.

For each manager-created sandbox, obtain its GUID and apply both exact route policies after its identity route exists:

```bash
CF_BIN=/path/to/cf
export MANAGER_APP_GUID="$("$CF_BIN" app "$MANAGER_APP_NAME" --guid)"
export SANDBOX_APP_NAME=replace-with-sandbox-name
export SANDBOX_HOST="$SANDBOX_APP_NAME"
export SANDBOX_GUID="$("$CF_BIN" app "$SANDBOX_APP_NAME" --guid)"
"$CF_BIN" add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_HOST" --source "cf:app:$MANAGER_APP_GUID"
"$CF_BIN" add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID"
```

Confirm the binary buildpack and every sandbox buildpack are allow-listed by platform operators before pushing. The manifest's `binary_buildpack` is required because the manager Go binary is prebuilt.

## Cleanup

Manager reconciliation records each sandbox app GUID. Before a name-addressed app deletion or route unmap, the provider resolves the name again and requires the GUID to match. An absent original is already clean; a replacement is never deleted or unmapped, while manager-owned route policies and the stable sandbox route can still be removed. This preflight narrows but cannot eliminate the race between separate CF CLI calls. The POC assumes a trusted, single-operator space during cleanup; stronger multi-operator guarantees require GUID-addressed CAPI deletion and job handling.

Remove policies before deleting routes and apps:

```bash
CF_BIN=/path/to/cf
"$CF_BIN" remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_HOST" --source "cf:app:$MANAGER_APP_GUID" -f
"$CF_BIN" remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID" -f
"$CF_BIN" delete "$SANDBOX_APP_NAME" -f -r
"$CF_BIN" unmap-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
"$CF_BIN" unmap-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
"$CF_BIN" delete-route "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST" -f
"$CF_BIN" delete-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" -f
"$CF_BIN" delete "$MANAGER_APP_NAME" -f
rm -rf dist
```

## Verification

Lab deployment requires `CF_API`, `CF_USERNAME`, `CF_PASSWORD`, `CF_ORG`, and `CF_SPACE` in the caller environment. Set `CF_SKIP_SSL_VALIDATION=true` only for the lab when required; deployment passes these values to the manager without printing them. The manager authenticates the bundled CF CLI and targets the configured org and space using a private isolated configuration directory. The manager fails startup if any of these CF settings is absent.

```bash
TMPDIR=/tmp go test -race ./...
TMPDIR=/tmp go vet ./...
(cd web && bun run test && bun run typecheck && bun run build)
bash -n scripts/build.sh scripts/build-runtime.sh scripts/relocate-nix-runtime.sh scripts/smoke-relocated-runtime.sh scripts/deploy.sh scripts/lab-deploy.sh sandbox/start.sh
```

Artifact tests use fake executable fixtures only; they do not establish that any real Bun or Herdr binary is portable. Real portable artifact execution and live CF behavior remain pending.

## POC Friction

- Deferred runtime and identity spike procedures are in [`docs/spikes/cf-buildpack-runtime.md`](docs/spikes/cf-buildpack-runtime.md) and [`docs/spikes/cf-pack-identity.md`](docs/spikes/cf-pack-identity.md). The original design and task log remain in `docs/superpowers/`.
- The nested Collie checkout is intentionally a fork with POC changes and may be dirty; packaging must not modify its source or `.envrc`.
- The cflinuxfs5 builder pins Herdr v0.8.2 acquisition in `docker/cflinuxfs5-builder/artifacts.env`.
  Verified CF CLI acquisition remains external and unresolved here, while non-container legacy paths
  may still require externally supplied portable Bun/Herdr artifacts.
- Lab deployment relocates local Nix startup dependency graphs without downloads. Separate private glibc bundles avoid collisions but materially increase `dist/`; dynamic `dlopen` and absolute asset paths remain risks until a live smoke test, and official static or portable artifacts remain the production path.
- Local curl does not trust the lab CA used by the public manager route (observed curl code 60). An explicit insecure diagnostic reached login with HTTP 204, but no successful lifecycle result is claimed. The smoke harness prefers `SMOKE_PUBLIC_CA_CERT`; its explicit `SMOKE_INSECURE_PUBLIC_TLS=1` fallback applies only to public manager calls. Identity-route no-certificate and `cf ssh` instance-certificate checks retain strict TLS verification.
- Sandbox staging checks `cf app NAME --guid` immediately before `cf push`, but separate CLI calls cannot make name reservation atomic. Another actor can still create the app in that lookup/push window; eliminating this race requires an atomic CAPI creation strategy.
- Deletion never adopts an app GUID discovered by name. If a sandbox record has no persisted app GUID and that CF app name exists, stable manager-owned Pack state may be cleaned but the record remains failed with `ownership unknown`; an operator must investigate the potential orphan rather than risk deleting an unrelated app.
