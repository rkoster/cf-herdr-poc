# OpenCode Sandbox Packaging

## Purpose

Package OpenCode into every sandbox as an interactive CLI usable from both `cf ssh`
and Collie's Herdr terminal sessions. The sandbox must also expose the running Herdr
server through the same command and Unix socket contract in both contexts.

## Chosen Approach

Extend the existing cflinuxfs5 Docker artifact pipeline. OpenCode is packaged from a
pinned Linux release artifact recorded in `docker/cflinuxfs5-builder/artifacts.env`.
The manifest contains the release version, architecture-specific GitHub release URLs,
and SHA-256 checksums. The Docker build downloads and verifies the exact manifest
artifact; it does not resolve or download a floating `latest` asset during a build.

Refreshing the manifest to a newer OpenCode GitHub release is the upgrade mechanism.
This preserves the current fail-closed and reproducible artifact contract while still
allowing the packaged version to track the latest release when intentionally updated.

## Runtime Artifacts

The verified executable is copied into the sandbox runtime at:

```text
dist/sandbox/runtime/bin/opencode
```

The manager-created sandbox runtime and direct sandbox runtime use the same artifact
layout. Runtime assembly, output validation, and runtime validation require
`bin/opencode` to be an executable regular file alongside Bun, Herdr, Collie, and the
sandbox bootstrap binary.

No OpenCode service, route, or background process is added. OpenCode is started only
when a user invokes it interactively.

## Environment Contract

`sandbox/runtime/start.sh` establishes the shared process environment before starting
Herdr or Collie:

```text
PATH=/home/vcap/app/sandbox-runtime/bin:$PATH
HERDR_SOCKET_PATH=/home/vcap/app/.sandbox-state/herdr.sock
```

The runtime directory is prepended so `opencode`, `herdr`, `bun`, and `collie` resolve
by command name. Herdr starts on this exact socket, allowing an interactive `herdr`
command to attach to the already-running server instead of creating an unrelated
session.

The same values are configured as Cloud Foundry app environment variables for sandbox
apps. This makes the contract available to `cf ssh` shells as well as to processes
inherited by Collie terminal sessions. The startup script remains authoritative for
defaults so the runtime is also correct when launched without the expected app
environment.

## Interactive Shell Persistence

On startup, `start.sh` creates or updates a clearly marked managed block in the
sandbox user's `~/.bashrc` containing exports for the runtime `PATH` and
`HERDR_SOCKET_PATH`. The update is idempotent: repeated starts replace the managed
block rather than appending duplicates, and unrelated `.bashrc` content is preserved.
The block uses absolute runtime and socket paths and does not depend on prior shell
state.

## Components To Change

- `docker/cflinuxfs5-builder/artifacts.env`: pinned OpenCode release metadata.
- `scripts/select-cflinuxfs5-artifacts.sh`: architecture-specific OpenCode metadata
  selection and fail-closed validation.
- `docker/cflinuxfs5-builder/Dockerfile`: download, checksum verification, and
  installation of the OpenCode artifact.
- `scripts/build-runtime.sh` and build output validation: copy and require OpenCode
  in sandbox runtime artifacts.
- `sandbox/runtime/start.sh`: export the shared environment and maintain the
  idempotent `.bashrc` block.
- Direct and manager sandbox provisioning: set the same `PATH` and
  `HERDR_SOCKET_PATH` app environment variables.
- Artifact, shell, and sandbox contract tests: cover packaging and interactive
  environment behavior.
- Build and deployment documentation: document the pinned OpenCode artifact and
  interactive usage.

## Failure Handling

Builds fail before producing a distribution when OpenCode metadata is absent, the
selected architecture is unsupported, the download fails, or the checksum does not
match. Runtime validation fails when the executable is missing, non-regular, or not
executable.

The `.bashrc` update must fail safely without deleting unrelated user configuration.
If the managed block cannot be updated, startup reports the error rather than silently
claiming that `cf ssh` shell persistence is configured; process environment exports
still provide the runtime contract to the started Herdr and Collie processes.

## Verification

Automated checks cover:

- OpenCode manifest selection for supported architectures.
- Propagation of OpenCode URL and checksum into Docker build arguments.
- Checksum verification and executable installation in the Docker builder.
- Presence and executable mode of `sandbox/runtime/bin/opencode` in build output.
- Runtime validation of the OpenCode asset.
- Idempotent `.bashrc` managed-block replacement while preserving unrelated content.
- Exported `PATH` and `HERDR_SOCKET_PATH` values in `start.sh`.
- Direct and manager sandbox environment configuration.

The live smoke check verifies that `cf ssh` can run `opencode --version`, resolve and
invoke `herdr`, and observe the active `HERDR_SOCKET_PATH`. It also verifies that a
Collie terminal session resolves both commands and that invoking `herdr` attaches to
the existing Herdr server socket.

## Non-Goals

- Running OpenCode as a sandbox service.
- Exposing OpenCode or Herdr through a new public route.
- Resolving GitHub's moving `latest` release URL during every build.
- Adding OpenCode-specific persistence beyond the existing sandbox home and state
  directories.
