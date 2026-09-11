# Sandbox Cloud Foundry CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package `cf` into new sandboxes, provision the manager CF context during enrollment, and initialize an authenticated, targeted CF CLI session in interactive shells.

**Architecture:** Reuse the existing verified CF CLI artifact and runtime relocation pipeline, installing a sandbox copy at `sandbox/runtime/bin/cf` while retaining the manager copy. Extend the existing enrollment command batch with the manager's five CF context variables, and extend the launcher-managed `.bashrc` block with best-effort `cf api`, `cf auth`, and `cf target` commands that never print secrets.

**Tech Stack:** Bash launcher, Docker/BuildKit runtime packaging, Go Cloud Foundry provider and configuration code, Go contract tests, Markdown documentation.

---

## File Map

- `scripts/build-runtime.sh`: validate and install the CF CLI into the sandbox runtime in addition to the manager runtime.
- `docker/cflinuxfs5-builder/Dockerfile`: require `sandbox/runtime/bin/cf` in build output validation.
- `internal/runtime/bundle.go` and tests: validate the sandbox CF executable as a runtime asset.
- `sandbox/start-bash.sh`: export CF variables and maintain the idempotent interactive CF setup block.
- `sandbox/start_test.go`: verify CF launcher ordering, secret-safe shell content, and standalone behavior.
- `internal/cf/provider.go` and tests: provision CF context during sandbox enrollment without exposing values.
- `internal/reconcile/reconciler.go` and tests: pass manager CF configuration into enrollment.
- `internal/config/config.go` and tests: expose the already loaded CF settings to reconciliation.
- `scripts/direct-sandbox.sh` and tests: retain direct-sandbox behavior without injecting manager credentials while allowing the packaged `cf` binary to resolve.
- `README.md` and sandbox runtime documentation: document the new CF CLI contract and safe smoke checks.

### Task 1: Add failing runtime packaging coverage

**Files:**
- Modify: `scripts/build_runtime_test.go`
- Modify: `internal/runtime/bundle_test.go`
- Modify: `scripts/package_test.go`

- [ ] **Step 1: Require sandbox CF in build-runtime contract tests**

Extend the required sandbox artifact assertions to include `bin/cf`. In the script contract test, assert that the runtime builder has a validated `cf_bin` and installs it into `"$RUNTIME_DIR/bin/cf"` in both relocation and non-relocation branches. Keep the existing manager installation assertion unchanged.

- [ ] **Step 2: Require sandbox CF in Docker output validation tests**

Update the Dockerfile contract test's executable list so it requires:

```text
sandbox/runtime/bin/cf
```

The test must continue to require `manager-runtime/bin/cf` separately.

- [ ] **Step 3: Require sandbox CF in runtime bundle validation tests**

Add `bin/cf` to the sandbox required-asset fixture and add a failure assertion that removing it makes `ValidateSandboxRuntime` return an error naming `cf`.

- [ ] **Step 4: Run the focused tests and verify failure**

Run:

```bash
go test ./scripts ./internal/runtime -run 'Test.*(Runtime|Package|Bundle)' -count=1
```

Expected: FAIL because the production runtime builder and validator do not yet create or require `sandbox/runtime/bin/cf`.

### Task 2: Install CF into the sandbox runtime

**Files:**
- Modify: `scripts/build-runtime.sh`, next to the existing `cf_bin` validation and runtime installation branches
- Modify: `docker/cflinuxfs5-builder/Dockerfile`, in the generated executable validation loop

- [ ] **Step 1: Validate the packaged CF executable once**

Keep the existing line:

```bash
cf_bin="$(validate_runtime_binary CF_BIN)"
```

and use that validated path for both runtime destinations. Do not download a second artifact or bypass `validate_runtime_binary`.

- [ ] **Step 2: Install CF beside the sandbox tools**

In the relocation branch, add:

```bash
relocate_cf_wrapper "$cf_bin" "$RUNTIME_DIR/bin/cf" "$TARGET_INSTALL_DIR"
if ! "$RUNTIME_DIR/bin/cf" version >/dev/null 2>&1; then
	printf 'error: relocated sandbox CF CLI smoke test failed: %s\n' "$RUNTIME_DIR/bin/cf" >&2
	exit 1
fi
```

In the portable branch, add:

```bash
install -m 0755 "$cf_bin" "$RUNTIME_DIR/bin/cf"
```

Place the installation before the manager-runtime branch and retain the existing manager CF installation. The final `scan_elf_metadata "$RUNTIME_DIR"` must cover the new asset.

- [ ] **Step 3: Require the generated sandbox CF asset**

Add `sandbox/runtime/bin/cf` to the Dockerfile's final executable loop immediately beside the other sandbox binaries.

- [ ] **Step 4: Run packaging tests and verify success**

Run:

```bash
go test ./scripts ./internal/runtime -count=1
```

Expected: PASS.

### Task 3: Add failing enrollment credential coverage

**Files:**
- Modify: `internal/cf/provider_test.go`
- Modify: `internal/reconcile/reconciler_test.go`

- [ ] **Step 1: Assert the provider emits all enrollment variables**

