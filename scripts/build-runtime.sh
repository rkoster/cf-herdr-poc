#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
COLLIE_DIR="$ROOT/collie"
RUNTIME_DIR="$ROOT/sandbox/runtime"

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

bun_bin="$(require_tool bun)"
herdr_bin="$(require_tool herdr)"
if [[ ! -d "$COLLIE_DIR/.git" ]]; then
  printf 'error: Collie checkout is missing at %s\n' "$COLLIE_DIR" >&2
  exit 1
fi

(
  cd "$COLLIE_DIR"
  "$bun_bin" install --frozen-lockfile
  "$bun_bin" run build
)

rm -rf "$RUNTIME_DIR"
mkdir -p "$RUNTIME_DIR/bin" "$RUNTIME_DIR/collie"
install -m 0755 "$bun_bin" "$RUNTIME_DIR/bin/bun"
install -m 0755 "$herdr_bin" "$RUNTIME_DIR/bin/herdr"
install -m 0755 "$COLLIE_DIR/bin/collie" "$RUNTIME_DIR/bin/collie"
install -m 0755 "$ROOT/sandbox/start.sh" "$RUNTIME_DIR/start.sh"

# The source bridge needs the root dependency tree; the built web UI does not need web/node_modules.
cp -R "$COLLIE_DIR/bridge" "$COLLIE_DIR/package.json" "$RUNTIME_DIR/collie/"
cp -RL "$COLLIE_DIR/node_modules" "$RUNTIME_DIR/collie/"
mkdir -p "$RUNTIME_DIR/collie/web"
cp -R "$COLLIE_DIR/web/dist" "$RUNTIME_DIR/collie/web/"

printf 'sandbox runtime assembled at %s\n' "$RUNTIME_DIR"
