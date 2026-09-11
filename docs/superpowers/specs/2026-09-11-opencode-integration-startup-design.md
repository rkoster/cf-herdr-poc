# OpenCode Integration Startup

## Purpose

Ensure every sandbox startup installs the Herdr OpenCode integration before the
sandbox becomes usable. OpenCode's integration installer requires
`$HOME/.config/opencode` to be a directory; the launcher must create that
directory rather than relying on a user to prepare it manually.

## Chosen Approach

Keep the setup in the existing `sandbox/start-bash.sh` launcher. After the
launcher establishes `/home/vcap`, the XDG directories, and the managed
`.bashrc` block, it will create `$HOME/.config/opencode` and run:

```sh
"$BIN_DIR/herdr" integration install opencode
```

The command runs on every startup and applies to both direct and manager-created
sandboxes because both use the same packaged launcher. No separate helper or
provisioning-specific implementation is needed.

## Startup Ordering

The OpenCode config directory and integration installation happen before Herdr,
sandbox bootstrap, or Collie are started. The existing `set -euo pipefail`
contract makes a failed directory creation or integration installation fail
startup instead of leaving a partially configured sandbox running.

The launcher does not remove or replace an existing path. A regular file at
`$HOME/.config/opencode` causes the normal `mkdir -p` failure and is reported by
the shell; startup must not delete user data.

## Testing

Launcher contract tests will verify that:

- the OpenCode config directory is created;
- `herdr integration install opencode` is invoked;
- installation occurs after shell-directory setup and before Herdr starts;
- the command uses the packaged Herdr binary and inherits the sandbox `HOME`.

Existing launcher tests continue to cover signal handling, bootstrap ordering,
standalone startup, and idempotent `.bashrc` configuration.

## Documentation

The OpenCode sandbox packaging documentation will state that startup ensures the
`$HOME/.config/opencode` directory and installs the Herdr OpenCode integration on
each launch. The runtime artifact layout, Cloud Foundry provisioning, and route
contracts do not change.

## Non-Goals

- Replacing a conflicting regular file at `$HOME/.config/opencode`.
- Installing OpenCode itself at runtime.
- Adding a new runtime helper asset.
- Changing Herdr, Collie, bootstrap, or Cloud Foundry routing behavior.
