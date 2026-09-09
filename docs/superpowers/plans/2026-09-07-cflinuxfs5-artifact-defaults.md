# cflinuxfs5 Artifact Defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make cflinuxfs5 builds consume pinned Bun and Herdr manifest artifacts by default while preserving fail-closed CF metadata validation and explicit non-empty overrides.

**Architecture:** The artifact selector sources the checked-in manifest, chooses architecture-specific values, then replaces them only with non-empty environment values. The build wrapper invokes the selector without manufacturing empty overrides and forwards the selector's uppercase output directly to Docker.

**Tech Stack:** Bash, Go tests, Docker build argument contract.

---

### Task 1: Add regression tests

**Files:**
- Modify: `scripts/package_test.go`

- [ ] Add tests that execute the selector with a clean environment and assert pinned Bun/Herdr output plus actionable missing CF metadata.
- [ ] Add an override test asserting non-empty environment values replace manifest values.
- [ ] Add a source-contract assertion that the build script does not pass empty artifact overrides.
- [ ] Run the focused tests and confirm they fail against the current implementation.

### Task 2: Resolve manifest defaults safely

**Files:**
- Modify: `scripts/select-cflinuxfs5-artifacts.sh`
- Modify: `scripts/build-cflinuxfs5.sh`

- [ ] Source the checked-in manifest and use architecture-specific values as the initial selection.
- [ ] Apply only non-empty explicit environment overrides, retaining empty CF manifest values as validation failures.
- [ ] Invoke the selector from the build script without `${VAR:-}` empty overrides.
- [ ] Keep the selector output as uppercase `NAME=value` pairs for Docker build args.

### Task 3: Verify and commit

- [ ] Run scripts tests, Go race tests, Go vet, shell syntax/tests, and JSON validation.
- [ ] Inspect the final diff and status, ensuring no deployment or nested checkout changes.
- [ ] Commit all intended changes as `fix: load pinned cflinuxfs5 artifact defaults`.
