# OpenCode Sandbox Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package a pinned OpenCode Linux release into every sandbox and make OpenCode, Herdr, and the shared Herdr socket available from `cf ssh` and Collie terminal sessions.

**Architecture:** Extend the existing cflinuxfs5 artifact manifest and Docker builder with OpenCode's architecture-specific tarballs, checksum verification, and executable extraction. Extend the visible sandbox runtime launcher with a managed PATH/socket environment block in `.bashrc`, and configure the same values through direct and manager-created CF app environment commands.

**Tech Stack:** Bash, Docker/BuildKit, Cloud Foundry CLI, Go contract tests, GitHub release artifacts, Unix sockets.

---

## File Map

- Modify `docker/cflinuxfs5-builder/artifacts.env` with OpenCode v1.18.30 metadata.
- Modify `scripts/select-cflinuxfs5-artifacts.sh` to select and validate OpenCode URL/checksum pairs.
- Modify `docker/cflinuxfs5-builder/Dockerfile` to download, verify, extract, and expose `opencode`.
- Modify `scripts/build-runtime.sh` to copy OpenCode into the sandbox runtime and validate its packaged executable.
- Modify `scripts/build.sh` to require the generated `sandbox/runtime/bin/opencode` artifact.
- Modify `sandbox/runtime/start.sh` to export PATH/socket values and maintain the managed `.bashrc` block.
- Modify `scripts/direct-sandbox.sh` to validate and set the PATH/socket CF environment values.
- Modify `internal/cf/provider.go` to configure PATH/socket values for manager-created sandbox apps.
- Modify `scripts/package_test.go`, `scripts/build_runtime_test.go`, `scripts/direct_sandbox_test.go`, `internal/cf/provider_test.go`, and `sandbox/start_test.go` for regression coverage.
- Modify `README.md` with the pinned OpenCode artifact and interactive usage contract.

### Task 1: Pin OpenCode Release Metadata

**Files:**
- Modify: `docker/cflinuxfs5-builder/artifacts.env`
- Modify: `scripts/select-cflinuxfs5-artifacts.sh`
- Test: `scripts/package_test.go`
- Test: `scripts/build_runtime_test.go`

- [ ] **Step 1: Write failing manifest and selector tests**

Extend the existing manifest assertions with the latest release values:

```go
"OPENCODE_VERSION=1.18.30",
"OPENCODE_URL_AMD64=https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-x64.tar.gz",
"OPENCODE_SHA256_AMD64=55007246858165496ff85ba1c2b648f7421e8e2013bf4189a680c9ff8e699d17",
"OPENCODE_URL_ARM64=https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-arm64.tar.gz",
"OPENCODE_SHA256_ARM64=4111a55c2a02c0fac314bd51e9a2330280e6d29d2b85b9554fff6d62612566ed",
```

