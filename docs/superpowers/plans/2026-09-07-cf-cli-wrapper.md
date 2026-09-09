# CF CLI Wrapper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Package the relocated CF CLI as `manager-runtime/bin/cf`, a loader wrapper around `cf.real`, without changing direct ELF relocation for Bun, Herdr, or Collie.

**Architecture:** Extend the relocation helper with an explicit wrapper mode. It relocates the source into `cf.real`, leaves private libraries in `.cf-libs`, and writes a small argv-preserving shell wrapper that `exec`s the private loader with `--library-path` and the payload. The manager build invokes this mode only for CF CLI; all other runtimes retain direct ELF mode.

**Tech Stack:** Bash, Go tests, ELF tools (`patchelf`, `readelf`, `ldd`), CF CLI, Markdown.

---

### Task 1: Add red tests for wrapper relocation

**Files:**
- Modify: `scripts/relocate_nix_runtime_test.go`

- [ ] Add tests asserting wrapper execution preserves arguments and exit status, wrapper/payload/private libraries exist, no Nix path appears in wrapper/payload metadata, and direct mode still produces an ELF.
- [ ] Run the focused tests and verify the new wrapper tests fail because the helper has no wrapper mode.

### Task 2: Implement explicit wrapper mode

**Files:**
- Modify: `scripts/relocate-nix-runtime.sh`

- [ ] Parse an explicit `--wrapper` option without changing the existing two-argument direct mode.
- [ ] In wrapper mode relocate the source to `cf.real`, use `.cf-libs`, and generate an executable wrapper using absolute paths derived from its installed directory.
- [ ] Ensure the wrapper uses `exec`, forwards `"$@"`, and contains no source/Nix path.
- [ ] Run focused relocation tests to green.

### Task 3: Wire build and package tests

**Files:**
- Modify: `scripts/build-runtime.sh`
- Modify: `scripts/build_runtime_test.go`
- Modify: `scripts/package_test.go`

- [ ] Invoke wrapper mode only for manager CF CLI and smoke-test `manager-runtime/bin/cf` directly with `cf version`.
- [ ] Assert the fake package artifact has wrapper, `cf.real`, and `.cf-libs`, while Bun/Collie remain direct ELF artifacts.
- [ ] Run focused build/package tests to green.

### Task 4: Verify provider wiring and document exception

**Files:**
- Modify: `internal/cf/provider_test.go`
- Modify: `README.md`
- Modify: `docs/spikes/cf-buildpack-runtime.md`

- [ ] Add/adjust provider coverage to require the configured manager wrapper path for CF operations.
- [ ] Document that only CF CLI uses a wrapper because it does not require `process.execPath` or self-spawn identity; Bun, Herdr, and Collie remain direct ELF.
- [ ] Run race, vet, frontend, shell syntax, and focused artifact tests; do not run live deploy.

### Task 5: Review and commit

- [ ] Inspect diff and status, leaving unrelated/untracked nested content untouched.
- [ ] Commit intended changes as `fix: wrap relocated CF CLI loader`.
