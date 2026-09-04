# Unowned CF App Mutations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refuse staging and deletion mutations unless the manager can prove the CF app name is safe or the persisted GUID proves ownership.

**Architecture:** Add one provider-level name absence observation backed by `cf app NAME --guid`. Stage invokes it immediately before push; deletion with no persisted GUID invokes it only to distinguish safe absence from unknown ownership, never adopting the observed GUID.

**Tech Stack:** Go 1.25, CF CLI runner fakes, `net/http` integration tests.

---

### Task 1: Stage Preflight

**Files:**
- Modify: `internal/cf/provider_test.go`
- Modify: `internal/cf/provider.go`

- [ ] Add tests proving an existing GUID returns a typed `already exists` conflict without push and a CF not-found result permits push.
- [ ] Run the focused tests and verify they fail because Stage does not perform the lookup.
- [ ] Implement `EnsureAppAbsent` and invoke it immediately before `cf push`.
- [ ] Run the focused provider tests and verify they pass.

### Task 2: Unknown Ownership Deletion

**Files:**
- Modify: `internal/reconcile/reconciler_test.go`
- Modify: `internal/reconcile/reconciler.go`
- Modify: `internal/cf/provider.go`

- [ ] Add tests proving empty `AppGUID` never discovers, unmaps, or deletes an occupied app and retains an explicit `ownership unknown` failed record.
- [ ] Add a test proving an absent app allows GUID-independent member, policy, route, runtime, and record cleanup.
- [ ] Run focused reconciler tests and verify the current GUID adoption behavior fails them.
- [ ] Replace GUID discovery with absence observation and support route cleanup without app unmapping when no GUID exists.
- [ ] Run focused reconciler tests and verify they pass.

### Task 3: HTTP Lifecycle Integration

**Files:**
- Modify: `internal/httpapi/server_test.go`

- [ ] Add an integration-style create/reconcile/delete test using the real reconciler/provider with a fake CF runner, proving an existing CF app blocks push and survives later deletion.
- [ ] Run the focused HTTP test and verify it fails before implementation completion.
- [ ] Make only the fixture/interface adjustments required for the test to pass.
- [ ] Run the focused HTTP test and verify it passes.

### Task 4: Documentation And Verification

**Files:**
- Modify: `README.md`

- [ ] Document the unavoidable lookup/push race and unknown-ownership retained-record failure in the POC friction log.
- [ ] Run `gofmt` on changed Go files.
- [ ] Run CF, reconciler, HTTP, and root race tests 20 times, then `go vet ./...`.
- [ ] Inspect the diff and commit only root repository changes as `fix: refuse unowned CF app mutations`.
