#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT/dist}"
BUILD_RUNTIME_SCRIPT="${BUILD_RUNTIME_SCRIPT:-$ROOT/scripts/build-runtime.sh}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"

BUILD_MODE="${BUILD_MODE:-cflinuxfs5}"
case "$BUILD_MODE" in
 cflinuxfs5) exec bash "$ROOT/scripts/build-cflinuxfs5.sh" ;;
 nix-relocation) : ;;
 *) printf 'error: BUILD_MODE must be cflinuxfs5 or explicit nix-relocation\n' >&2; exit 2 ;;
esac

if [[ -z "${BUN_RUNTIME_BIN:-}" ]]; then
	printf 'error: BUN_RUNTIME_BIN is required and must name a portable runtime executable\n' >&2
	exit 1
fi
if [[ -z "${HERDR_RUNTIME_BIN:-}" ]]; then
	printf 'error: HERDR_RUNTIME_BIN is required and must name a portable runtime executable\n' >&2
	exit 1
fi

parent="$(dirname -- "$DIST_DIR")"
name="$(basename -- "$DIST_DIR")"
mkdir -p "$parent"
DIST_STAGING="$(mktemp -d "$parent/.${name}.staging.XXXXXX")"
previous="$parent/${name}.previous"
cleanup() {
	rm -rf "$DIST_STAGING"
	if [[ ! -e "$DIST_DIR" && -e "$previous" ]]; then mv "$previous" "$DIST_DIR"; fi
}
trap cleanup EXIT

(cd "$ROOT/web" && bun run build)
test -f "$ROOT/web/dist/index.html"

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$DIST_STAGING/manager" ./cmd/manager
test -x "$DIST_STAGING/manager"

RUNTIME_DIR="$DIST_STAGING/sandbox/runtime" GOOS="$GOOS" GOARCH="$GOARCH" \
	TARGET_INSTALL_DIR=/home/vcap/app/.sandbox/bin \
	MANAGER_RUNTIME_DIR="$DIST_STAGING/manager-runtime" \
	MANAGER_TARGET_INSTALL_DIR=/home/vcap/app/manager-runtime/bin \
	BUN_RUNTIME_BIN="$BUN_RUNTIME_BIN" HERDR_RUNTIME_BIN="$HERDR_RUNTIME_BIN" CF_BIN="${CF_BIN:?CF_BIN is required}" \
	CF_BIN="${CF_BIN:?CF_BIN is required and must name a portable CF CLI executable}" \
	ALLOW_NIX_RUNTIME_RELOCATION="${ALLOW_NIX_RUNTIME_RELOCATION:-}" \
	bash "$BUILD_RUNTIME_SCRIPT"

mkdir -p "$DIST_STAGING/web"
cp -R "$ROOT/web/dist/." "$DIST_STAGING/web/"

for executable in manager manager-runtime/bin/bun manager-runtime/bin/cf manager-runtime/bin/collie sandbox/runtime/bin/bun sandbox/runtime/bin/herdr sandbox/runtime/bin/collie sandbox/runtime/bin/sandbox-bootstrap sandbox/runtime/start.sh; do
	test -x "$DIST_STAGING/$executable" || { printf 'error: missing executable artifact %s\n' "$executable" >&2; exit 1; }
done
for artifact in web/index.html sandbox/runtime/collie/bridge/index.ts sandbox/runtime/collie/cli/install-kind.ts sandbox/runtime/collie/cli/link.ts sandbox/runtime/collie/cli/sys.ts sandbox/runtime/collie/package.json sandbox/runtime/collie/web/dist/index.html; do
	test -f "$DIST_STAGING/$artifact" || { printf 'error: missing artifact %s\n' "$artifact" >&2; exit 1; }
done
test -d "$DIST_STAGING/sandbox/runtime/collie/node_modules" || { printf 'error: missing artifact sandbox/runtime/collie/node_modules\n' >&2; exit 1; }

rm -rf "$previous"
if [[ -e "$DIST_DIR" ]]; then mv "$DIST_DIR" "$previous"; fi
if ! mv "$DIST_STAGING" "$DIST_DIR"; then
	if [[ -e "$previous" ]]; then mv "$previous" "$DIST_DIR"; fi
	exit 1
fi
rm -rf "$previous"
printf 'manager distribution assembled at %s\n' "$DIST_DIR"
