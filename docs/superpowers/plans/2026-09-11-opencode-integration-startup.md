# OpenCode Integration Startup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every sandbox startup create the OpenCode config directory and install the Herdr OpenCode integration before starting sandbox processes.

**Architecture:** Extend the existing `sandbox/start-bash.sh` launcher. After shell and XDG directories are initialized, it will create `$HOME/.config/opencode` and invoke the packaged Herdr binary's `integration install opencode` command. Existing `set -euo pipefail` behavior makes setup failures stop startup without deleting conflicting user paths.

**Tech Stack:** POSIX shell entrypoint, Bash launcher, Go launcher contract tests, Markdown documentation.

---

### Task 1: Add failing launcher contract coverage

**Files:**
- Modify: `sandbox/start_test.go`, alongside `TestLauncherContract`
- Test: `sandbox/start_test.go`

- [ ] **Step 1: Add static ordering assertions**

Extend `TestLauncherContract` with required launcher fragments for:

```go
`mkdir -p "$HOME/.config/opencode"`,
`"$BIN_DIR/herdr" integration install opencode`,
```

Then assert their order relative to the existing setup and Herdr startup:

```go
configDir := strings.Index(script, `mkdir -p "$HOME/.config/opencode"`)
integration := strings.Index(script, `"$BIN_DIR/herdr" integration install opencode`)
herdr := strings.Index(script, `"$BIN_DIR/herdr" server &`)
if configDir < 0 || integration < 0 || herdr < 0 {
	t.Fatal("launcher is missing OpenCode integration setup")
}
if configDir >= integration || integration >= herdr {
	t.Fatalf("OpenCode integration setup is out of order: config=%d integration=%d herdr=%d", configDir, integration, herdr)
}
```

Ensure the assertions search the executable-line script already used by the test so comments cannot satisfy the contract.

- [ ] **Step 2: Add runtime helper coverage for the inherited home**

Update `prepareLauncher` to include a `herdr` test wrapper that records the integration invocation and verifies `HOME` points at the test home. Keep the existing role wrapper behavior for server startup. The wrapper should write a line such as `integration installed` to `SIGNAL_LOG` when invoked with arguments `integration install opencode`, and exit nonzero for any unexpected arguments.

Add a test that starts the launcher with `SANDBOX_LAUNCHER_HELPER=1`, a temporary `SANDBOX_STATE_DIR`, `PORT=8080`, and a short Herdr socket path, waits for `integration installed` and `bun started`, and then terminates the launcher. Assert that the log contains the integration marker before the Bun marker.

- [ ] **Step 3: Run the focused tests and verify failure**

Run:

```bash
go test ./sandbox -run 'TestLauncher(Contract|InstallsOpenCodeIntegration)' -count=1
```

Expected: FAIL because `sandbox/start-bash.sh` does not yet create the OpenCode config directory or invoke the integration installer.

### Task 2: Implement startup integration setup

**Files:**
- Modify: `sandbox/start-bash.sh`, immediately after `mkdir -p ...` and before `configure_bashrc`

- [ ] **Step 1: Create the config directory and install the integration**

Add the minimal setup:

```bash
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$COLLIE_STATE_DIR" "$HERDR_PLUGIN_CONFIG_DIR" "$(dirname -- "$HERDR_SOCKET_PATH")" "$HOME/.config/opencode"
"$BIN_DIR/herdr" integration install opencode
configure_bashrc
```

Keep the existing `set -euo pipefail`. Do not remove or replace a conflicting file, suppress installer output, or start any child process before the installer succeeds.

- [ ] **Step 2: Run the focused tests and verify success**

Run:

```bash
go test ./sandbox -run 'TestLauncher(Contract|InstallsOpenCodeIntegration)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run all sandbox launcher tests**

Run:

```bash
go test ./sandbox -count=1
```

Expected: PASS, including existing bootstrap, signal, standalone, socket, and `.bashrc` tests.

### Task 3: Document the runtime contract

**Files:**
- Modify: `docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md`, in the startup/environment and verification sections

- [ ] **Step 1: Document startup installation**

Add that `sandbox/start.sh` creates `$HOME/.config/opencode` and runs `herdr integration install opencode` on every launch before Herdr, bootstrap, or Collie starts. State that a conflicting regular file is not removed and causes startup to fail.

- [ ] **Step 2: Document verification**

Add the expected smoke check:

```bash
cf ssh <sandbox-name> -c 'test -d /home/vcap/.config/opencode && test -f /home/vcap/.config/opencode/tui.jsonc'
```

The current Herdr installer contract creates `tui.jsonc`, so the smoke check verifies both the required directory and the TUI integration configuration file.

- [ ] **Step 3: Run documentation consistency checks**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

### Task 4: Verify the complete change

**Files:**
- No additional files

- [ ] **Step 1: Run package-level tests**

Run:

```bash
go test ./sandbox ./internal/runtime ./scripts
```

Expected: PASS.

- [ ] **Step 2: Validate shell syntax**

Run:

```bash
bash -n sandbox/start.sh sandbox/start-bash.sh
```

Expected: PASS with no output.

- [ ] **Step 3: Review the final diff**

Run:

```bash
git diff --check
git diff -- sandbox/start-bash.sh sandbox/start_test.go docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md
```

Confirm the change only adds directory creation, integration installation, focused tests, and documentation. Do not modify unrelated untracked deployment files.

- [ ] **Step 4: Commit the implementation**

```bash
git add sandbox/start-bash.sh sandbox/start_test.go docs/superpowers/specs/2026-09-09-opencode-sandbox-packaging-design.md
git commit -m "feat: install OpenCode integration on sandbox startup"
```
