# Sandbox Cloud Foundry CLI

## Purpose

Make the Cloud Foundry CLI available to agents running inside every new
sandbox, with an authenticated and targeted CF session available from
interactive shells. Sandboxes will use the manager's existing CF credentials
and target context so an agent can run `cf push` without additional setup.

## Chosen Approach

Extend the existing sandbox runtime and enrollment path:

- Package the verified CF CLI artifact into `sandbox-runtime/bin/cf`.
- Keep the existing launcher `PATH` export so `cf` resolves by command name.
- Extend manager-to-sandbox enrollment provisioning with `CF_API`,
  `CF_USERNAME`, `CF_PASSWORD`, `CF_ORG`, and `CF_SPACE`.
- Extend the managed `.bashrc` block with idempotent CF session commands.

This applies to manager-created sandboxes without adding a second bootstrap
asset or a separate credential-delivery path. Direct sandboxes remain a local
debug workflow and do not receive manager credentials unless explicitly
configured by their caller.

## Runtime Packaging

The existing pinned CF CLI release and checksum remain the source artifact.
Runtime assembly will install the validated executable as:

```text
dist/sandbox/runtime/bin/cf
```

The sandbox runtime validation and generated artifact checks will require this
file to be executable. The existing runtime `PATH` export makes `cf` available
to `cf ssh` shells, Herdr terminal sessions, and child processes launched by
the agent.

## Credential Provisioning

During manager enrollment, the CF provider will set these app environment
variables on the sandbox:

```text
CF_API
CF_USERNAME
CF_PASSWORD
CF_ORG
CF_SPACE
```

The values come from the manager's already validated configuration. The
provider must pass them to `cf set-env` without printing values in operation
logs, test output, or error messages. Existing enrollment variables and
`HERDR_SOCKET_PATH` remain unchanged.

Credentials are intentionally inherited by agent child processes because the
agent must be able to invoke `cf push`. They are not written to tracked files,
runtime artifacts, or documentation examples containing real values.

## Interactive Shell Setup

The managed `.bashrc` block remains idempotent and preserves unrelated user
content. It will export the CF variables and run:

```bash
"$BIN_DIR/cf" api "$CF_API" --skip-ssl-validation
"$BIN_DIR/cf" auth "$CF_USERNAME" "$CF_PASSWORD"
"$BIN_DIR/cf" target -o "$CF_ORG" -s "$CF_SPACE"
```

The commands run only when all five CF variables are present, allowing direct
standalone sandboxes to continue without CF credentials. The launcher must not
echo the password or use shell tracing. A failed CF command reports an error
but does not prevent Herdr, bootstrap, or Collie from starting; the process
environment remains available for explicit agent-side retries.

The command block is replaced on repeated starts rather than duplicated. CF
CLI configuration uses the sandbox user's normal CF home/config location and
is not copied outside the sandbox.

## Testing

Automated coverage will verify:

- CF CLI packaging into `sandbox/runtime/bin/cf` and executable validation.
- Enrollment emits the five CF `set-env` operations without exposing values.
- Launcher exports CF variables and includes the ordered `cf api`, `cf auth`,
  and `cf target` commands in the managed block.
- Repeated launcher starts retain one CF setup block and preserve unrelated
  `.bashrc` content.
- Standalone launch without CF variables still starts normally.

The live smoke check for a newly created sandbox will verify `cf version`,
`cf target`, and a safe API/session status command without printing the
username or password.

## Failure Handling

Builds fail closed if the pinned CF artifact cannot be downloaded, verified,
or installed. Runtime validation fails if `bin/cf` is absent or not
executable.

Enrollment fails if any required CF credential or target value is unavailable
from the manager configuration. The provider does not log secret values.

Interactive CF setup is best effort: failures remain visible and do not take
down the sandbox's Herdr or Collie processes. This preserves terminal access
for diagnosis while allowing an agent or operator to retry authentication.

## Non-Goals

- Creating a separate CF service account.
- Exposing manager API credentials to sandboxes.
- Adding CF credentials to direct sandbox defaults.
- Persisting CF credentials in tracked files or packaged artifacts.
- Adding a new public route for CF CLI traffic.
