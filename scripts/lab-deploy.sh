#!/usr/bin/env bash
set -euo pipefail

TMPDIR="${DEPLOY_TMPDIR:-/tmp}"
if ! tmp_probe="$(mktemp "$TMPDIR/cf-herdr-deploy-probe.XXXXXX" 2>/dev/null)"; then
	printf 'error: cannot create temporary files in %s; set DEPLOY_TMPDIR to a writable, searchable directory\n' "$TMPDIR" >&2
	exit 1
fi
rm -f "$tmp_probe"
export TMPDIR

resolve_tool() {
	local variable=$1 name=$2 path
	path=${!variable:-}
	if [[ -z "$path" ]]; then
		path="$(command -v "$name" 2>/dev/null || true)"
	fi
	if [[ -z "$path" ]]; then
		if [[ -n "${LAB_PROFILE_BIN_DIR:-}" ]]; then
			profile_bin_dir=$LAB_PROFILE_BIN_DIR
		elif [[ -n "${USER:-}" ]]; then
			profile_bin_dir="/etc/profiles/per-user/${USER}/bin"
		else
			profile_bin_dir=
		fi
		if [[ -n "$profile_bin_dir" && -x "$profile_bin_dir/$name" ]]; then
			path="$profile_bin_dir/$name"
		elif [[ -n "${HOME:-}" && -x "$HOME/.nix-profile/bin/$name" ]]; then
			path="$HOME/.nix-profile/bin/$name"
		fi
	fi
	if [[ -z "$path" ]]; then
		printf 'error: required tool %s was not found; install it, expose it in PATH or a Nix user profile, or set %s\n' "$name" "$variable" >&2
		exit 1
	fi
	if [[ ! -f "$path" || ! -x "$path" ]]; then
		printf 'error: %s must name an executable file: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	printf '%s' "$path"
}

BUN_RUNTIME_BIN="$(resolve_tool BUN_RUNTIME_BIN bun)"
HERDR_RUNTIME_BIN="$(resolve_tool HERDR_RUNTIME_BIN herdr)"
CF_BIN="$(resolve_tool CF_BIN cf)"
export BUN_RUNTIME_BIN HERDR_RUNTIME_BIN ALLOW_NIX_RUNTIME_RELOCATION=1
for tool in go patchelf readelf ldd nix-store; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'error: required build tool %s was not found in PATH\n' "$tool" >&2
		exit 1
	fi
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
"$CF_BIN" push "$MANAGER_APP_NAME" -f manifest.yml --no-route --no-start
MANAGER_APP_GUID="$("$CF_BIN" app "$MANAGER_APP_NAME" --guid)"
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_IDENTITY_DOMAIN "$CF_IDENTITY_DOMAIN"
"$CF_BIN" set-env "$MANAGER_APP_NAME" SANDBOX_BUILDPACKS "$SANDBOX_BUILDPACKS"
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_APP_NAME "$MANAGER_APP_NAME"
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_APP_GUID "$MANAGER_APP_GUID"
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_PACK_HOST "$MANAGER_PACK_HOST"
if "$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_API_TOKEN "$MANAGER_API_TOKEN" >/dev/null 2>&1; then
	printf 'MANAGER_API_TOKEN configured\n'
else
	printf 'error: failed to set MANAGER_API_TOKEN\n' >&2
	exit 1
fi

printf '==> configure routes\n'
"$CF_BIN" create-route "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
"$CF_BIN" map-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
"$CF_BIN" create-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
"$CF_BIN" map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"

printf '==> start manager\n'
"$CF_BIN" start "$MANAGER_APP_NAME"
