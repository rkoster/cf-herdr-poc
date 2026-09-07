#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
arch="${1:-}"
case "$arch" in
  amd64|arm64) suffix="${arch^^}" ;;
  *) printf 'error: unsupported artifact architecture %s; expected amd64 or arm64\n' "$arch" >&2; exit 2 ;;
esac

# Read only literal manifest assignments; this file is data, not executable shell.
while IFS='=' read -r key value; do
  [[ -z "$key" || "$key" == \#* ]] && continue
  if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    printf 'error: invalid manifest key %s\n' "$key" >&2
    exit 2
  fi
  declare -g "$key=$value"
done < "$ROOT/docker/cflinuxfs5-builder/artifacts.env"

# Indirect expansion keeps architecture-specific manifest values as defaults.
missing=0
for name in BUN_URL BUN_SHA256 HERDR_URL HERDR_SHA256 CF_URL CF_SHA256 GO_URL GO_SHA256; do
  key="${name}_${suffix}"
  printf -v value '%s' "${!name:-${!key:-}}"
  if [[ -z "$value" ]]; then
    printf 'error: %s is required for %s in artifacts.env\n' "$name" "$arch" >&2
    missing=1
    continue
  fi
  printf '%s=%s\n' "$name" "$value"
done
exit "$missing"
