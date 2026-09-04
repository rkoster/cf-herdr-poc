#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
COLLIE_DIR="$ROOT/collie"
RUNTIME_DIR="${RUNTIME_DIR:-$ROOT/sandbox/runtime}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
TOOLS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cf-herdr-tools.XXXXXX")"
trap 'rm -rf "$TOOLS_DIR"' EXIT

require_tool() {
  local name=$1
  local path
  path="$(command -v "$name" 2>/dev/null || true)"
  if [[ -z "$path" ]]; then
    printf 'error: required binary %s was not found in PATH\n' "$name" >&2
    exit 1
  fi
  printf '%s' "$path"
}

validate_runtime_binary() {
	local variable=$1
	local path=${2:-${!variable:-}}
	if [[ -z "$path" ]]; then
		printf 'error: %s is required and must name a portable runtime executable\n' "$variable" >&2
		exit 1
	fi
	if [[ -L "$path" ]]; then
		printf 'error: %s must not be a symlink: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ ! -f "$path" || ! -x "$path" ]]; then
		printf 'error: %s must name a regular executable file: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ "$path" == /nix/store/* ]]; then
		printf 'error: %s must not come from /nix/store: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ "$(uname -s)" == Linux ]]; then
		local readelf_bin ldd_bin elf_details dependencies
		readelf_bin="$(require_tool readelf)"
		ldd_bin="$(require_tool ldd)"
		if ! "$readelf_bin" -h "$path" >/dev/null 2>&1; then
			printf 'error: %s must be an ELF executable on Linux: %s\n' "$variable" "$path" >&2
			exit 1
		fi
		elf_details="$("$readelf_bin" -l "$path" 2>&1)"
		dependencies="$("$ldd_bin" "$path" 2>&1 || true)"
		if [[ "$elf_details" == *'/nix/store/'* || "$dependencies" == *'/nix/store/'* ]]; then
			printf 'error: %s has non-portable /nix/store ELF dependencies: %s\n' "$variable" "$path" >&2
			exit 1
		fi
		if [[ "$dependencies" == *'not found'* ]]; then
			printf 'error: %s has unresolved ELF dependencies: %s\n' "$variable" "$path" >&2
			exit 1
		fi
	fi
	printf '%s' "$path"
}

build_bun="$(require_tool bun)"
bun_bin="$(validate_runtime_binary BUN_RUNTIME_BIN)"
herdr_bin="$(validate_runtime_binary HERDR_RUNTIME_BIN)"
if [[ ! -d "$COLLIE_DIR/.git" ]]; then
  printf 'error: Collie checkout is missing at %s\n' "$COLLIE_DIR" >&2
  exit 1
fi

(
	cd "$COLLIE_DIR"
	"$build_bun" install --frozen-lockfile
	"$build_bun" run build
)
collie_bin="$(validate_runtime_binary COLLIE_RUNTIME_BIN "$COLLIE_DIR/bin/collie")"
GOHOSTOS="$(go env GOHOSTOS)"
GOHOSTARCH="$(go env GOHOSTARCH)"
CGO_ENABLED=0 GOOS="$GOHOSTOS" GOARCH="$GOHOSTARCH" go build -o "$TOOLS_DIR/copytree" ./cmd/copytree

rm -rf "$RUNTIME_DIR"
mkdir -p "$RUNTIME_DIR/bin" "$RUNTIME_DIR/collie"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$RUNTIME_DIR/bin/sandbox-bootstrap" ./cmd/sandbox-bootstrap
test -x "$RUNTIME_DIR/bin/sandbox-bootstrap"
install -m 0755 "$bun_bin" "$RUNTIME_DIR/bin/bun"
install -m 0755 "$herdr_bin" "$RUNTIME_DIR/bin/herdr"
install -m 0755 "$collie_bin" "$RUNTIME_DIR/bin/collie"
install -m 0755 "$ROOT/sandbox/start.sh" "$RUNTIME_DIR/start.sh"

# Materialize only selected runtime assets. The copier rejects broken or escaping symlinks.
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/bridge" "$RUNTIME_DIR/collie/bridge"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/package.json" "$RUNTIME_DIR/collie/package.json"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/node_modules" "$RUNTIME_DIR/collie/node_modules"
mkdir -p "$RUNTIME_DIR/collie/web"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/web/dist" "$RUNTIME_DIR/collie/web/dist"

printf 'sandbox runtime assembled at %s\n' "$RUNTIME_DIR"
