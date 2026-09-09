# Local Deploy Secrets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load only deployment secrets from a local ignored `.secrets` file while giving `devbox run deploy` defaults for nonsecret lab settings.

**Architecture:** `.envrc` sources `.secrets` after Devbox initializes, and `lab-deploy.sh` supplies defaults only when nonsecret variables are unset. CF is used once to read the existing manager environment; secret values are written directly to `.secrets` without printing them.

**Tech Stack:** Bash, direnv, Devbox, Cloud Foundry CLI.

---

### Task 1: Add local secret loading and deploy defaults

**Files:**
- Create: `.secrets`
- Modify: `.gitignore`
- Modify: `.envrc`
- Modify: `scripts/lab-deploy.sh`

- [ ] Add `.secrets` with restrictive permissions and only `CF_USERNAME`, `CF_PASSWORD`, and `MANAGER_API_TOKEN`.
- [ ] Add `/.secrets` to `.gitignore`.
- [ ] Source `.secrets` from `.envrc` when present, without failing when absent.
- [ ] Source `.secrets` from the Devbox `deploy` command as well, because `devbox run` does not evaluate `.envrc` itself.
- [ ] Default nonsecret deployment values in `scripts/lab-deploy.sh` before validation: `MANAGER_APP_NAME=cf-herdr-manager`, `PUBLIC_DOMAIN=10.246.0.21.sslip.io`, `MANAGER_PUBLIC_HOST=herdr-manager`, `CF_IDENTITY_DOMAIN=apps.identity`, `MANAGER_PACK_HOST=herdr-manager-pack.apps.identity`, `SANDBOX_BUILDPACKS=binary_buildpack,nodejs_buildpack`, `CF_API=https://api.10.246.0.21.sslip.io`, `CF_SKIP_SSL_VALIDATION=true`, `CF_ORG=poc`, and `CF_SPACE=demo`.

### Task 2: Document secret handling

**Files:**
- Modify: `AGENTS.md`

- [ ] Document that `.secrets` is local-only, ignored by Git, sourced by `.envrc`, and may contain only secret values.
- [ ] Reiterate that passwords, manager tokens, certificates, keys, and Pack tokens must never be logged or copied into tracked files.

### Task 3: Deploy and verify

**Files:**
- No source files.

- [ ] Read the existing manager environment with CF CLI and write the three secret values to `.secrets` without exposing them in command output.
- [ ] Verify `.secrets` permissions and that Git ignores it.
- [ ] Run `devbox run deploy` from the configured environment.
- [ ] Verify `cf app cf-herdr-manager` reports the latest upload and `1/1` running.
