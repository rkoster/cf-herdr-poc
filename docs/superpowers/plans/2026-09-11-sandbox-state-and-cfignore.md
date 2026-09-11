# Sandbox State And CF Ignore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move default sandbox state outside the app directory and create a managed `.cfignore` that prevents runtime, state, and enrollment material from entering agent `cf push` packages.

**Architecture:** Change only the default state/socket contract; preserve `SANDBOX_STATE_DIR` and `HERDR_SOCKET_PATH` overrides. Have `sandbox/start-bash.sh` create `/home/vcap/app/.cfignore` before child processes start, using an atomic temporary-file replacement and exact patterns for `sandbox-runtime/`, `.sandbox-state/`, and `join-token`. Update manager/direct provisioning, tests, and operational documentation to use the new default.

**Tech Stack:** Bash launcher, Go Cloud Foundry provider tests, Go direct-sandbox tests, Markdown documentation.

---

## File Map

- `sandbox/start-bash.sh`: default state path and managed `.cfignore` creation.
- `sandbox/start_test.go`: launcher path and `.cfignore` contract tests.
- `internal/cf/provider.go` and tests: manager enrollment socket path.
- `scripts/direct-sandbox.sh` and tests: direct-sandbox socket path.
- `README.md`, `AGENTS.md`, and sandbox specs/plans: operational path and CF push documentation.

### Task 1: Add failing state and ignore-file tests

**Files:**
- Modify: `sandbox/start_test.go`
- Modify: `internal/cf/provider_test.go`
- Modify: `scripts/direct_sandbox_test.go`

- [ ] **Step 1: Update launcher contract expectations**

Change the required launcher fragment from:

```go
`SANDBOX_STATE_DIR="${SANDBOX_STATE_DIR:-/home/vcap/app/.sandbox-state}"`,
```

to:

```go
`SANDBOX_STATE_DIR="${SANDBOX_STATE_DIR:-/home/vcap/.sandbox-state}"`,
```

Add a runtime test that starts the launcher with a temporary `SANDBOX_STATE_DIR`, waits for the fake Herdr and Bun children, reads `<test home parent>/.cfignore` at the launcher app root, and expects exactly:

```text
sandbox-runtime/
.sandbox-state/
join-token
```

The test must run the launcher twice and assert the file remains exactly three lines with no duplicates.

- [ ] **Step 2: Update provider command expectations**

Change the expected enrollment command from:

```go
{"set-env", "demo", "HERDR_SOCKET_PATH", "/home/vcap/app/.sandbox-state/herdr.sock"}
```

to:

```go
{"set-env", "demo", "HERDR_SOCKET_PATH", "/home/vcap/.sandbox-state/herdr.sock"}
```

- [ ] **Step 3: Update direct-sandbox expectations**

Change every expected direct-sandbox event containing `/home/vcap/app/.sandbox-state/herdr.sock` to `/home/vcap/.sandbox-state/herdr.sock`.

- [ ] **Step 4: Run focused tests and verify failure**

Run:

```bash
```

Expected: FAIL because production defaults and provisioning still use the app-directory state path, and the launcher does not yet create `.cfignore`.

### Task 2: Implement state relocation and managed `.cfignore`

**Files:**
- Modify: `sandbox/start-bash.sh`

- [ ] **Step 1: Change the default state directory**

Replace the launcher default with:

```bash
SANDBOX_STATE_DIR="${SANDBOX_STATE_DIR:-/home/vcap/.sandbox-state}"
```

Leave all explicit `SANDBOX_STATE_DIR` and `HERDR_SOCKET_PATH` overrides unchanged.

- [ ] **Step 2: Add atomic `.cfignore` creation**

Add a function before child processes start:

```bash
configure_cfignore() {
  local cfignore=/home/vcap/app/.cfignore
  local temporary
  temporary="$(mktemp "${cfignore}.XXXXXX")"
  printf '%s\n' 'sandbox-runtime/' '.sandbox-state/' 'join-token' >"$temporary"
  chmod 600 "$temporary"
  mv -- "$temporary" "$cfignore"
}
```

