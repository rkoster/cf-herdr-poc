# CF Peer Wiring and Safe Deletion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire Cloud Foundry Pack transport end-to-end and prevent name reuse from deleting or unmapping replacement applications.

**Architecture:** The sandbox launcher derives its listener settings from Cloud Foundry's runtime `PORT`; the manager runtime explicitly selects `cf-identity` while retaining loopback binding. Destructive provider operations preflight the current app GUID immediately before name-addressed commands, while the reconciler retains the originally persisted ownership GUID and fails closed on mismatch. This narrows the separate-CLI-call race under the documented same-operator trust boundary; it does not make deletion atomic.

**Tech Stack:** Go 1.25, Bash, CF CLI, Bun/TypeScript Collie runtime

---

### Task 1: Runtime transport contracts

**Files:**
- Modify: `sandbox/start_test.go`
- Modify: `sandbox/start.sh`
- Modify: `internal/collie/environment_test.go`
- Modify: `internal/collie/environment.go`
- Modify: `internal/pack/manager.go`
- Modify: `internal/pack/manager_test.go`

- [ ] Add failing tests proving the sandbox exports `COLLIE_PORT=$PORT`, wildcard host, non-loopback escape hatch, and `cf-identity` before bootstrap and Collie start.
- [ ] Run focused sandbox tests and confirm failure.
- [ ] Add failing manager environment tests proving loopback address/port and `COLLIE_PACK_TRANSPORT=cf-identity` override ambient values.
- [ ] Run focused Collie/Pack tests and confirm failure.
- [ ] Add `Runtime.PackTransport`, make it a managed environment value, pass `cf-identity` from the manager, and export the four sandbox variables at launcher startup.
- [ ] Run focused tests and confirm success.

### Task 2: Provider identity preflight

**Files:**
- Modify: `internal/cf/provider_test.go`
- Modify: `internal/cf/provider.go`

- [ ] Add failing tests for matching, absent, and replaced app deletion; assert replacement is never deleted and errors classify as identity mismatch.
- [ ] Add failing route tests proving replacement and absent apps are not passed to `cf unmap-route`, while manager-owned policy and route cleanup continues.
- [ ] Run `go test ./internal/cf` and confirm failure.
- [ ] Change `DeleteApp` to require an expected GUID, resolve the current GUID first, treat absence as success, and return typed `identity_mismatch` on replacement.
- [ ] Require `RouteRequest.AppGUID`; remove policy first, preflight app identity before optional unmap, then delete the stable route.
- [ ] Run `go test ./internal/cf` and confirm success.

### Task 3: Reconciler ownership preservation

**Files:**
- Modify: `internal/reconcile/reconciler_test.go`
- Modify: `internal/reconcile/reconciler.go`

- [ ] Add failing tests proving persisted GUIDs are passed to deletion, replacements are not adopted, replacement apps are neither unmapped nor deleted, and discovery-owned GUIDs are persisted before cleanup.
- [ ] Run `go test ./internal/reconcile` and confirm failure.
- [ ] Update the provider interface and deletion effects to carry the expected GUID.
- [ ] Preflight persisted identities before destructive cleanup, fail with identity mismatch without replacing the stored GUID, and retain GUID-independent cleanup behavior.
- [ ] Run `go test ./internal/reconcile` and confirm success.

### Task 4: Documentation and verification

**Files:**
- Modify: `README.md`
- Modify: `docs/spikes/cf-pack-identity.md`

- [ ] Document the peer/manager transport split, plain HTTP behind Gorouter, runtime `PORT`, disabled peer browser behavior, deletion preflight limitation, and same-operator trust boundary.
- [ ] Run formatting and all root tests with race detection.
- [ ] Run `go vet ./...`, sandbox contract tests, and the existing frontend checks without downloading or contacting Cloud Foundry.
- [ ] Review the diff for ownership safety and accidental nested-repository changes.
- [ ] Commit intended root files as `fix: wire CF peer transport and safe deletion`.
