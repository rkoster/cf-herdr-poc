#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
COLLIE_DIR="$ROOT/collie"
RUNTIME_DIR="${RUNTIME_DIR:-$ROOT/sandbox/runtime}"
TARGET_INSTALL_DIR="${TARGET_INSTALL_DIR:-}"
MANAGER_RUNTIME_DIR="${MANAGER_RUNTIME_DIR:-}"
MANAGER_TARGET_INSTALL_DIR="${MANAGER_TARGET_INSTALL_DIR:-}"
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

validate_target_install_dir() {
	local variable=$1 path=${!1:-}
	if [[ -z "$path" || "$path" != /* || "$path" =~ [[:space:][:cntrl:]] || "$path" == *//* || "$path" == */./* || "$path" == */../* || "$path" == */. || "$path" == */.. ]]; then
		printf 'error: %s must be an absolute normalized path without whitespace or traversal: %s\n' "$variable" "$path" >&2
		exit 1
	fi
}

validate_target_install_dir TARGET_INSTALL_DIR
if [[ -n "$MANAGER_RUNTIME_DIR" ]]; then
	validate_target_install_dir MANAGER_TARGET_INSTALL_DIR
fi

validate_runtime_binary() {
	local variable=$1
	local path=${2:-${!variable:-}}
	if [[ -z "$path" ]]; then
		printf 'error: %s is required and must name a portable runtime executable\n' "$variable" >&2
		exit 1
	fi
	if [[ "${ALLOW_NIX_RUNTIME_RELOCATION:-}" == 1 ]]; then
		path="$(readlink -f -- "$path" 2>/dev/null || true)"
	elif [[ -L "$path" ]]; then
		printf 'error: %s must not be a symlink: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ ! -f "$path" || ! -x "$path" ]]; then
		printf 'error: %s must name a regular executable file: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ "${ALLOW_NIX_RUNTIME_RELOCATION:-}" != 1 && "$path" == /nix/store/* ]]; then
		printf 'error: %s must not come from /nix/store: %s\n' "$variable" "$path" >&2
		exit 1
	fi
	if [[ "$(uname -s)" == Linux ]]; then
		local readelf_bin ldd_bin elf_details dependencies machine expected_machine
		readelf_bin="$(require_tool readelf)"
		ldd_bin="$(require_tool ldd)"
		if ! "$readelf_bin" -h "$path" >/dev/null 2>&1; then
			printf 'error: %s must be an ELF executable on Linux: %s\n' "$variable" "$path" >&2
			exit 1
		fi
		elf_details="$("$readelf_bin" -l "$path" 2>&1)"
		machine="$("$readelf_bin" -h "$path" | while IFS= read -r line; do if [[ "$line" =~ ^[[:space:]]*Machine:[[:space:]]*(.+)$ ]]; then printf '%s' "${BASH_REMATCH[1]}"; fi; done)"
		case "$GOARCH" in
			amd64) expected_machine='Advanced Micro Devices X86-64' ;;
			arm64) expected_machine='AArch64' ;;
			*) printf 'error: unsupported GOARCH for runtime binaries: %s\n' "$GOARCH" >&2; exit 1 ;;
		esac
		if [[ "$machine" != "$expected_machine" ]]; then
			printf 'error: %s ELF architecture %s does not match GOARCH %s\n' "$variable" "$machine" "$GOARCH" >&2
			exit 1
		fi
		dependencies="$("$ldd_bin" "$path" 2>&1 || true)"
		if [[ "${ALLOW_NIX_RUNTIME_RELOCATION:-}" != 1 && ( "$elf_details" == *'/nix/store/'* || "$dependencies" == *'/nix/store/'* ) ]]; then
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

if [[ -n "${ALLOW_NIX_RUNTIME_RELOCATION:-}" && "${ALLOW_NIX_RUNTIME_RELOCATION:-}" != 1 ]]; then
	printf 'error: ALLOW_NIX_RUNTIME_RELOCATION must be exactly 1 when enabled\n' >&2
	exit 1
fi

relocate_runtime() {
	TARGET_INSTALL_DIR="$3" TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/relocate-nix-runtime.sh" "$1" "$2"
}