Add selector cases for both architectures and require `OPENCODE_URL` and
`OPENCODE_SHA256` in the selector's output. Update the generic artifact-key list in
the builder contract test to include both OpenCode keys.

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
go test ./scripts -run 'TestManifestPinsVerifiedHerdrArtifacts|TestCFLinuxFS5SelectorUsesPinnedArtifactsForEachArchitecture|TestCFLinuxFS5ArtifactManifestHasPerArchitectureBunInputs|TestCFLinuxFS5ArtifactSelectorUsesManifestKeysForBothArchitectures' -count=1
```

Expected: FAIL because the manifest and selector do not yet expose OpenCode.

- [ ] **Step 3: Add pinned metadata and selector support**

Add the five manifest assignments above to `artifacts.env`. Add `OPENCODE_URL` and
`OPENCODE_SHA256` to the selector's required `name` list. Keep the existing literal
assignment parsing, architecture suffix handling, and non-empty environment override
behavior unchanged.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run the same `go test ./scripts -run ... -count=1` command. Expected: PASS.

- [ ] **Step 5: Commit the metadata change**

```bash
git add docker/cflinuxfs5-builder/artifacts.env scripts/select-cflinuxfs5-artifacts.sh scripts/package_test.go scripts/build_runtime_test.go
git commit -m "build: pin OpenCode sandbox artifact"
```

### Task 2: Download and Package OpenCode in Docker

**Files:**
- Modify: `docker/cflinuxfs5-builder/Dockerfile`
- Test: `scripts/package_test.go`
- Test: `scripts/build_runtime_test.go`

- [ ] **Step 1: Write failing Docker contract tests**

Extend `TestCFLinuxFS5BuilderContract` and the Dockerfile package tests to require:

```text
ARG OPENCODE_URL
ARG OPENCODE_SHA256
OPENCODE_URL is required
/tools/downloads/opencode.tar.gz
/tools/bin/opencode
tar -xzf /tools/downloads/opencode.tar.gz
```

Require the build output contract to mention `sandbox/runtime/bin/opencode` and reject
the unpinned `latest` string as before. Add a fixture assertion that the generated
runtime includes executable `sandbox/runtime/bin/opencode`.

- [ ] **Step 2: Run the focused tests and verify they fail**

```bash
go test ./scripts -run 'TestCFLinuxFS5BuilderContract|TestBuildAssemblesExpectedLayoutWithFixtureTools' -count=1
```

Expected: FAIL because the Dockerfile and fixture output do not include OpenCode.

- [ ] **Step 3: Implement verified archive extraction**

Add Docker build arguments after the existing Herdr arguments:

```dockerfile
ARG OPENCODE_URL
ARG OPENCODE_SHA256
```

In the tools-stage validation, require both values. Download the selected tarball to
`/tools/downloads/opencode.tar.gz`, verify it with `sha256sum -c -`, extract it into a
temporary directory, locate the regular file named `opencode`, and install it as
`/tools/bin/opencode` mode `0755`. Fail if no executable is found or if extraction
produces an ambiguous result. Copying only the executable prevents release metadata or
desktop files from entering the sandbox.

Pass the tools binary into `scripts/build-runtime.sh` through the new
`OPENCODE_RUNTIME_BIN=/tools/bin/opencode` environment variable. Require the resulting
`sandbox/runtime/bin/opencode` in the Docker output validation loop.

- [ ] **Step 4: Update the fixture runtime builder and run tests**

Update `runFixtureBuild` in `scripts/package_test.go` to create an executable
`$RUNTIME_DIR/bin/opencode` alongside the existing fixture binaries. Run:

```bash
go test ./scripts -run 'TestCFLinuxFS5BuilderContract|TestBuildAssemblesExpectedLayoutWithFixtureTools' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Docker packaging**

```bash
git add docker/cflinuxfs5-builder/Dockerfile scripts/package_test.go scripts/build_runtime_test.go
git commit -m "build: package OpenCode in sandbox runtime"
```

### Task 3: Extend Runtime Assembly and Validation

**Files:**
- Modify: `scripts/build-runtime.sh`
- Modify: `scripts/build.sh`
- Modify: `internal/runtime/bundle.go`
- Test: `scripts/build_runtime_test.go`
- Test: `scripts/package_test.go`
- Test: `internal/runtime/bundle_test.go`

- [ ] **Step 1: Write failing runtime contract tests**

Add `OPENCODE_RUNTIME_BIN` to the required runtime inputs and assert that the script
copies it to `RUNTIME_DIR/bin/opencode` in both normal and relocation-compatible paths.
Add `bin/opencode` to the `validateRuntime` required assets and to all generated layout
assertions. Update fixture invocations to provide an executable OpenCode input where
the real runtime builder is exercised.

- [ ] **Step 2: Run focused runtime tests and verify failure**

```bash
go test ./internal/runtime ./scripts -run 'TestBuildRuntime|TestBuildAssemblesExpectedLayout|TestRuntime' -count=1
```

Expected: FAIL because OpenCode is not a required input or output.

- [ ] **Step 3: Implement minimal runtime copying and validation**

In `scripts/build-runtime.sh`, validate `OPENCODE_RUNTIME_BIN` with the same portable
regular-executable checks used for the other packaged runtime inputs, then install it
as `RUNTIME_DIR/bin/opencode`. OpenCode is a release executable, not a Nix-linked
runtime requiring the existing relocation graph; do not add it to the Bun/Herdr/Collie
relocation set unless the artifact validation proves it needs that treatment.

In `scripts/build.sh`, pass `OPENCODE_RUNTIME_BIN` through to the runtime builder when
using externally supplied legacy inputs and require `sandbox/runtime/bin/opencode` in
the final executable loop. In `internal/runtime/bundle.go`, require `bin/opencode`
from `validateRuntime` so overlays cannot omit it.

