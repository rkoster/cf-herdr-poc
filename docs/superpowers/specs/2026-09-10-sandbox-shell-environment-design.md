# Sandbox Shell Environment

## Purpose

Provide one consistent home, shell, runtime PATH, and Herdr socket contract for Cloud
Foundry SSH sessions, Herdr sessions, Collie, and OpenCode without modifying the Cloud
Foundry staging environment.

## Chosen Approach

The sandbox launcher owns runtime shell configuration. Cloud Foundry app environment
configuration must not set `PATH`, because `cf set-env` stores `$PATH` literally rather
than expanding it. That malformed value removes `/bin` and `/usr/bin` during staging and
causes the binary buildpack release phase to fail when `/usr/bin/env` cannot locate Bash.

All sandbox processes and interactive users use:

```text
HOME=/home/vcap
SHELL=/bin/bash
```

Runtime state remains separate under `/home/vcap/app/.sandbox-state`.

## Launcher Environment

The Bash launcher exports its runtime binary directory before starting Herdr, bootstrap,
or Collie:

```text
PATH=/home/vcap/app/sandbox-runtime/bin:<inherited PATH>
HERDR_SOCKET_PATH=/home/vcap/app/.sandbox-state/herdr.sock
SHELL=/bin/bash
HOME=/home/vcap
```

Using the inherited process PATH here is safe because this expansion occurs inside the
running application container, not in a persisted Cloud Foundry environment variable.
Herdr and Collie inherit these values, so Herdr-created sessions use Bash and resolve
`opencode` and `herdr` by command name.

## Interactive Shell Configuration

The launcher creates or replaces one clearly marked managed block in
`/home/vcap/.bashrc`. The block exports the sandbox runtime PATH, Herdr socket path, and
`SHELL=/bin/bash`. Repeated launches replace the block without duplicating it and
preserve unrelated user configuration.

Because `cf ssh` reports `HOME=/home/vcap`, the same `.bashrc` configures interactive CF
SSH sessions. There is no second sandbox-specific home or `.bashrc`.

## Cloud Foundry Environment

Sandbox provisioning removes the `cf set-env PATH ...` command from both manager-created
and direct workflows. It continues setting `HERDR_SOCKET_PATH` so non-interactive
`cf ssh -c` commands and processes that do not source `.bashrc` attach to the running
Herdr server. No CF-level `SHELL` override is required because the launcher exports it
for Herdr and `.bashrc` exports it for interactive shells.

## Staging-Safe Entrypoint

The packaged `sandbox-runtime/start.sh` remains a POSIX `/bin/sh` wrapper that invokes
`/bin/bash` by absolute path at application runtime. The Bash implementation remains in
`sandbox-runtime/start-bash.sh`. Build and runtime validation require both executable
files.

## Verification

Automated tests verify:

- Provisioning never emits `cf set-env ... PATH ...`.
- The launcher exports `HOME=/home/vcap` and `SHELL=/bin/bash`.
- The managed `/home/vcap/.bashrc` block contains PATH, socket, and shell exports.
- Repeated launches produce exactly one managed block and preserve unrelated content.
- Herdr and Collie inherit the runtime PATH, socket, home, and Bash shell.
- Both launcher files and the OpenCode executable are required runtime artifacts.
- The full Go suite and shell syntax checks pass.

Live verification creates a fresh sandbox and confirms the non-interactive and interactive
shell contracts:

```text
cf ssh <sandbox> -c 'printf "%s\n" "$HOME" "$SHELL" "$HERDR_SOCKET_PATH"; /home/vcap/app/sandbox-runtime/bin/opencode --version; /home/vcap/app/sandbox-runtime/bin/herdr --version'
cf ssh <sandbox> -c '/bin/bash -ic "command -v opencode; command -v herdr"'
```

The sandbox must stage successfully, report `/home/vcap` and `/bin/bash`, run both commands from
the absolute runtime paths, and attach `herdr` to the configured server socket. Non-interactive
`cf ssh -c` does not source `/home/vcap/.bashrc`; the explicit interactive Bash command verifies
that an interactive shell sources it and resolves both commands by name.

## Non-Goals

- Changing Cloud Foundry's default SSH daemon configuration.
- Persisting sandbox state outside `/home/vcap/app/.sandbox-state`.
- Setting PATH through Cloud Foundry environment variables.
