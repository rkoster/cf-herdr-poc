# Devbox Deployment Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `devbox run setup` for the CredHub-backed identity-aware CF foundation deployment and `devbox run deploy` for the Herdr manager application deployment.

**Architecture:** Keep both fail-fast shell commands directly in `devbox.json`. `setup` sources `bosh.env`, deploys `cf.yml` with `ops-enable-mtls-app-routing.yml` without `--vars-store`, and registers `apps.identity`; `deploy` validates Herdr variables and mirrors the existing `scripts/deploy.sh` CF CLI sequence without invoking BOSH.

**Tech Stack:** Devbox shell scripts, Bash, BOSH CLI, CF CLI v8, JSON.

---

### Task 1: Add the foundation setup command

**Files:**
- Modify: `devbox.json:15-17`
- Test: `devbox.json` JSON parsing and command text checks

- [ ] **Step 1: Add the `setup` shell script entry**

Add a `setup` entry under `shell.scripts` using a single Bash command string. It must:

```bash
set -euo pipefail
test -f bosh.env
test -f cf.yml
test -f ops-enable-mtls-app-routing.yml
# shellcheck disable=SC1091
source bosh.env
: "${BOSH_ENVIRONMENT:?BOSH_ENVIRONMENT is required by bosh.env}"
bosh -e "$BOSH_ENVIRONMENT" -d cf deploy cf.yml -o ops-enable-mtls-app-routing.yml
cf create-shared-domain apps.identity --enforce-route-policies || {
  test "$(cf curl '/v3/domains?names=apps.identity' | jq -r '.resources[0].router_group.type // empty')" = "shared"
}
```

Use the project’s existing Devbox JSON style. Do not add `--vars-store`, a vars-store path, CF login, or credential output. The rerun branch must only accept an already-existing shared domain; it must not silently accept an incorrectly configured domain.

- [ ] **Step 2: Validate JSON and inspect the command**

Run:

```bash
jq empty devbox.json
jq -r '.shell.scripts.setup' devbox.json
```

Expected: `jq empty` succeeds, and the printed command contains `source bosh.env`, `bosh ... deploy cf.yml -o ops-enable-mtls-app-routing.yml`, no `--vars-store`, and `cf create-shared-domain apps.identity --enforce-route-policies`.

- [ ] **Step 3: Commit the setup command**

```bash
git add devbox.json
git commit -m "feat: add devbox foundation setup command"
```

### Task 2: Add the Herdr application deploy command

**Files:**
- Modify: `devbox.json` setup script section
- Reference: `scripts/deploy.sh:4-30`
- Test: `devbox.json` command text and extracted shell syntax

- [ ] **Step 1: Add the `deploy` shell script entry**

Inline the existing `scripts/deploy.sh` logic into a `deploy` entry under `shell.scripts`. Preserve the exact required variables, hostname validation, and command order:

```bash
set -euo pipefail
: "${MANAGER_APP_NAME:?MANAGER_APP_NAME is required}"
: "${PUBLIC_DOMAIN:?PUBLIC_DOMAIN is required}"
: "${MANAGER_PUBLIC_HOST:?MANAGER_PUBLIC_HOST is required}"
: "${CF_IDENTITY_DOMAIN:?CF_IDENTITY_DOMAIN is required}"
: "${MANAGER_PACK_HOST:?MANAGER_PACK_HOST is required}"
: "${SANDBOX_BUILDPACKS:?SANDBOX_BUILDPACKS is required}"
: "${MANAGER_API_TOKEN:?MANAGER_API_TOKEN is required}"
MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
if [[ -z "$MANAGER_ROUTE_HOST" || "$MANAGER_ROUTE_HOST" == "$MANAGER_PACK_HOST" || "$MANAGER_ROUTE_HOST" == *.* || "$MANAGER_ROUTE_HOST.$CF_IDENTITY_DOMAIN" != "$MANAGER_PACK_HOST" || ! "$MANAGER_ROUTE_HOST" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
  printf 'error: MANAGER_PACK_HOST must be a direct child FQDN of CF_IDENTITY_DOMAIN\n' >&2
  exit 1
fi
cf push "$MANAGER_APP_NAME" -f manifest.yml --no-route --no-start
MANAGER_APP_GUID="$(cf app "$MANAGER_APP_NAME" --guid)"
cf set-env "$MANAGER_APP_NAME" CF_IDENTITY_DOMAIN "$CF_IDENTITY_DOMAIN"
cf set-env "$MANAGER_APP_NAME" SANDBOX_BUILDPACKS "$SANDBOX_BUILDPACKS"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_NAME "$MANAGER_APP_NAME"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_GUID "$MANAGER_APP_GUID"
cf set-env "$MANAGER_APP_NAME" MANAGER_PACK_HOST "$MANAGER_PACK_HOST"
cf set-env "$MANAGER_APP_NAME" MANAGER_API_TOKEN "$MANAGER_API_TOKEN"
cf create-route "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf map-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf create-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf start "$MANAGER_APP_NAME"
```

Do not source `bosh.env`, run BOSH, perform `cf login`, or call `scripts/deploy.sh` in this command.

- [ ] **Step 2: Validate JSON and shell syntax**

Run:

```bash
jq empty devbox.json
jq -r '.shell.scripts.deploy' devbox.json > /tmp/cf-herdr-devbox-deploy.sh
bash -n /tmp/cf-herdr-devbox-deploy.sh
```

Expected: all commands succeed. Inspect the extracted command to confirm it contains no BOSH deployment and preserves the current application deployment sequence.

- [ ] **Step 3: Commit the application command**

```bash
git add devbox.json
git commit -m "feat: add devbox herdr deploy command"
```

### Task 3: Update operator documentation and verify the final change

**Files:**
- Modify: `README.md:55-83`
- Test: `devbox.json`, documentation diff, shell syntax

- [ ] **Step 1: Document prerequisites and command separation**

Update the deployment section to state that the operator must first authenticate and target the CF CLI, then show:

```bash
devbox run setup
devbox run deploy
```

Explain that `setup` applies the BOSH identity-routing changes and registers `apps.identity`, while `deploy` only deploys the Herdr manager. State that `setup` uses CredHub configured on the BOSH director and does not create a local vars-store.

- [ ] **Step 2: Run repository checks**

Run:

```bash
jq empty devbox.json
jq -r '.shell.scripts.setup, .shell.scripts.deploy' devbox.json > /tmp/cf-herdr-devbox-commands.sh
bash -n /tmp/cf-herdr-devbox-commands.sh
bash -n scripts/deploy.sh
git diff --check
```

Expected: every command succeeds. Do not run the live deployment commands unless the operator explicitly requests it.

- [ ] **Step 3: Review the final diff and commit documentation**

```bash
git diff -- devbox.json README.md
git status --short
git add devbox.json README.md
git commit -m "docs: document devbox deployment workflow"
```

Confirm that unrelated untracked files such as `bosh.env`, `cf.yml`, and the local Collie checkout are not staged.
