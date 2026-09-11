# Cloud Foundry Herdr Sandbox Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a demonstrable manager that creates buildpack-backed CF sandbox apps, securely federates their Herdr/Collie peers into one lead Collie, and exposes all interaction through one public UI.

**Architecture:** A Go process is the CF app entrypoint, serves the manager API/frontend, supervises lead Collie, and reconciles sandbox resources through the `cf` CLI. Each sandbox contains a prebuilt Herdr/Bun/Collie runtime plus the selected Git repository; its only route is on an identity-aware domain and permits the manager app identity. A narrow Collie fork mode delegates transport identity to CF Gorouter while preserving Pack enrollment, request signing, and the Pack secret.

**Tech Stack:** Go 1.25 standard library, `cf` CLI v8, Cloud Foundry identity-aware routing, React 19, React Router 7, Vite 8, TypeScript 7, Tailwind CSS 4, shadcn-style primitives, Vitest, Testing Library, Bun, Herdr, Collie Pack v1

---

## File Map

The repository root remains the POC product. The existing `collie/` clone is a vendored fork used to build lead and peer Collie artifacts.

- `go.mod`: Go module and version.
- `cmd/manager/main.go`: process assembly, signal handling, and server startup only.
- `internal/config/config.go`: environment parsing and buildpack allow-list.
- `internal/model/sandbox.go`: persisted sandbox and lifecycle types.
- `internal/store/file.go`: atomic JSON persistence.
- `internal/runner/runner.go`: injectable external-command interface.
- `internal/cf/provider.go`: safe `cf` CLI argument construction and CF resource operations.
- `internal/runtime/bundle.go`: clone repository and overlay prebuilt sandbox runtime files.
- `internal/pack/manager.go`: lead invite, Collie restart, and enrollment observation.
- `internal/reconcile/reconciler.go`: creation/deletion state machine.
- `internal/bootstrap/server.go`: sandbox bootstrap health and one-time Pack enrollment endpoint; it relies on the CF identity-aware route policy rather than browser authentication.
- `cmd/sandbox-bootstrap/main.go`: temporary sandbox bootstrap process used before peer Collie starts.
- `internal/httpapi/server.go`: manager API, static files, and reverse proxy routes.
- `internal/httpapi/sanitize.go`: browser response projection that omits internal routes and secrets.
- `internal/identity/client.go`: outbound HTTPS client using `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY`.
- `internal/supervisor/collie.go`: starts and restarts lead Collie as a child process.
- `web/`: manager frontend using Collie's React/Vite/Tailwind conventions.
- `sandbox/start.sh`: sandbox process launcher for Herdr, one-time Pack join, and peer Collie.
- `sandbox/runtime/`: generated or copied Herdr, Bun, Collie, and configuration artifacts; ignored by Git.
- `scripts/build-runtime.sh`: builds Collie and assembles the runtime overlay.
- `scripts/smoke.sh`: real-CF identity and lifecycle smoke test.
- `manifest.yml`: manager app deployment manifest.
- `collie/bridge/pack/cf-identity.ts`: CF transport mode and instance-certificate TLS options.
- `collie/bridge/pack/{cf-identity,admission,transport}.test.ts`: fork behavior tests.
- `collie/bridge/{config,index}.ts`: wire the explicit CF transport mode.
- `collie/cli/pack.ts`: use CF instance identity for enrollment and avoid Pack certificate pinning at Gorouter.
- `collie/CHANGELOG.md`: one concise Unreleased entry required by the fork's working agreement.

## Milestone 1: Prove Runtime And Transport

### Task 1: Scaffold The Go Manager

**Files:**
- Create: `go.mod`
- Create: `cmd/manager/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `devbox.json`

- [ ] **Step 1: Write the failing configuration tests**

Create `internal/config/config_test.go` with table tests proving that `Load` requires `CF_IDENTITY_DOMAIN`, defaults the state path and ports, parses `SANDBOX_BUILDPACKS` as a comma-separated allow-list, and rejects an empty list.

```go
func TestLoadBuildpacks(t *testing.T) {
    env := map[string]string{
        "CF_IDENTITY_DOMAIN": "apps.identity",
        "SANDBOX_BUILDPACKS": "ruby_buildpack,nodejs_buildpack",
    }
    got, err := Load(func(key string) string { return env[key] })
    if err != nil { t.Fatal(err) }
    if diff := cmp.Diff([]string{"ruby_buildpack", "nodejs_buildpack"}, got.Buildpacks); diff != "" {
        t.Fatal(diff)
    }
}
```

Use only the standard library in the actual test; replace `cmp.Diff` with `reflect.DeepEqual` so `go.mod` stays dependency-free.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config`