scan_elf_metadata() {
	local root=$1 path interpreter rpath
	shopt -s nullglob globstar
	for path in "$root"/**/*; do
		[[ -f "$path" ]] || continue
		if readelf -h "$path" >/dev/null 2>&1; then
			interpreter="$(patchelf --print-interpreter "$path" 2>/dev/null || true)"
			rpath="$(patchelf --print-rpath "$path" 2>/dev/null || true)"
			if [[ "$interpreter" == *'/nix/store/'* || "$rpath" == *'/nix/store/'* ]]; then
				printf 'error: packaged ELF retains Nix loader metadata: %s\n' "$path" >&2
				exit 1
			fi
		fi
	done
	shopt -u globstar
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
	"$build_bun" install --frozen-lockfile --offline
	"$build_bun" run build
)
collie_bin="$(validate_runtime_binary COLLIE_RUNTIME_BIN "$COLLIE_DIR/bin/collie")"
GOHOSTOS="$(go env GOHOSTOS)"
GOHOSTARCH="$(go env GOHOSTARCH)"
CGO_ENABLED=0 GOOS="$GOHOSTOS" GOARCH="$GOHOSTARCH" go build -o "$TOOLS_DIR/copytree" ./cmd/copytree
CGO_ENABLED=0 GOOS="$GOHOSTOS" GOARCH="$GOHOSTARCH" go build -o "$TOOLS_DIR/checkimports" ./cmd/checkimports

rm -rf "$RUNTIME_DIR"
mkdir -p "$RUNTIME_DIR/bin" "$RUNTIME_DIR/collie"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$RUNTIME_DIR/bin/sandbox-bootstrap" ./cmd/sandbox-bootstrap
test -x "$RUNTIME_DIR/bin/sandbox-bootstrap"
if [[ "${ALLOW_NIX_RUNTIME_RELOCATION:-}" == 1 ]]; then
	relocate_runtime "$bun_bin" "$RUNTIME_DIR/bin/bun" "$TARGET_INSTALL_DIR"
	relocate_runtime "$herdr_bin" "$RUNTIME_DIR/bin/herdr" "$TARGET_INSTALL_DIR"
	relocate_runtime "$collie_bin" "$RUNTIME_DIR/bin/collie" "$TARGET_INSTALL_DIR"
	TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/smoke-relocated-runtime.sh" "$RUNTIME_DIR/bin/bun" --version >/dev/null
	TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/smoke-relocated-runtime.sh" "$RUNTIME_DIR/bin/herdr" --version >/dev/null
	TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/smoke-relocated-runtime.sh" "$RUNTIME_DIR/bin/collie" --version >/dev/null
else
	install -m 0755 "$bun_bin" "$RUNTIME_DIR/bin/bun"
	install -m 0755 "$herdr_bin" "$RUNTIME_DIR/bin/herdr"
	install -m 0755 "$collie_bin" "$RUNTIME_DIR/bin/collie"
fi

if [[ -n "$MANAGER_RUNTIME_DIR" ]]; then
	rm -rf "$MANAGER_RUNTIME_DIR"
	mkdir -p "$MANAGER_RUNTIME_DIR/bin"
	if [[ "${ALLOW_NIX_RUNTIME_RELOCATION:-}" == 1 ]]; then
		relocate_runtime "$bun_bin" "$MANAGER_RUNTIME_DIR/bin/bun" "$MANAGER_TARGET_INSTALL_DIR"
		relocate_runtime "$collie_bin" "$MANAGER_RUNTIME_DIR/bin/collie" "$MANAGER_TARGET_INSTALL_DIR"
		TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/smoke-relocated-runtime.sh" "$MANAGER_RUNTIME_DIR/bin/bun" --version >/dev/null
		TARGET_ARCH="$GOARCH" bash "$ROOT/scripts/smoke-relocated-runtime.sh" "$MANAGER_RUNTIME_DIR/bin/collie" --version >/dev/null
	else
		install -m 0755 "$bun_bin" "$MANAGER_RUNTIME_DIR/bin/bun"
		install -m 0755 "$collie_bin" "$MANAGER_RUNTIME_DIR/bin/collie"
	fi
	scan_elf_metadata "$MANAGER_RUNTIME_DIR"
fi
install -m 0755 "$ROOT/sandbox/start.sh" "$RUNTIME_DIR/start.sh"

# Materialize only selected runtime assets. The copier rejects broken or escaping symlinks.
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/bridge" "$RUNTIME_DIR/collie/bridge"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/cli" "$RUNTIME_DIR/collie/cli"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/package.json" "$RUNTIME_DIR/collie/package.json"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/node_modules" "$RUNTIME_DIR/collie/node_modules"
mkdir -p "$RUNTIME_DIR/collie/web"
"$TOOLS_DIR/copytree" "$COLLIE_DIR" "$COLLIE_DIR/web/dist" "$RUNTIME_DIR/collie/web/dist"
"$TOOLS_DIR/checkimports" "$RUNTIME_DIR/collie" "$RUNTIME_DIR/collie/bridge/index.ts"
scan_elf_metadata "$RUNTIME_DIR"

printf 'sandbox runtime assembled at %s\n' "$RUNTIME_DIR"
