# Supervise Herdr In Manager Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans (inline execution selected). Steps use checkbox syntax for tracking.

**Goal:** Bundle Herdr into the manager runtime and supervise it before Collie so manager health becomes ready only when both processes are healthy.

**Architecture:** Generalize the existing process supervisor with configurable command, environment, and readiness probe while preserving Collie defaults. Add a Unix-socket Herdr supervisor using the shared manager config/state/socket paths; manager starts Herdr, waits for socket readiness, starts Collie, and stops Herdr after Collie/API during shutdown. Extend runtime and Docker artifact contracts to copy/relocate Herdr into `manager-runtime/bin/herdr`.

**Tech Stack:** Go, Unix process groups, Unix sockets, Bash, Dockerfile contract tests, Go race/vet tests, frontend and shell checks.

---

### Task 1: Add failing Herdr supervisor tests

**Files:**
- Modify: `internal/supervisor/collie_test.go`

- [ ] Add tests for a configurable `herdr server` command, shared Herdr environment, Unix-socket readiness, start-before-Collie ordering, unexpected exit propagation, idempotent stop, and restart cleanup using fake process/socket fixtures.
- [ ] Run `go test ./internal/supervisor`; confirm the new API/tests fail before implementation.

### Task 2: Generalize supervisor lifecycle

**Files:**
- Modify: `internal/supervisor/collie.go`

- [ ] Introduce the minimal configurable process name/args/environment/probe behavior needed by both Herdr and Collie without changing existing Collie defaults.
- [ ] Implement a filesystem Unix-socket readiness probe that checks socket type/existence and honors context cancellation.
- [ ] Preserve process-generation cleanup, stop/restart serialization, and unsolicited child exit signaling.
- [ ] Run `go test ./internal/supervisor -race`; confirm all supervisor tests pass.

### Task 3: Add manager configuration and lifecycle tests

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/manager/main.go`
- Modify: `cmd/manager/main_test.go`

- [ ] Add `HerdrExecutable` with default `MANAGER_RUNTIME_DIR/bin/herdr`, environment override `MANAGER_HERDR_EXECUTABLE`, and path canonicalization.
- [ ] Add manager startup tests proving Herdr starts and becomes socket-ready before Collie, and shutdown tests proving reverse order and no process leaks.
- [ ] Add health handler tests proving a healthy Herdr plus Collie does not return 503 while an unhealthy Collie still does.
- [ ] Run focused config/manager tests and verify they fail before implementation.

### Task 4: Wire Herdr into manager startup

**Files:**
- Modify: `cmd/manager/main.go`

- [ ] Construct a Herdr supervisor with `herdr server`, shared config/state/socket environment, and Unix-socket readiness.
- [ ] Start and await Herdr before constructing/starting the lead Collie readiness path; combine both supervisor error channels so unexpected exits fail manager.
- [ ] Stop HTTP/API/reconciler/Collie first and Herdr last, joining shutdown errors and avoiding duplicate stop calls.
- [ ] Run focused manager tests, then `go test ./cmd/manager ./internal/config ./internal/httpapi`.

### Task 5: Extend runtime artifact contracts

**Files:**
- Modify: `scripts/build-runtime.sh`
- Modify: `docker/cflinuxfs5-builder/Dockerfile`
- Modify: `scripts/build_runtime_test.go`
- Modify: `scripts/package_test.go`

- [ ] Copy or relocate the downloaded Herdr binary into `manager-runtime/bin/herdr` using the manager CF layout and scan/smoke it with the existing artifact rules.
- [ ] Require `manager-runtime/bin/herdr` in Docker output validation and fixture artifact assertions.
- [ ] Run the script/package contract tests and local cflinux builder if Docker is available; do not deploy.

### Task 6: Full verification and commit

**Files:**
- Modify only files from Tasks 1-5.

- [ ] Run `gofmt`, root `go test -race ./...`, `go vet ./...`, frontend tests/build, shell tests/checks, and feasible local cflinux builder verification.
- [ ] Inspect `git status`, `git diff`, and staged file list; exclude all pre-existing untracked/generated files.
- [ ] Commit exactly `feat: supervise Herdr in manager runtime`.