Extend the exact command expectation for `ConfigureEnrollment` to include, after `HERDR_SOCKET_PATH` and before the existing Pack variables:

```go
{"set-env", "demo", "CF_API", "https://api.example"},
{"set-env", "demo", "CF_USERNAME", "manager"},
{"set-env", "demo", "CF_PASSWORD", "deploy-password"},
{"set-env", "demo", "CF_ORG", "poc"},
{"set-env", "demo", "CF_SPACE", "demo"},
```

Use the existing fake command runner and ensure the test output assertions do not print the password.

- [ ] **Step 2: Add reconciliation propagation coverage**

Extend the reconciler test configuration with `CFAPI`, `CFUsername`, `CFPassword`, `CFOrg`, and `CFSpace`, then assert the fake CF provider receives those five values when enrollment is configured. Add fields to the fake provider only if needed to observe the values; do not change the production interface beyond adding a credential-bearing enrollment argument.

- [ ] **Step 3: Run the focused tests and verify failure**

Run:

```bash
go test ./internal/cf ./internal/reconcile -run 'Test(ConfigureEnrollment|.*Enrollment.*)' -count=1
```

Expected: FAIL because `ConfigureEnrollment` currently accepts only token path, lead address, and self address.

### Task 4: Thread manager CF configuration through enrollment

**Files:**
- Modify: `internal/cf/provider.go`
- Modify: `internal/reconcile/reconciler.go`
- Modify: `internal/config/config.go`, if needed to expose the loaded CF fields through the reconciler configuration
- Modify: affected Go tests from Task 3

- [ ] **Step 1: Extend the enrollment interface**

Define `EnrollmentConfig` in `internal/cf/provider.go` and change the provider and reconciler enrollment method shape to accept a value object containing:

```go
type EnrollmentConfig struct {
	TokenAppPath string
	LeadAddress  string
	SelfAddress  string
	CFAPI        string
	CFUsername   string
	CFPassword   string
	CFOrg        string
	CFSpace      string
}
```

Keep the existing fixed token path, HTTPS lead validation, and host validation. Validate all five CF fields as non-empty before constructing commands, returning an error that names the missing field but never includes its value. The reconciler interface should use `cf.EnrollmentConfig` rather than duplicating this type.

- [ ] **Step 2: Append credential-safe `cf set-env` commands**

Construct the command batch with:

```go
{"set-env", name, "CF_API", enrollment.CFAPI},
{"set-env", name, "CF_USERNAME", enrollment.CFUsername},
{"set-env", name, "CF_PASSWORD", enrollment.CFPassword},
{"set-env", name, "CF_ORG", enrollment.CFOrg},
{"set-env", name, "CF_SPACE", enrollment.CFSpace},
```

Do not include values in formatted errors, operation names, or logs. Preserve the existing enrollment variables and command ordering.

- [ ] **Step 3: Pass manager configuration from reconciliation**

Populate the enrollment value object from the manager's loaded `config.Config` fields in `effectConfigureEnrollment`. The manager's existing `ValidateProduction` remains the source of truth for required credentials.

- [ ] **Step 4: Run provider and reconciliation tests**

Run:

```bash
go test ./internal/cf ./internal/reconcile -count=1
```

Expected: PASS, with no secret values in test failure output.

### Task 5: Add failing launcher CF setup coverage

**Files:**
- Modify: `sandbox/start_test.go`

- [ ] **Step 1: Add launcher contract assertions**

Require these fragments in the executable-line script:

```bash
export CF_API="${CF_API:-}"
export CF_USERNAME="${CF_USERNAME:-}"
export CF_PASSWORD="${CF_PASSWORD:-}"
export CF_ORG="${CF_ORG:-}"
export CF_SPACE="${CF_SPACE:-}"
```

Require the CF setup commands in this order:

```bash
"$BIN_DIR/cf" api "$CF_API" --skip-ssl-validation
"$BIN_DIR/cf" auth "$CF_USERNAME" "$CF_PASSWORD"
"$BIN_DIR/cf" target -o "$CF_ORG" -s "$CF_SPACE"
```

Assert all three commands occur after `.bashrc` creation begins and before Herdr starts. Also assert the launcher source contains neither `set -x` nor an unquoted password expansion.

- [ ] **Step 2: Add a runtime helper test for CF command order**

Add a fake `cf` role in `prepareLauncher`. The helper must record `api`, `auth`, and `target` arguments without writing the password to the log, and return success. Add `TestLauncherInitializesCFSessionWhenConfigured` with:

```text
CF_API=https://api.example
CF_USERNAME=manager
CF_PASSWORD=deploy-password
CF_ORG=poc
CF_SPACE=demo
```

Assert the log contains `cf api`, `cf auth`, and `cf target` in order, contains no `deploy-password`, and still reaches `bun started`.

- [ ] **Step 3: Add standalone behavior coverage**

Add `TestLauncherStartsWithoutCFSessionWhenCredentialsAreMissing`, using the existing standalone setup. Assert the fake CF role is not invoked and Bun still starts.

- [ ] **Step 4: Run the focused tests and verify failure**