Call `configure_cfignore` immediately after the existing runtime directories and OpenCode integration setup, before starting Herdr. Under `set -euo pipefail`, any failure stops startup. Do not log token contents or delete unrelated files.

- [ ] **Step 3: Run focused tests and verify success**

Run:

```bash
```

Expected: PASS.

- [ ] **Step 4: Validate shell syntax**

Run:

```bash
bash -n sandbox/start.sh sandbox/start-bash.sh scripts/direct-sandbox.sh
```

Expected: PASS with no output.

### Task 3: Update manager and direct-sandbox path contracts

**Files:**
- Modify: `internal/cf/provider.go`
- Modify: `scripts/direct-sandbox.sh`
- Modify: `internal/cf/provider_test.go`
- Modify: `scripts/direct_sandbox_test.go`

- [ ] **Step 1: Update manager enrollment socket path**

Change the fixed `HERDR_SOCKET_PATH` value emitted by `ConfigureEnrollment` to:

```text
/home/vcap/.sandbox-state/herdr.sock
```

Keep the enrollment token path under `/home/vcap/app/sandbox-runtime/join-token`; `.cfignore` protects it from later agent pushes.

- [ ] **Step 2: Update direct-sandbox environment setup**

Change the direct-sandbox `cf set-env` command to:

```bash
"$CF_BIN" set-env "$APP_NAME" HERDR_SOCKET_PATH /home/vcap/.sandbox-state/herdr.sock >/dev/null 2>&1
```

Do not alter the visible runtime path or target-install-dir contract.

- [ ] **Step 3: Run affected tests**

Run:

```bash
```

Expected: PASS.

### Task 4: Update documentation and specifications

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md`
- Modify: `docs/superpowers/specs/2026-09-10-sandbox-shell-environment-design.md`
- Modify: `docs/superpowers/plans/2026-09-10-sandbox-shell-environment.md`
- Modify: `docs/superpowers/plans/2026-09-09-opencode-sandbox-packaging.md`

- [ ] **Step 1: Replace default state-path documentation**

Replace default references to `/home/vcap/app/.sandbox-state` with `/home/vcap/.sandbox-state` wherever they describe the launcher, socket, or runtime state contract. Preserve `/home/vcap/app/sandbox-runtime` as the visible runtime path.

- [ ] **Step 2: Document `.cfignore` behavior**

Add the exact managed patterns and explain that the launcher creates `/home/vcap/app/.cfignore` before starting child processes so agent `cf push` ignores runtime, state, and enrollment material.

- [ ] **Step 3: Update safe smoke checks**

Use:

```bash
cf ssh <sandbox-name> -c 'test -S /home/vcap/.sandbox-state/herdr.sock; test -f /home/vcap/app/.cfignore; /bin/bash -ic "cat /home/vcap/app/.cfignore"'
```

Do not add commands that print credentials or token contents.

- [ ] **Step 4: Run documentation checks**

Run:

```bash
```

Expected: no whitespace errors.

### Task 5: Full verification and review

**Files:**
- No additional files

- [ ] **Step 1: Run the complete Go suite**

Run:

```bash
```

Expected: PASS.

- [ ] **Step 2: Validate all affected shell scripts**

Run:

```bash
bash -n sandbox/start.sh sandbox/start-bash.sh scripts/build-runtime.sh scripts/direct-sandbox.sh scripts/build.sh
```

Expected: PASS with no output.

- [ ] **Step 3: Review state and ignore-file diff**

Run:

```bash
```

Confirm no migration code, secret values, or unrelated deployment files are included. The only default-path change should move state/socket references from `/home/vcap/app/.sandbox-state` to `/home/vcap/.sandbox-state`; the runtime path remains `/home/vcap/app/sandbox-runtime`.

- [ ] **Step 4: Commit the implementation**

```bash
