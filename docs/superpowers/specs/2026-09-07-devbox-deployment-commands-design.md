# Devbox Deployment Commands Design

## Goal

Provide two Devbox commands for the local Cloud Foundry proof of concept:

- `devbox run setup` deploys the Cloud Foundry foundation changes needed for identity-aware routing.
- `devbox run deploy` deploys and configures the Herdr manager application.

The commands are operator-facing setup helpers, not a general deployment framework.

## `setup` command

The command is defined directly in `devbox.json` and runs as a fail-fast shell command. It will:

1. Source the local `bosh.env` file so the BOSH CLI receives the configured director connection and credentials.
2. Deploy deployment `cf` from local `cf.yml`.
3. Apply local `ops-enable-mtls-app-routing.yml` with BOSH `-o`.
4. Omit `--vars-store`; the BOSH director's configured CredHub stores generated variables and credentials.
5. Register the identity-aware shared domain with `cf create-shared-domain apps.identity --enforce-route-policies`.

The command relies on the operator's existing authenticated and targeted CF CLI session. It must not perform CF login or print credentials. The shared-domain step should be safe to rerun when the domain already exists, but must not hide an existing domain that lacks route-policy enforcement, because that property is immutable.

## `deploy` command

The command is defined directly in `devbox.json` and contains the Herdr manager deployment logic currently implemented by `scripts/deploy.sh`. It will:

1. Validate the required Herdr environment variables.
2. Validate that `MANAGER_PACK_HOST` is a direct child hostname of `CF_IDENTITY_DOMAIN` and derive `MANAGER_ROUTE_HOST`.
3. Push the manager app from `manifest.yml` without routes and without starting it.
4. Resolve and export the manager app GUID for its runtime environment.
5. Set the manager identity-routing and operational environment variables.
6. Create and map the public manager route.
7. Create and map the manager identity route.
8. Start the manager app.

It will use the existing authenticated and targeted CF CLI session and will not run BOSH commands or perform foundation setup.

## Configuration and safety

All command logic belongs in `devbox.json`; no new wrapper script is introduced. Secret values remain in `bosh.env` or the operator's environment and are passed to subprocesses without being echoed. Existing `scripts/deploy.sh` remains available but is not called by the Devbox command, avoiding a second source of deployment sequencing.

The commands use `set -euo pipefail` and required-variable checks. A failed BOSH deployment prevents shared-domain registration; a failed setup does not trigger application deployment; a failed application deployment does not roll back foundation changes.

## Verification

Add or update tests/checks to verify:

- `devbox.json` remains valid JSON.
- The setup command contains the expected BOSH manifest, ops-file, deployment name, no `--vars-store`, and shared-domain registration.
- The deploy command contains the required validations and the same CF CLI sequencing as the current manager deployment script.
- Shell syntax for extracted command bodies is valid where practical.

The documentation should show the prerequisite of an authenticated, targeted CF CLI session and the two command invocations.
