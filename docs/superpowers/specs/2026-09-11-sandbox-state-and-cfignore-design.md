# Sandbox State And CF Ignore

## Purpose

Keep sandbox runtime state outside the application directory so an agent can
run `cf push` from the sandbox repository without packaging or colliding with
Herdr, OpenCode, Collie, or bootstrap state. Also ensure the staged sandbox
runtime and enrollment material are excluded from agent application uploads.

## Chosen Approach

Change the launcher default state directory from:

```text
/home/vcap/app/.sandbox-state
```

to:

```text
/home/vcap/.sandbox-state
```

The existing `SANDBOX_STATE_DIR` override remains supported for tests and
explicit deployments. Manager enrollment and direct-sandbox provisioning will
use the new default socket path:

```text
/home/vcap/.sandbox-state/herdr.sock
```

At startup, the launcher creates or replaces a managed `/home/vcap/app/.cfignore`
file containing:

```text
sandbox-runtime/
.sandbox-state/
join-token
```

The file is created in the application root because that is the directory from
which an agent normally runs `cf push`. It protects the visible runtime
overlay, any legacy or manually created in-app state, and the enrollment token.

## Path Contract

The default state path change applies consistently to:

- `SANDBOX_STATE_DIR` launcher defaults;
- `HERDR_SOCKET_PATH` defaults and manager enrollment environment;
- direct-sandbox environment setup;
- launcher tests, provider tests, and smoke-test documentation;
- XDG state and data paths derived from `SANDBOX_STATE_DIR`.

Explicit `HERDR_SOCKET_PATH` and `SANDBOX_STATE_DIR` values continue to win over
defaults. No migration of existing state is attempted: a newly started sandbox
uses the new location, while callers that need old state can set the override
explicitly.

## `.cfignore` Behavior

The launcher writes the managed `.cfignore` before starting Herdr, bootstrap, or
Collie. Repeated starts replace only the managed file contents and preserve the
file mode as a private user file. The launcher does not delete arbitrary files
or modify an agent repository outside `/home/vcap/app`.

The ignore file contains no secrets. The `join-token` pattern prevents the
staged token from entering a CF package even though the token itself remains
under the visible runtime directory for bootstrap consumption.

## Testing

Automated tests will verify:

- the new default state and socket paths;
- manager enrollment and direct-sandbox setup use the new socket path;
- launcher-created `.cfignore` contains exactly the managed runtime, state, and
  token patterns;
- repeated launcher starts keep one correct `.cfignore` and preserve expected
  startup behavior;
- existing explicit state/socket overrides still work.

The live smoke check will run `cf ssh` commands to confirm:

```bash
test -S /home/vcap/.sandbox-state/herdr.sock
test -f /home/vcap/app/.cfignore
```

It will also perform a dry-run or safe package listing, where supported, to
confirm `sandbox-runtime/`, `.sandbox-state/`, and `join-token` are excluded.

## Failure Handling

Failure to create or update `/home/vcap/app/.cfignore` stops launcher startup
before child processes begin, avoiding a false claim that agent uploads are
protected. Existing runtime state and unrelated files are not deleted.

The state directory and socket parent are created with the existing launcher
directory setup and retain the current permissions and symlink protections.

## Non-Goals

- Migrating state from `/home/vcap/app/.sandbox-state` automatically.
- Changing the visible runtime path `/home/vcap/app/sandbox-runtime`.
- Modifying `.cfignore` files inside an agent repository outside the sandbox app
  root.
- Ignoring arbitrary user files beyond runtime, state, and enrollment material.
