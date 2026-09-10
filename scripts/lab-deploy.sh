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

BUILD_MODE="${BUILD_MODE:-cflinuxfs5}"
MANAGER_APP_NAME="${MANAGER_APP_NAME:-cf-herdr-manager}"
PUBLIC_DOMAIN="${PUBLIC_DOMAIN:-10.246.0.21.sslip.io}"
MANAGER_PUBLIC_HOST="${MANAGER_PUBLIC_HOST:-herdr-manager}"
CF_IDENTITY_DOMAIN="${CF_IDENTITY_DOMAIN:-apps.identity}"
MANAGER_PACK_HOST="${MANAGER_PACK_HOST:-herdr-manager-pack.apps.identity}"
SANDBOX_BUILDPACKS="${SANDBOX_BUILDPACKS:-binary_buildpack,nodejs_buildpack}"
CF_API="${CF_API:-https://api.10.246.0.21.sslip.io}"
CF_SKIP_SSL_VALIDATION="${CF_SKIP_SSL_VALIDATION:-true}"
CF_ORG="${CF_ORG:-poc}"
CF_SPACE="${CF_SPACE:-demo}"
CF_BIN="$(resolve_tool CF_BIN cf)"
if [[ "$BUILD_MODE" == nix-relocation ]]; then
	BUN_RUNTIME_BIN="$(resolve_tool BUN_RUNTIME_BIN bun)"
	HERDR_RUNTIME_BIN="$(resolve_tool HERDR_RUNTIME_BIN herdr)"
	OPENCODE_RUNTIME_BIN="$(resolve_tool OPENCODE_RUNTIME_BIN opencode)"
	export BUN_RUNTIME_BIN HERDR_RUNTIME_BIN OPENCODE_RUNTIME_BIN ALLOW_NIX_RUNTIME_RELOCATION=1 CF_BIN
	for tool in go patchelf readelf ldd nix-store; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			printf 'error: required build tool %s was not found in PATH\n' "$tool" >&2
			exit 1
		fi
	done
elif [[ "$BUILD_MODE" != cflinuxfs5 ]]; then
	printf 'error: BUILD_MODE must be cflinuxfs5 or explicit nix-relocation\n' >&2
	exit 2
fi

printf '==> build distribution\n'
bash scripts/build.sh

: "${MANAGER_APP_NAME:?MANAGER_APP_NAME is required}"
: "${PUBLIC_DOMAIN:?PUBLIC_DOMAIN is required}"
: "${MANAGER_PUBLIC_HOST:?MANAGER_PUBLIC_HOST is required}"
: "${CF_IDENTITY_DOMAIN:?CF_IDENTITY_DOMAIN is required}"
: "${MANAGER_PACK_HOST:?MANAGER_PACK_HOST is required}"
: "${SANDBOX_BUILDPACKS:?SANDBOX_BUILDPACKS is required}"
: "${MANAGER_API_TOKEN:?MANAGER_API_TOKEN is required}"
: "${CF_API:?CF_API is required}"
: "${CF_USERNAME:?CF_USERNAME is required}"
: "${CF_PASSWORD:?CF_PASSWORD is required}"
: "${CF_ORG:?CF_ORG is required}"
: "${CF_SPACE:?CF_SPACE is required}"

MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
MANAGER_PUBLIC_ADDRESS="$MANAGER_PUBLIC_HOST.$PUBLIC_DOMAIN"
if [[ -z "$MANAGER_ROUTE_HOST" || "$MANAGER_ROUTE_HOST" == "$MANAGER_PACK_HOST" || "$MANAGER_ROUTE_HOST" == *.* || "$MANAGER_ROUTE_HOST.$CF_IDENTITY_DOMAIN" != "$MANAGER_PACK_HOST" || ! "$MANAGER_ROUTE_HOST" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
	printf 'error: MANAGER_PACK_HOST must be a direct child FQDN of CF_IDENTITY_DOMAIN\n' >&2
	exit 1
fi

printf '==> push manager\n'
"$CF_BIN" push "$MANAGER_APP_NAME" --no-manifest -p dist -b binary_buildpack -c ./manager --no-route --no-start -u http --endpoint /manager/healthz --redact-env
MANAGER_APP_GUID="$("$CF_BIN" app "$MANAGER_APP_NAME" --guid)"
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_WEB_DIR ./web >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_COLLIE_DIR ./sandbox/runtime/collie >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_RUNTIME_DIR ./manager-runtime >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_BUN_EXECUTABLE ./manager-runtime/bin/bun >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_COLLIE_EXECUTABLE ./manager-runtime/bin/collie >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_CF_EXECUTABLE ./manager-runtime/bin/cf >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_IDENTITY_DOMAIN "$CF_IDENTITY_DOMAIN" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" SANDBOX_BUILDPACKS "$SANDBOX_BUILDPACKS" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_APP_NAME "$MANAGER_APP_NAME" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_APP_GUID "$MANAGER_APP_GUID" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_PACK_HOST "$MANAGER_PACK_HOST" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_API "$CF_API" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_USERNAME "$CF_USERNAME" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_PASSWORD "$CF_PASSWORD" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_ORG "$CF_ORG" >/dev/null 2>&1
"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_SPACE "$CF_SPACE" >/dev/null 2>&1
if [[ -n "${CF_SKIP_SSL_VALIDATION:-}" ]]; then
	"$CF_BIN" set-env "$MANAGER_APP_NAME" CF_SKIP_SSL_VALIDATION "$CF_SKIP_SSL_VALIDATION" >/dev/null 2>&1
fi
"$CF_BIN" set-env "$MANAGER_APP_NAME" COLLIE_PUBLIC_HOSTS "$MANAGER_PUBLIC_ADDRESS" >/dev/null 2>&1
if "$CF_BIN" set-env "$MANAGER_APP_NAME" MANAGER_API_TOKEN "$MANAGER_API_TOKEN" >/dev/null 2>&1; then
	printf 'MANAGER_API_TOKEN configured\n'
else
	printf 'error: failed to set MANAGER_API_TOKEN\n' >&2
	exit 1
fi

printf '==> configure routes\n'
ensure_route() {
	local output status
	if output=$("$CF_BIN" create-route "$1" --hostname "$2" 2>&1); then
		return 0
	fi
	status=$?
	case "$output" in
		*already\ exists*|*already\ exists.*) return 0 ;;
		*) printf '%s\n' "$output" >&2; return "$status" ;;
	esac
}

ensure_route "$PUBLIC_DOMAIN" "$MANAGER_PUBLIC_HOST"
ensure_route "$CF_IDENTITY_DOMAIN" "$MANAGER_ROUTE_HOST"

printf '==> start manager\n'
"$CF_BIN" start "$MANAGER_APP_NAME"
"$CF_BIN" map-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
"$CF_BIN" map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
