#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
command -v docker >/dev/null 2>&1 || { printf 'error: Docker is required for BUILD_MODE=cflinuxfs5\n' >&2; exit 2; }
docker info >/dev/null 2>&1 || { printf 'error: Docker daemon is unavailable; start Docker or set BUILD_MODE=nix-relocation explicitly\n' >&2; exit 2; }

parent="$(dirname -- "${DIST_DIR:-$ROOT/dist}")"
dist="$(basename -- "${DIST_DIR:-$ROOT/dist}")"
mkdir -p "$parent"
staging="$(mktemp -d "$parent/.${dist}.docker.XXXXXX")"
cleanup() { rm -rf "$staging"; }
trap cleanup EXIT

args=(--file "$ROOT/docker/cflinuxfs5-builder/Dockerfile" --tag "cf-herdr-cflinuxfs5-builder:${BUILD_TAG:-local}" --output "type=local,dest=$staging" --build-arg "GOARCH=${GOARCH:-amd64}" --build-arg "GOOS=${GOOS:-linux}")
for name in BUN_URL BUN_SHA256 HERDR_URL HERDR_SHA256 CF_URL CF_SHA256; do
  if [[ -n "${!name:-}" ]]; then args+=(--build-arg "$name=${!name}"); fi
done
docker build "${args[@]}" "$ROOT"
test -x "$staging/manager" || { printf 'error: Docker output did not contain dist/manager\n' >&2; exit 1; }
previous="$parent/${dist}.previous"
rm -rf "$previous"
if [[ -e "$parent/$dist" ]]; then mv "$parent/$dist" "$previous"; fi
if ! mv "$staging" "$parent/$dist"; then
  [[ -e "$previous" ]] && mv "$previous" "$parent/$dist"
  exit 1
fi
rm -rf "$previous"
printf 'cflinuxfs5 distribution assembled at %s\n' "$parent/$dist"