Expected: FAIL because `Load` and `Config` do not exist.

- [ ] **Step 3: Add the module and minimal config implementation**

Set the module to `cf-herdr-poc`. Define:

```go
type Config struct {
    Address            string
    StatePath          string
    WebDir             string
    CollieDir          string
    IdentityDomain     string
    ManagerAppName     string
    ManagerAppGUID     string
    ManagerPackHost    string
    Buildpacks         []string
    ReconcileInterval  time.Duration
}
```

`Load` must trim values, split buildpacks, use `:8080`, `./data/sandboxes.json`, `./web/dist`, and `./collie` defaults, and return concrete errors naming missing settings.

- [ ] **Step 4: Add Go and Node tools to Devbox**

Change `devbox.json` packages to `go@1.25`, `bun@1.3`, `nodejs@24`, `git`, and `cloudfoundry-cli`. Replace the placeholder test script with `go test ./...`.

- [ ] **Step 5: Run tests and format checks**

Run: `gofmt -w cmd internal && go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod cmd/manager/main.go internal/config devbox.json devbox.lock
git commit -m "chore: scaffold Go sandbox manager"
```

### Task 2: Build A Local Sandbox Runtime Bundle

**Files:**
- Create: `.gitignore`
- Create: `scripts/build-runtime.sh`
- Create: `sandbox/start.sh`
- Create: `sandbox/start_test.go`
- Create: `internal/runtime/bundle.go`
- Create: `internal/runtime/bundle_test.go`

- [ ] **Step 1: Write a failing bundle-overlay test**

Test `Builder.Prepare(ctx, repoURL, destination)` with an injected runner. For inputs `https://git.example/demo.git` and `/tmp/work/demo`, assert the runner receives `git clone --depth 1 -- https://git.example/demo.git /tmp/work/demo`, runtime files are copied to `/tmp/work/demo/.sandbox`, and the resolved Git revision is returned.

```go
type Result struct { Revision string }

type Builder struct {
    Run runner.Runner
    RuntimeDir string
}

func (b Builder) Prepare(ctx context.Context, repoURL, destination string) (Result, error)
```