Run:

```bash
go test ./sandbox -run 'TestLauncher(Contract|InitializesCFSession|StartsWithoutCFSession)' -count=1
```

Expected: FAIL because the launcher has no CF exports or setup commands.

### Task 6: Initialize CF from the managed Bash block

**Files:**
- Modify: `sandbox/start-bash.sh`

- [ ] **Step 1: Export the optional CF environment**

After the existing runtime exports, add:

```bash
export CF_API="${CF_API:-}"
export CF_USERNAME="${CF_USERNAME:-}"
export CF_PASSWORD="${CF_PASSWORD:-}"
export CF_ORG="${CF_ORG:-}"
export CF_SPACE="${CF_SPACE:-}"
```

These preserve values supplied by CF app environment and remain empty for direct standalone sandboxes.

- [ ] **Step 2: Add the idempotent `.bashrc` setup**

Extend the existing block with the five quoted exports and a guarded setup command block that runs when a shell sources `.bashrc`. Use a managed sentinel variable such as `CF_HERDR_SANDBOX_INITIALIZED=1` so the block does not execute setup twice in one shell. Use the absolute `$BIN_DIR/cf` path in the block so it does not depend on PATH ordering. Do not invoke CF setup from the noninteractive launcher path; this is specifically an interactive shell initialization contract.

The block must run these commands only when all five variables are non-empty:

```bash
"$BIN_DIR/cf" api "$CF_API" --skip-ssl-validation
"$BIN_DIR/cf" auth "$CF_USERNAME" "$CF_PASSWORD"
"$BIN_DIR/cf" target -o "$CF_ORG" -s "$CF_SPACE"
```

Each command is best effort: print a generic warning on failure, never print the password, and allow the shell to remain usable for retry or diagnosis.

- [ ] **Step 3: Run launcher tests**

Run:

```bash
go test ./sandbox -count=1
```

Expected: PASS, including existing signal, bootstrap, `.bashrc`, and standalone tests.

### Task 7: Update runtime documentation and smoke checks

**Files:**
- Modify: `README.md`, in runtime and sandbox operations sections
- Modify: `docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md`, to add `cf` to the runtime environment contract

- [ ] **Step 1: Document the packaged CF CLI**

State that new manager-created sandboxes expose `cf` from `sandbox-runtime/bin/cf`, and that the manager provisions `CF_API`, `CF_USERNAME`, `CF_PASSWORD`, `CF_ORG`, and `CF_SPACE` during enrollment. State that direct sandboxes do not receive manager credentials by default.

- [ ] **Step 2: Document safe verification**

Add a smoke check that does not print credentials:

```bash
cf ssh <sandbox-name> -c 'command -v cf; cf version; cf target'
```

Do not add commands that print `CF_PASSWORD` or dump the full environment.

- [ ] **Step 3: Run documentation checks**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

### Task 8: Verify, build, and review the complete change

**Files:**
- No additional files

- [ ] **Step 1: Run all affected Go tests**

Run:

```bash
go test ./sandbox ./internal/cf ./internal/config ./internal/reconcile ./internal/runtime ./scripts
```

Expected: PASS.

- [ ] **Step 2: Validate shell syntax**

Run:

```bash
bash -n sandbox/start.sh sandbox/start-bash.sh scripts/build-runtime.sh scripts/direct-sandbox.sh
```

Expected: PASS with no output.

- [ ] **Step 3: Build the distribution**

Run:

```bash
devbox run deploy
```

Expected: the build output contains executable `dist/sandbox/runtime/bin/cf` and the manager deployment completes without printing credential values. If deployment credentials or CF access are unavailable, run the build-only command used by `scripts/build.sh` with the repository's pinned artifact inputs and report deployment as unverified; do not invent credentials or add them to files.

- [ ] **Step 4: Inspect runtime artifacts without secrets**

Run:

```bash
test -x dist/sandbox/runtime/bin/cf
dist/sandbox/runtime/bin/cf version
git diff --check
```

Expected: the executable exists, reports its version, and the diff is clean.

- [ ] **Step 5: Review status and commit**

Run:

```bash
git status --short
git diff -- sandbox/start-bash.sh sandbox/start_test.go scripts/build-runtime.sh docker/cflinuxfs5-builder/Dockerfile internal/runtime/bundle.go internal/runtime/bundle_test.go internal/cf/provider.go internal/cf/provider_test.go internal/reconcile/reconciler.go internal/reconcile/reconciler_test.go scripts/build_runtime_test.go scripts/package_test.go README.md docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md
```

Confirm no `.secrets`, credential values, or unrelated deployment files are staged. Commit with:

```bash
git add sandbox/start-bash.sh sandbox/start_test.go scripts/build-runtime.sh docker/cflinuxfs5-builder/Dockerfile internal/runtime/bundle.go internal/runtime/bundle_test.go internal/cf/provider.go internal/cf/provider_test.go internal/reconcile/reconciler.go internal/reconcile/reconciler_test.go scripts/build_runtime_test.go scripts/package_test.go README.md docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md
git commit -m "feat: provide CF CLI access in sandboxes"
```
