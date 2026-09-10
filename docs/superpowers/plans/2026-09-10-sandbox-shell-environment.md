# Sandbox Shell Environment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use `/home/vcap` and Bash consistently for CF SSH and Herdr sessions while keeping runtime PATH changes out of Cloud Foundry staging.

**Architecture:** Remove the persisted CF `PATH` override that hides buildpack tools. The runtime launcher exports its own PATH, home, shell, and Herdr socket, and idempotently writes the same values to `/home/vcap/.bashrc` for interactive shells.

**Tech Stack:** Go, Bash, Cloud Foundry CLI, binary buildpack.

---

### Task 1: Remove the Cloud Foundry PATH Override

**Files:**
- Modify: `internal/cf/provider.go:190-198`
- Modify: `internal/cf/provider_test.go:180-198`
- Modify: `scripts/direct-sandbox.sh:76-88`
- Modify: `scripts/direct_sandbox_test.go:12-42`

- [ ] **Step 1: Write failing provider and direct-workflow tests**

Change expected CF command sequences so no command contains:

```text
set-env <app> PATH
```

Keep the exact socket command:

```text
set-env <app> HERDR_SOCKET_PATH /home/vcap/app/.sandbox-state/herdr.sock
```

Add explicit assertions over recorded commands that fail when an argument sequence sets
`PATH`.

- [ ] **Step 2: Verify tests fail for the existing PATH command**

```bash
TMPDIR=/tmp go test ./internal/cf ./scripts -run 'TestConfigureEnrollmentAndStartAppUseSeparateExactCommands|TestDirectSandboxPushesStandaloneAppWithExactContract' -count=1
```

Expected: FAIL because both workflows still call `cf set-env ... PATH ...`.

- [ ] **Step 3: Remove only the PATH commands**

Delete the PATH entry from `Provider.ConfigureEnrollment` and delete this line from the
direct workflow:

```bash
"$CF_BIN" set-env "$APP_NAME" PATH '/home/vcap/app/sandbox-runtime/bin:$PATH'
```

Do not remove `HERDR_SOCKET_PATH` or enrollment variables.

- [ ] **Step 4: Verify focused tests pass**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cf/provider.go internal/cf/provider_test.go scripts/direct-sandbox.sh scripts/direct_sandbox_test.go
git commit -m "fix: keep sandbox PATH out of CF environment"
```

### Task 2: Unify Sandbox Home and Bash Environment

**Files:**
- Modify: `sandbox/start-bash.sh:1-55`
- Modify: `sandbox/start_test.go:20-145`

- [ ] **Step 1: Write failing launcher environment tests**

Require the launcher to export:

```bash
export HOME=/home/vcap
export SHELL=/bin/bash
export PATH="$BIN_DIR:$PATH"
```

Update the idempotent `.bashrc` integration test to use `/home/vcap` semantics and
require exactly one managed block containing:

```bash
export PATH=/home/vcap/app/sandbox-runtime/bin:$PATH
export HERDR_SOCKET_PATH=/home/vcap/app/.sandbox-state/herdr.sock
export SHELL=/bin/bash
```

Assert unrelated `.bashrc` lines remain and no `SANDBOX_HOME` override is accepted.

- [ ] **Step 2: Verify launcher tests fail**

```bash
TMPDIR=/tmp go test ./sandbox -run 'TestLauncherContract|TestLauncherMaintainsIdempotentBashrcRuntimeBlock' -count=1
```

Expected: FAIL because the launcher currently derives HOME from `SANDBOX_HOME` and does
not export/write `SHELL`.

- [ ] **Step 3: Implement the unified runtime environment**

In `sandbox/start-bash.sh`, replace the configurable home with:

```bash
export HOME=/home/vcap
export SHELL=/bin/bash
```

Keep state paths rooted at `SANDBOX_STATE_DIR`. Extend the managed `.bashrc` block with:

```bash
printf 'export SHELL=/bin/bash\n'
```

The launcher continues prepending `BIN_DIR` to its live process PATH and writing the
absolute runtime path to `.bashrc`.

- [ ] **Step 4: Verify launcher tests pass**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sandbox/start-bash.sh sandbox/start_test.go
git commit -m "fix: unify sandbox shell home and Bash environment"
```

### Task 3: Verify Staging and Interactive Shell Behavior

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update runtime documentation**

Document that:

- Cloud Foundry app environment does not override PATH.
- `HOME=/home/vcap` and `SHELL=/bin/bash` are runtime contracts.
- `/home/vcap/.bashrc` exposes `opencode`, `herdr`, and the shared Herdr socket.

- [ ] **Step 2: Run local verification**

```bash
gofmt -w internal/cf/provider.go internal/cf/provider_test.go scripts/direct_sandbox_test.go sandbox/start_test.go
bash -n scripts/direct-sandbox.sh sandbox/start.sh sandbox/start-bash.sh
TMPDIR=/tmp go test ./...
git diff --check
```

Expected: all commands exit successfully.

- [ ] **Step 3: Deploy and test a fresh sandbox**

Deploy the manager, create a new sandbox, and verify:

```bash
cf ssh <sandbox> -c 'printf "HOME=%s\nSHELL=%s\nPATH=%s\nHERDR_SOCKET_PATH=%s\n" "$HOME" "$SHELL" "$PATH" "$HERDR_SOCKET_PATH"; /home/vcap/app/sandbox-runtime/bin/opencode --version; /home/vcap/app/sandbox-runtime/bin/herdr --version'
cf ssh <sandbox> -c '/bin/bash -ic "command -v opencode; command -v herdr"'
```

Expected:

```text
HOME=/home/vcap
SHELL=/bin/bash
/home/vcap/app/sandbox-runtime/bin/opencode
/home/vcap/app/sandbox-runtime/bin/herdr
```

The first command intentionally uses absolute paths because non-interactive `cf ssh -c`
does not source `/home/vcap/.bashrc`. The second command explicitly starts an interactive Bash
shell; interactive `cf ssh` shells source `/home/vcap/.bashrc` and therefore resolve both
commands by name.

Confirm staging no longer reports `/usr/bin/env: bash: No such file or directory`.

- [ ] **Step 4: Commit documentation**

```bash
git add README.md
git commit -m "docs: describe sandbox shell environment"
```