Include a rejection test for repository values beginning with `-` and destinations outside the configured work root.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runtime`

Expected: FAIL because `Builder` does not exist.

- [ ] **Step 3: Implement the minimal bundle builder**

Use `exec.CommandContext` through a small `runner.Runner` interface. Pass every argument as a separate argv item and insert `--` before the repository URL. Copy files with Go filesystem APIs; do not invoke a shell for user input.

- [ ] **Step 4: Write the sandbox launcher contract test**

The test reads `sandbox/start.sh` and asserts it contains these ordered operations:

```text
herdr server
collie pack join
bun run bridge/index.ts
```

It must also assert that the join token is read from a file path in `COLLIE_JOIN_TOKEN_FILE`, not placed in argv or printed.

- [ ] **Step 5: Implement the launcher and runtime build script**

`scripts/build-runtime.sh` must:

1. Run `bun install --frozen-lockfile` and `bun run build` in `collie/`.
2. Copy the Collie checkout needed by its bridge, the `bun` binary, the `herdr` binary, and `sandbox/start.sh` into `sandbox/runtime/`.
3. Fail with a clear message if either binary is absent.

`sandbox/start.sh` must set writable `HOME`, XDG config/state paths under `/home/vcap/.sandbox-state`, create the managed `/home/vcap/app/.cfignore`, start `herdr server`, wait for its socket, run one-time Pack join when no trust store exists, and finally `exec` peer Collie. Write child logs to stdout/stderr.

- [ ] **Step 6: Verify the bundle locally**

Run: `bash scripts/build-runtime.sh && go test ./internal/runtime ./sandbox`

Expected: PASS and `sandbox/runtime/` contains executable `bin/herdr`, `bin/bun`, and `bin/collie` artifacts.

- [ ] **Step 7: Commit**

```bash
git add .gitignore scripts/build-runtime.sh sandbox/start.sh sandbox/start_test.go internal/runner internal/runtime
git commit -m "feat: assemble sandbox runtime overlay"
```

### Task 3: Run The Manual CF Staging Spike

**Files:**
- Create: `docs/spikes/cf-buildpack-runtime.md`
- Create: `fixtures/hello-ruby/Gemfile`
- Create: `fixtures/hello-ruby/app.rb`

- [ ] **Step 1: Prepare the fixture and runtime overlay**

The fixture must require one Ruby gem and print its version, proving the selected buildpack contributes runtime binaries independently of the overlaid Herdr/Collie files.

- [ ] **Step 2: Push the fixture with no route**

Run:

```bash
cf push herdr-runtime-spike --no-route -b ruby_buildpack -p /tmp/herdr-runtime-spike -c './sandbox-runtime/start.sh'
cf logs herdr-runtime-spike --recent
```

Expected: staging succeeds, Ruby and the fixture gem are available, Herdr creates its socket, and peer Collie reaches its enrollment attempt. A failed assumption is not patched around silently; record exact output and revise Task 2's bundle shape before proceeding.

- [ ] **Step 3: Record measured findings**

Write `docs/spikes/cf-buildpack-runtime.md` with CF version, buildpack/version, upload size, staging duration, final command, writable paths, binary preservation result, and any workaround required.

- [ ] **Step 4: Remove the spike app**

Run: `cf delete herdr-runtime-spike -f -r`

Expected: app and routes are absent.

- [ ] **Step 5: Commit**

```bash
git add docs/spikes/cf-buildpack-runtime.md fixtures/hello-ruby
git commit -m "docs: record CF sandbox runtime spike"
```

### Task 4: Add CF Identity-Aware Pack Transport To The Collie Fork

**Files:**
- Create: `collie/bridge/pack/cf-identity.ts`
- Create: `collie/bridge/pack/cf-identity.test.ts`
- Modify: `collie/bridge/config.ts`
- Modify: `collie/bridge/config.test.ts`
- Modify: `collie/bridge/pack/admission.ts`
- Modify: `collie/bridge/pack/admission.test.ts`
- Modify: `collie/bridge/index.ts`
- Modify: `collie/cli/pack.ts`
- Modify: `collie/cli/pack.test.ts`
- Modify: `collie/CHANGELOG.md`

- [ ] **Step 1: Write failing transport-mode tests**

Define an explicit `COLLIE_PACK_TRANSPORT=cf-identity` mode. Tests must prove:

```ts
expect(resolvePackTransport({ COLLIE_PACK_TRANSPORT: "cf-identity" })).toEqual({
  kind: "cf-identity",
  certPath: "/etc/cf-instance-credentials/instance.crt",
  keyPath: "/etc/cf-instance-credentials/instance.key",
});
```

Use `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY` when set. Reject this mode outside peer/lead Pack operation rather than changing solo behavior.

- [ ] **Step 2: Run focused tests to verify failure**

Run: `cd collie && bun test bridge/pack/cf-identity.test.ts bridge/config.test.ts`

Expected: FAIL because the mode is unknown.

- [ ] **Step 3: Implement outbound instance-identity TLS options**

Add a pure helper that loads the mounted certificate/key and returns Bun fetch TLS options without a custom `ca`, `serverName`, or Pack certificate callback:

```ts
export interface CfIdentityTls { readonly cert: string; readonly key: string }
export function cfIdentityTls(paths: CfIdentityPaths, read: (path: string) => string): CfIdentityTls
```

Use it for lead-to-peer Pack fetches and peer-to-lead enrollment fetches in CF mode. Keep the current self-signed pinning path byte-for-byte for default mode.

- [ ] **Step 4: Write failing admission tests for trusted-proxy transport**

Add a transport fact distinct from `transportPinned`, named `platformIdentityAuthorized`. Prove it identifies only the enrolled lead and still requires the Pack secret and protocol header.

```ts
expect(admitPackRequest(peer, facts({
  transportPinned: false,
  platformIdentityAuthorized: true,
  authorization: `Bearer ${peer.pack!.secret}`,
  protocol: "1",
}))).toMatchObject({ ok: true, caller: "member" });
```

Also prove false refuses, wrong secret refuses, and lead mode cannot infer a caller from this fact.

- [ ] **Step 5: Implement CF-mode listener wiring**

In CF mode, serve plain HTTP behind Gorouter, set `platformIdentityAuthorized: true` only on the peer Pack router, and leave `transportPinned: false`. The mode is a deployment assertion that the only mapped route is an identity-aware, default-deny route restricted to the lead app GUID. Log one startup line naming this delegated enforcement.

Do not trust a request header to turn the fact on. Do not expose browser routes in peer mode.

- [ ] **Step 6: Adapt enrollment without weakening Pack identity**

Use the CF instance certificate for the HTTPS connection to the manager enrollment route. Continue validating the lead Pack certificate returned in the enrollment body against the invite fingerprint. Keep the invite token, Pack secret, protocol version, signed requests, and trust-store contents unchanged.

- [ ] **Step 7: Run Collie verification**

Run:

```bash
cd collie
bun test bridge/pack bridge/config.test.ts cli/pack.test.ts
bun run typecheck
cd web && bun run typecheck
```

Expected: PASS.

- [ ] **Step 8: Record the fork change**

Add under Collie's `CHANGELOG.md` Unreleased/Changed: `- Allow Pack transport identity to be enforced by a Cloud Foundry identity-aware route.`

- [ ] **Step 9: Commit inside the Collie repository**

```bash
cd collie
git add bridge/pack/cf-identity.ts bridge/pack/cf-identity.test.ts bridge/config.ts bridge/config.test.ts bridge/pack/admission.ts bridge/pack/admission.test.ts bridge/index.ts cli/pack.ts cli/pack.test.ts CHANGELOG.md
git commit -m "feat(pack): support CF identity-aware transport"
```

Record the resulting Collie commit SHA in the root POC README when that file is introduced in Task 12.

### Task 5: Prove Identity-Aware Enrollment End To End

**Files:**
- Create: `docs/spikes/cf-pack-identity.md`
- Create: `scripts/spike-pack-identity.sh`

- [ ] **Step 1: Script exact CF resources**

The script must create a manager identity route and one sandbox identity route, map each app, and install destination-controlled policies:

```bash
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_HOST" --source "cf:app:$MANAGER_GUID"
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_PACK_HOST" --source "cf:app:$SANDBOX_GUID"
```

All names come from environment variables; quote every expansion. Include a `cleanup` trap that removes policies, routes, and apps.

- [ ] **Step 2: Verify default deny**

From outside an app identity and from a probe app with a different GUID, call the sandbox route.

Expected: TLS client authentication or route policy denies access; the wrong app receives `403`.

- [ ] **Step 3: Verify Pack enrollment and action flow**

Start manager lead Collie, mint an invite, pass the token by file to the sandbox, and start the sandbox. Verify `/pack/v1/hello`, merged snapshot visibility, and one workspace creation or pane action through the lead.

Expected: the manager identity succeeds; Pack's secret/signature checks remain visible in peer audit logs.

- [ ] **Step 4: Record friction and exact timings**

Document policy propagation delay, required route commands, certificate paths, Gorouter/backend protocol behavior, Collie restart requirements, and all temporary workarounds in `docs/spikes/cf-pack-identity.md`.

- [ ] **Step 5: Commit**

```bash
git add docs/spikes/cf-pack-identity.md scripts/spike-pack-identity.sh
git commit -m "docs: prove identity-aware Pack enrollment"
```

## Milestone 2: Build The Control Plane

### Task 6: Persist Sandbox Lifecycle State

**Files:**
- Create: `internal/model/sandbox.go`
- Create: `internal/store/file.go`
- Create: `internal/store/file_test.go`

- [ ] **Step 1: Write failing round-trip and recovery tests**

Define phases `creating`, `staging`, `starting`, `securing-route`, `joining-pack`, `ready`, `deleting`, and `failed`. Test atomic save/load, missing-file-as-empty, malformed-file failure, and preservation of failed records.

```go
type Sandbox struct {
    Name            string      `json:"name"`
    AppGUID         string      `json:"appGuid,omitempty"`
    Repository      string      `json:"repository"`
    Revision        string      `json:"revision,omitempty"`
    Buildpack       string      `json:"buildpack"`
    Desired         Desired     `json:"desired"`
    Phase           Phase       `json:"phase"`
    InternalHost    string      `json:"internalHost,omitempty"`
    PackMemberID    string      `json:"packMemberId,omitempty"`
    LastError       string      `json:"lastError,omitempty"`
    Operations      []Operation `json:"operations,omitempty"`
    CreatedAt       time.Time   `json:"createdAt"`
    UpdatedAt       time.Time   `json:"updatedAt"`
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store`

Expected: FAIL because the store does not exist.

- [ ] **Step 3: Implement atomic persistence**

Write to a unique temporary sibling, `Sync`, close, rename, and use a mutex around read-modify-write. Expose `List`, `Get`, and `Update(name, func(*Sandbox) error)`; sort list results by creation time then name.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/model ./internal/store -race`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model internal/store
git commit -m "feat: persist sandbox lifecycle state"
```

### Task 7: Implement Safe CF Provider Operations

**Files:**
- Create: `internal/runner/exec.go`
- Create: `internal/cf/provider.go`
- Create: `internal/cf/provider_test.go`

- [ ] **Step 1: Write failing argv and redaction tests**

Use a recording runner to prove exact argument arrays for app creation, GUID lookup, push, route mapping, route-policy add/remove, app inspection, and deletion. Include malicious names and repositories to prove no shell interpolation occurs.

```go
type Provider interface {
    Push(context.Context, PushRequest) (App, Operation, error)
    SecureRoute(context.Context, RouteRequest) (Operation, error)
    RemoveRoute(context.Context, RouteRequest) (Operation, error)
    DeleteApp(context.Context, string) (Operation, error)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/cf`

Expected: FAIL because `Provider` does not exist.

- [ ] **Step 3: Implement commands as argv arrays**

For a fixture request named `demo`, use exact argv shapes such as `cf push demo --no-route -b ruby_buildpack -p /tmp/work/demo -c ./sandbox-runtime/start.sh`, `cf app demo --guid`, and the corresponding `cf create-route`, `cf map-route`, `cf add-route-policy`, remove, and `cf delete demo -f` forms. Production values replace fixture values as individual argv entries. Capture bounded output and duration in `model.Operation`.

Validate app/host names with `^[a-z][a-z0-9-]{0,47}$`. Validate buildpack membership before running any command. Never persist join tokens, certificates, keys, or full environment output.

- [ ] **Step 4: Add eventual-consistency inspection**

Build `cf curl` paths with `fmt.Sprintf("/v3/apps/%s/processes", url.PathEscape(guid))`, then query each returned process at `fmt.Sprintf("/v3/processes/%s/stats", url.PathEscape(processGUID))`. Parse both JSON responses into typed minimal structs and return observed state as values rather than string-matching human CLI output.

- [ ] **Step 5: Run tests**

Run: `gofmt -w internal && go test ./internal/cf ./internal/runner -race`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cf internal/runner
git commit -m "feat: add Cloud Foundry sandbox provider"
```

### Task 8: Supervise Lead Collie And Automate Pack Enrollment

**Files:**
- Create: `internal/supervisor/collie.go`
- Create: `internal/supervisor/collie_test.go`
- Create: `internal/pack/manager.go`
- Create: `internal/pack/manager_test.go`

- [ ] **Step 1: Write failing supervisor tests**

Test start, readiness timeout, serialized restart, child exit reporting, and graceful shutdown with an injected process interface. Restart must stop the old process before starting the new one.

- [ ] **Step 2: Implement the Collie child supervisor**

Launch Collie's bridge with manager-controlled config/state directories and loopback address. Stream logs to manager stdout/stderr. Expose:

```go
type Collie interface {
    Start(context.Context) error
    Restart(context.Context) error
    Ready(context.Context) error
    Stop(context.Context) error
}
```

- [ ] **Step 3: Write failing Pack manager tests**

Test that `PrepareEnrollment` invokes `collie pack invite --address` followed by `https://` plus `Config.ManagerPackHost`, extracts the one-time token without logging it, writes it to a mode-0600 temporary file, restarts lead Collie, and returns only the file path to the reconciler. Test `MemberPresent` and `RemoveMember` using `collie pack status`/`remove` with structured or narrowly parsed output.

- [ ] **Step 4: Implement Pack lifecycle operations**

Keep token lifetime bounded to one reconciliation attempt. Delete its file after successful enrollment or terminal failure. Restart lead Collie after trust-store changes because Collie reads Pack state at process startup.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/supervisor ./internal/pack -race`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/supervisor internal/pack
git commit -m "feat: manage lead Collie Pack membership"
```

### Task 9: Implement The Reconciliation State Machine

**Files:**
- Create: `internal/reconcile/reconciler.go`
- Create: `internal/reconcile/reconciler_test.go`

- [ ] **Step 1: Write failing creation sequence tests**

Use fake runtime, CF, Pack, identity probe, and store ports. Assert exact transitions and call order:

```text
prepare bits -> prepare invite -> push no-route -> discover GUID ->
create/map identity route -> add manager source policy ->
add sandbox source policy to manager enrollment route -> wait app ->
wait identity route -> observe Pack member -> ready
```

Assert every transition is persisted before the next external side effect and every operation duration is appended.

- [ ] **Step 2: Write failing retry tests**

Cover restart at each phase, duplicate resource responses, policy propagation timeout, staging failure, expired invite, and manager restart. A retry must resume from observed state rather than recreate completed resources.

- [ ] **Step 3: Implement minimal creation reconciliation**

Use one worker and a keyed in-flight set for the POC. Reconcile pending records on startup and every configured interval. Set `failed` with a sanitized error but retain the previous intended phase so Retry can continue it.

- [ ] **Step 4: Write failing deletion sequence tests**

Assert actions are blocked first, then Pack member removed, sandbox-to-manager policy removed, manager-to-sandbox policy/route removed, app deleted, and finally record removed. Simulate each cleanup failure and assert the record remains retryable.

- [ ] **Step 5: Implement deletion reconciliation**

Treat already-absent Pack members, policies, routes, and apps as success. Never delete the state record until all external resources are confirmed absent.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/reconcile -race -count=10`

Expected: PASS without ordering flakes.

- [ ] **Step 7: Commit**

```bash
git add internal/reconcile
git commit -m "feat: reconcile CF sandbox lifecycle"
```

## Milestone 3: Expose The Experience

### Task 10: Add Manager API And Collie Reverse Proxy

**Files:**
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/server_test.go`
- Create: `internal/httpapi/sanitize.go`
- Create: `internal/httpapi/sanitize_test.go`
- Create: `internal/identity/client.go`
- Create: `internal/identity/client_test.go`
- Modify: `cmd/manager/main.go`

- [ ] **Step 1: Write failing public projection tests**

Define browser `SandboxView` without `InternalHost`, app GUID, Pack token/file, route-policy source, or raw command output. Use reflection to assert forbidden JSON field names cannot appear.

- [ ] **Step 2: Write failing API tests**

Test:

- `GET /manager/api/sandboxes`
- `POST /manager/api/sandboxes` with name/repository/buildpack
- `POST /manager/api/sandboxes/:name/retry`
- `DELETE /manager/api/sandboxes/:name`

Assert allow-list validation, duplicate conflict, JSON content types, bounded body size, and deletion changing desired state rather than directly issuing CF commands.

- [ ] **Step 3: Implement manager handlers**

Return `202 Accepted` for create/retry/delete and let reconciliation perform side effects. Include phases, timings, sanitized errors, repository, revision, and buildpack in responses.

- [ ] **Step 4: Write and implement reverse-proxy tests**

Route `/collie/*` to loopback lead Collie. Strip the prefix while preserving query strings, streaming bodies, Web/PWA assets, and response headers. Do not proxy `/manager/*`. Add a root redirect to `/manager/`.

Add a second, host-gated proxy rule: requests whose canonical `Host` equals `Config.ManagerPackHost`
may proxy `/pack/v1/*` to lead Collie; all other paths on that host return 404. The ordinary public
host must return 404 for `/pack/v1/*`, even though both CF routes map to the same manager process.
Tests must set both hostnames and prove the four allow/deny combinations.

- [ ] **Step 5: Implement the identity client**

Load `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY` for every new TLS config so certificate rotation does not require manager restart. Use system roots for Gorouter. Add timeouts and a bounded response body. Tests use temporary certificates and assert missing/unreadable files fail closed.

- [ ] **Step 6: Wire process assembly**

In `main`, load config/store, start lead Collie, start reconciler, serve HTTP, and stop in reverse order on SIGTERM. Make all long-lived components return errors through one channel.

- [ ] **Step 7: Run tests**

Run: `go test ./... -race`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/manager internal/httpapi internal/identity
git commit -m "feat: expose sandbox API and Collie gateway"
```

### Task 11: Build The Collie-Style Manager Frontend

**Files:**
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/vitest.config.ts`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Create: `web/src/router.tsx`
- Create: `web/src/index.css`
- Create: `web/src/lib/api.ts`
- Create: `web/src/lib/types.ts`
- Create: `web/src/routes/root.tsx`
- Create: `web/src/routes/sandboxes.tsx`
- Create: `web/src/routes/sandboxes.test.tsx`
- Create: `web/src/components/ui/button.tsx`
- Create: `web/src/components/ui/input.tsx`
- Create: `web/src/components/ui/select.tsx`
- Create: `web/src/components/sandbox-card.tsx`
- Create: `web/src/components/create-sandbox-form.tsx`
- Create: `web/src/test/setup.ts`
- Create: `web/src/test/handlers.ts`

- [ ] **Step 1: Scaffold the Collie-compatible frontend stack**

Copy dependency versions and TypeScript/Vite/Tailwind conventions from `collie/web/package.json`, `collie/web/vite.config.ts`, and relevant base tokens from `collie/web/src/index.css`. Do not import source files across repository boundaries; the manager is independently built.

- [ ] **Step 2: Write failing route tests**

With MSW fixtures, assert the page renders existing sandboxes, the form exposes Name/Git repository/Buildpack, invalid submissions stay client-side, valid submit posts exact JSON, and delete requires confirmation.

- [ ] **Step 3: Run tests to verify failure**

Run: `cd web && bun install && bun run test`

Expected: FAIL because routes/components are incomplete.

- [ ] **Step 4: Implement typed API and React Router loader**

Use React Router data mode. Loader fetches `/manager/api/sandboxes`; mutations call typed API functions then `revalidator.revalidate()`. Do not add TanStack Query or local duplicate server state.

- [ ] **Step 5: Implement the mobile-first sandbox experience**

Render a compact create form followed by phase cards. Each card shows buildpack, repository/revision, elapsed phase time, lifecycle step rail, sanitized last error, Retry when failed, Open Agents when ready, and Delete. For a safe API member ID such as `demo-ruby`, `Open Agents` navigates to `/collie/?h=demo-ruby`; omit the action until the member ID exists.

Use Collie's square 2px radius, neutral paper surfaces, monospace operational data, status colors, and strong structural rules. Add a top-level `Sandboxes | Agents` switch; Agents links to `/collie/`.

- [ ] **Step 6: Add polling while work is active**

Use `useRevalidator()` at 1500 ms only while any sandbox is nonterminal. Relax to 10 seconds when all records are ready/failed. Pause while `document.hidden`.

- [ ] **Step 7: Verify frontend**

Run:

```bash
cd web
bun run test
bun run typecheck
bun run build
```

Expected: PASS and `web/dist/index.html` exists.

- [ ] **Step 8: Commit**

```bash
git add web
git commit -m "feat: add sandbox management frontend"
```

### Task 12: Package And Deploy The Manager

**Files:**
- Create: `manifest.yml`
- Create: `Procfile`
- Create: `scripts/build.sh`
- Create: `README.md`
- Modify: `.gitignore`

- [ ] **Step 1: Write the build script**

Build runtime bundle, frontend, and static Go binary into `dist/`. Copy `web/dist`, sandbox runtime, and the required Collie CLI/bridge assets beside the manager binary. Fail if any expected artifact is absent.

- [ ] **Step 2: Add deployment manifest**

The manager manifest must use no hard-coded secrets and set only nonsecret defaults. Command: `./manager`. Health endpoint: `/manager/healthz`. Configure one ordinary public route for the manager and one separately named route on `CF_IDENTITY_DOMAIN` for Pack enrollment.

- [ ] **Step 3: Document setup and known dirt**

README sections must include prerequisites, required CF permissions, buildpack allow-list, identity domain, manager GUID discovery, deployment, cleanup, architecture, Collie fork SHA, and a “POC friction log” linking both spike documents. State explicitly that sandbox routes are never public and browser traffic always passes through manager/lead Collie.

- [ ] **Step 4: Build locally**

Run: `bash scripts/build.sh`

Expected: `dist/manager`, `dist/web/index.html`, and `dist/sandbox/runtime/` exist.

- [ ] **Step 5: Push manager**

Run: `cf push -f manifest.yml`

Expected: manager health endpoint is healthy and lead Collie starts under supervision.

- [ ] **Step 6: Commit**

```bash
git add manifest.yml Procfile scripts/build.sh README.md .gitignore
git commit -m "feat: package CF Herdr sandbox manager"
```

## Milestone 4: Verify And Capture Friction

### Task 13: Add The Real-CF Smoke Test

**Files:**
- Create: `scripts/smoke.sh`
- Create: `docs/smoke-test.md`

- [ ] **Step 1: Implement cleanup-safe smoke automation**

The script creates a uniquely named sandbox through the manager API, polls phases with a deadline, and installs a trap that requests deletion and directly cleans remaining CF resources if the manager cannot. It must not print credentials, join tokens, certificates, or state-file contents.

- [ ] **Step 2: Assert security boundaries**

The script must verify:

1. No ordinary public route is mapped to the sandbox app.
2. Calling the identity route without an instance certificate fails.
3. A wrong app identity receives `403`.
4. The manager identity reaches `/pack/v1/hello`.
5. The manager API response contains no identity hostname, app GUID, certificate path, or Pack secret.

- [ ] **Step 3: Assert user flow**

Create a Herdr workspace or start an OpenCode agent through lead Collie, verify it appears under the sandbox member, send one harmless prompt/action, then delete the sandbox. Confirm Pack member, policies, route, app, and manager record disappear.

- [ ] **Step 4: Run the smoke test**

Run: `bash scripts/smoke.sh`

Expected: PASS with a concise timing table for clone, upload, stage, start, policy propagation, Pack enrollment, ready, and delete.

- [ ] **Step 5: Record observed results**

Write actual environment/version, timing table, retries, failed assumptions, manual interventions, and proposed CF feature opportunities to `docs/smoke-test.md`. Separate observed facts from recommendations.

- [ ] **Step 6: Commit**

```bash
git add scripts/smoke.sh docs/smoke-test.md
git commit -m "test: cover end-to-end sandbox lifecycle"
```

### Task 14: Final Verification

**Files:**
- Modify only files required by failures found below.

- [ ] **Step 1: Run Go checks**

Run: `gofmt -w cmd internal && go vet ./... && go test ./... -race`

Expected: PASS with no changed files after a second `gofmt` check.

- [ ] **Step 2: Run manager frontend checks**

Run: `cd web && bun run test && bun run typecheck && bun run build`

Expected: PASS.

- [ ] **Step 3: Run Collie fork checks**

Run:

```bash
cd collie
bun run lint
bun run test
bun run typecheck
cd web && bun run test && bun run typecheck
```

Expected: PASS.

- [ ] **Step 4: Run artifact and real-CF checks**

Run: `bash scripts/build.sh && bash scripts/smoke.sh`

Expected: PASS; cleanup reports no leaked app, route, or policy.

- [ ] **Step 5: Inspect repository state**

Run: `git status --short` in both root and `collie/`, then inspect root and Collie diffs.

Expected: only intentional changes remain; runtime binaries, state, credentials, and temporary join files are untracked by neither repository.

- [ ] **Step 6: Commit verification fixes if needed**

```bash
git add path/to/each/file-fixed-during-verification
git commit -m "fix: address sandbox POC verification findings"
```

Skip this step when verification required no edits.