- [ ] **Step 4: Run focused runtime tests and verify success**

Run the same focused test command. Expected: PASS.

- [ ] **Step 5: Commit runtime assembly**

```bash
git add scripts/build-runtime.sh scripts/build.sh internal/runtime/bundle.go scripts/build_runtime_test.go scripts/package_test.go internal/runtime/bundle_test.go
git commit -m "build: add OpenCode to sandbox runtime contract"
```

### Task 4: Add Launcher PATH and Idempotent `.bashrc` Contract

**Files:**
- Modify: `sandbox/runtime/start.sh`
- Test: `sandbox/start_test.go`

- [ ] **Step 1: Write failing launcher tests**

Add contract assertions that `start.sh` exports the runtime bin directory before
starting Herdr and Collie, and that it contains managed `.bashrc` markers and exports
for:

```text
PATH=/home/vcap/app/sandbox-runtime/bin:$PATH
HERDR_SOCKET_PATH=/home/vcap/app/.sandbox-state/herdr.sock
```

Add an executable integration test using the existing launcher fixture that supplies a
temporary `HOME`, starts the fake Herdr/Collie processes, and checks that the resulting
`.bashrc` contains exactly one managed block after the launcher is run twice while an
unrelated line remains intact.

- [ ] **Step 2: Run launcher tests and verify failure**

```bash
go test ./sandbox -run 'TestLauncher|Test.*Bashrc|Test.*Environment' -count=1
```

Expected: FAIL because the launcher currently does not export PATH or maintain `.bashrc`.

- [ ] **Step 3: Implement the runtime environment and managed block**

After `HOME` and `HERDR_SOCKET_PATH` are established, set:

```bash
export PATH="$BIN_DIR:$PATH"
```

Keep the existing socket default based on `SANDBOX_STATE_DIR`. Add a quoted heredoc or
temporary-file replacement routine that replaces only a block bounded by stable
markers such as `# BEGIN CF HERDR SANDBOX RUNTIME` and `# END CF HERDR SANDBOX RUNTIME`.
The block must export the absolute `BIN_DIR` and current `HERDR_SOCKET_PATH`; create
the parent directory first, preserve unrelated `.bashrc` content, and fail with an
actionable error if the managed update cannot be completed. Do not use `cat > .bashrc`
or append unconditionally.

- [ ] **Step 4: Run launcher tests and verify success**

Run the same focused command. Expected: PASS, including the existing socket startup,
signal, Pack, and cleanup tests.

- [ ] **Step 5: Commit launcher behavior**

```bash
git add sandbox/runtime/start.sh sandbox/start_test.go
git commit -m "feat: expose sandbox runtime in interactive shells"
```

### Task 5: Configure Direct and Manager-Created Sandbox Environments

**Files:**
- Modify: `scripts/direct-sandbox.sh`
- Modify: `internal/cf/provider.go`
- Test: `scripts/direct_sandbox_test.go`
- Test: `internal/cf/provider_test.go`

- [ ] **Step 1: Write failing CF environment tests**

Extend the direct sandbox fake-CF event assertions to require, after push and before
start:

```text
cf set-env direct-sandbox HERDR_SOCKET_PATH /home/vcap/app/.sandbox-state/herdr.sock
```

Require that neither workflow emits a CF `PATH` setting. Add the socket command to the
manager provider enrollment configuration expectation and validate that its value is a
fixed safe path, not a caller-controlled argument.

- [ ] **Step 2: Run focused CF tests and verify failure**

```bash
go test ./internal/cf ./scripts -run 'Test.*(Enrollment|DirectSandbox|Sandbox)' -count=1
```

Expected: FAIL because neither workflow configures the two environment variables.

- [ ] **Step 3: Implement direct sandbox environment configuration**

In `scripts/direct-sandbox.sh`, after the existing `cf push` and before `cf start`,
set only the literal socket value. Do not set `PATH` through CF: `cf set-env` stores
`$PATH` literally and can remove the system paths required during staging. The launcher
prepends its own absolute runtime directory at process startup and updates
`/home/vcap/.bashrc` for interactive shells.

