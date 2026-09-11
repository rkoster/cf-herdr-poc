# Direct Sandbox Debugging

## Run

Use `devbox run sandbox` for a standalone sandbox. The CF CLI must be targeted to the intended API, org, and space. Set `CF_API`, `CF_ORG`, and `CF_SPACE`; set `CF_BIN` when Devbox cannot discover the CLI. `dist/sandbox/runtime` must exist, and the selected buildpack must be allow-listed.

Optional variables are `SANDBOX_NAME`, `SANDBOX_REPOSITORY`, `SANDBOX_BUILDPACK`, `SANDBOX_KEEP=1`, `SANDBOX_CWD`, `SANDBOX_FOLLOW_LOGS=1`, `COLLIE_JOIN_TOKEN_FILE`, and `COLLIE_PACK_LEAD_ADDRESS`. The default repository is the public Node sample and the default buildpack is `nodejs_buildpack`. Join variables are unset by default.

`SANDBOX_NAME` must be a lowercase Cloud Foundry app name. The script checks `cf app <name> --guid` first and refuses to use an existing app; choose a different name rather than reusing one. If `COLLIE_JOIN_TOKEN_FILE` is set, `COLLIE_PACK_LEAD_ADDRESS` is also required. The token is copied into the staged app with restrictive permissions and is never printed.

The direct workflow clones into a temporary directory, overlays the built runtime as the visible `sandbox-runtime` directory, pushes with `--no-route --no-start`, then starts the app. The visible path is intentional: CF package handling omits hidden `.sandbox` trees, which causes the launcher to be absent from the staged app. Runtime state remains under `/home/vcap/.sandbox-state`; the launcher creates `/home/vcap/app/.cfignore` to exclude `sandbox-runtime/`, `.sandbox-state/`, and `join-token` from agent `cf push` packages. An optional join token is staged under the visible runtime directory and consumed by `sandbox-bootstrap` before Bun starts. Direct builds target `/home/vcap/app/sandbox-runtime/bin`; manager-created sandboxes remain under `manager-runtime`. The direct workflow creates no public route, identity route, or route policy. With both join variables set it enrolls through the manager Pack lead; without them it skips bootstrap. Manager-created sandboxes are different: they use CF instance identity, manager route policy, and Pack enrollment. Configure those through the manager workflow, not this script.

## Inspect

```bash
cf app "$SANDBOX_NAME"
cf logs "$SANDBOX_NAME" --recent
cf ssh "$SANDBOX_NAME"
cf curl "/v3/apps/$APP_GUID/processes"
cf curl "/v3/apps/$APP_GUID/stats"
```

Inside the app, check the direct runtime and process contract:

```bash
cd /home/vcap/app
sandbox-runtime/bin/herdr server
sandbox-runtime/bin/bun --version
./sandbox-runtime/start.sh
```

Delete a debug app with `cf delete "$SANDBOX_NAME" -f -r`. The script deletes its app on failure and on normal exit unless `SANDBOX_KEEP=1`.

## Triage

Check in this order: staging, process startup, Herdr socket, Collie source closure and plugin root, CF instance identity and route policy, then disk quota. Direct mode intentionally has no public route or Pack enrollment, so identity and Pack failures belong to the manager-created workflow.

Never log or copy `CF_PASSWORD`, `MANAGER_API_TOKEN`, an instance certificate or key, or a Pack token. Record measured friction and reproducible observations in `docs/spikes/smoke-test.md`.

## Local Deployment Secrets

Deployment secrets may be stored locally in the ignored `.secrets` file. `.envrc` sources this file when present, and `devbox run deploy` supplies defaults for nonsecret lab configuration. Keep only secret values such as `CF_USERNAME`, `CF_PASSWORD`, and `MANAGER_API_TOKEN` in `.secrets`; never commit, print, or copy the file contents. Do not store certificates, private keys, instance identity material, or Pack tokens in tracked files or logs.
