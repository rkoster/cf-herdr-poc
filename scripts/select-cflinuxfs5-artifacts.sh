#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
arch="${1:-}"
case "$arch" in
  amd64|arm64) suffix="${arch^^}" ;;
  *) printf 'error: unsupported artifact architecture %s; expected amd64 or arm64\n' "$arch" >&2; exit 2 ;;
esac

# Indirect expansion keeps the manifest's uppercase architecture keys authoritative.
source "$ROOT/docker/cflinuxfs5-builder/artifacts.env"
missing=0
for name in BUN_URL BUN_SHA256 HERDR_URL HERDR_SHA256 CF_URL CF_SHA256; do
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