Extend the runtime asset preflight list with `bin/opencode` so direct sandbox setup
fails before creating an app when the packaged CLI is absent.

- [ ] **Step 4: Implement manager provider environment configuration**

Extend `Provider.ConfigureEnrollment` in `internal/cf/provider.go` to issue the fixed
`HERDR_SOCKET_PATH` `set-env` command before the Pack enrollment variables. Do not add a
CF `PATH` setting. Use the fixed socket path, not `p.Environment`, request input, or
repository content. Preserve existing validation and command ordering.

- [ ] **Step 5: Run focused CF tests and verify success**

Run the same focused command. Expected: PASS.

- [ ] **Step 6: Commit CF environment wiring**

```bash
git add scripts/direct-sandbox.sh internal/cf/provider.go scripts/direct_sandbox_test.go internal/cf/provider_test.go
git commit -m "feat: share sandbox Herdr environment with ssh sessions"
```

### Task 6: Document and Run Full Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/spikes/cf-buildpack-runtime.md` if the runtime command section needs the new CLI contract.
- Test: `scripts/package_test.go`

- [ ] **Step 1: Write documentation contract tests**

Extend README assertions to require the OpenCode manifest path, v1.18.30 artifact
version/URLs/checksums, and interactive commands such as:

```bash
cf ssh <sandbox-name> -c '/home/vcap/app/sandbox-runtime/bin/opencode --version'
cf ssh <sandbox-name> -c '/bin/bash -ic "command -v opencode; command -v herdr"'
```

Document that Collie terminal sessions inherit the launcher-provided runtime `PATH`
and `HERDR_SOCKET_PATH`, and that the packaged Herdr command attaches to the server
started by `start.sh`.
Noninteractive `cf ssh -c` does not source `/home/vcap/.bashrc`, so examples must use
absolute paths or explicitly invoke `/bin/bash -ic`.

- [ ] **Step 2: Update README and spike documentation**

Describe manifest refresh as the OpenCode upgrade mechanism, the exact pinned release,
the runtime location, and the no-background-service behavior. Do not claim live CF
verification until the smoke command has actually been run.

- [ ] **Step 3: Run all local verification**

```bash
gofmt -w internal/cf/provider_test.go internal/runtime/bundle_test.go scripts/package_test.go scripts/build_runtime_test.go scripts/direct_sandbox_test.go sandbox/start_test.go
go test ./...
bash -n scripts/direct-sandbox.sh scripts/build-runtime.sh scripts/build-cflinuxfs5.sh scripts/select-cflinuxfs5-artifacts.sh sandbox/runtime/start.sh
```

Expected: all Go tests pass and Bash syntax checks exit successfully. If Docker is
available, also run:

```bash
GOOS=linux GOARCH=amd64 BUILD_MODE=cflinuxfs5 bash scripts/build.sh
test -x dist/sandbox/runtime/bin/opencode
dist/sandbox/runtime/bin/opencode --version
```

- [ ] **Step 4: Run the sandbox smoke verification when credentials and a disposable CF space are available**

Use the existing direct or manager sandbox workflow without printing credentials or
tokens. Verify `cf ssh` resolves `opencode` and `herdr`, prints the configured socket
path, and that a Collie terminal session resolves the same commands. Record measured
friction and observations in `docs/spikes/smoke-test.md` only; do not fabricate live
results.

- [ ] **Step 5: Commit documentation and verification changes**

```bash
git add README.md docs/spikes/cf-buildpack-runtime.md scripts/package_test.go
git commit -m "docs: describe OpenCode sandbox usage"
```

## Final Review Checklist

- [ ] `artifacts.env` pins OpenCode URLs and checksums for amd64 and arm64.
- [ ] Docker fails closed on missing metadata or checksum mismatch and emits only the executable.
- [ ] `sandbox/runtime/bin/opencode` is present and executable in generated output.
- [ ] `start.sh` exports the runtime PATH before Herdr/Collie and uses one shared socket.
- [ ] `.bashrc` updates are idempotent and preserve unrelated content.
- [ ] Direct and manager-created apps configure only the fixed `HERDR_SOCKET_PATH` before start; neither configures CF `PATH`.
- [ ] Existing security rules remain intact: no public sandbox route, no secret logging, no unverified live-result claims.
- [ ] `go test ./...` and Bash syntax checks pass.
