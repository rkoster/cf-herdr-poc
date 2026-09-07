#!/usr/bin/env bash
set -euo pipefail

TMPDIR="${DEPLOY_TMPDIR:-/tmp}"
if ! tmp_probe="$(mktemp "$TMPDIR/cf-herdr-deploy-probe.XXXXXX" 2>/dev/null)"; then
	printf 'error: cannot create temporary files in %s; set DEPLOY_TMPDIR to a writable, searchable directory\n' "$TMPDIR" >&2
	exit 1
fi
rm -f "$tmp_probe"
export TMPDIR

require_tool() {
	local name=$1 path
	path="$(command -v "$name" 2>/dev/null || true)"
	if [[ -z "$path" ]]; then
		printf 'error: required tool %s was not found in PATH\n' "$name" >&2
		exit 1
	fi
	printf '%s' "$path"
}

resolve_runtime() {
	local variable=$1 name=$2 path
	path=${!variable:-}
	if [[ -z "$path" ]]; then
		path="$(command -v "$name" 2>/dev/null || true)"
		if [[ -z "$path" ]]; then
			printf 'error: required runtime %s was not found in PATH; install it or set %s\n' "$name" "$variable" >&2
			exit 1
		fi
	elif [[ ! -x "$path" ]]; then
		printf 'error: %s must name an executable: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	printf '%s' "$path"
}

BUN_RUNTIME_BIN="$(resolve_runtime BUN_RUNTIME_BIN bun)"
HERDR_RUNTIME_BIN="$(resolve_runtime HERDR_RUNTIME_BIN herdr)"
export BUN_RUNTIME_BIN HERDR_RUNTIME_BIN ALLOW_NIX_RUNTIME_RELOCATION=1
for tool in cf go patchelf readelf ldd nix-store; do
	require_tool "$tool" >/dev/null
done

printf '==> build distribution\n'
bash scripts/build.sh

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

printf '==> push manager\n'
cf push "$MANAGER_APP_NAME" -f manifest.yml --no-route --no-start
MANAGER_APP_GUID="$(cf app "$MANAGER_APP_NAME" --guid)"
cf set-env "$MANAGER_APP_NAME" CF_IDENTITY_DOMAIN "$CF_IDENTITY_DOMAIN"
cf set-env "$MANAGER_APP_NAME" SANDBOX_BUILDPACKS "$SANDBOX_BUILDPACKS"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_NAME "$MANAGER_APP_NAME"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_GUID "$MANAGER_APP_GUID"
cf set-env "$MANAGER_APP_NAME" MANAGER_PACK_HOST "$MANAGER_PACK_HOST"
if cf set-env "$MANAGER_APP_NAME" MANAGER_API_TOKEN "$MANAGER_API_TOKEN" >/dev/null 2>&1; then
	printf 'MANAGER_API_TOKEN configured\n'
else
	printf 'error: failed to set MANAGER_API_TOKEN\n' >&2
	exit 1
fi

printf '==> configure routes\n'
cf create-route "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf map-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf create-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"

printf '==> start manager\n'
cf start "$MANAGER_APP_NAME"
