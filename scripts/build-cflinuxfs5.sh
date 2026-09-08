#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
command -v docker >/dev/null 2>&1 || { printf 'error: Docker is required for BUILD_MODE=cflinuxfs5\n' >&2; exit 2; }
docker info >/dev/null 2>&1 || { printf 'error: Docker daemon is unavailable; start Docker or set BUILD_MODE=nix-relocation explicitly\n' >&2; exit 2; }

GOARCH="${GOARCH:-amd64}"
case "$GOARCH" in
  amd64) TARGETARCH=amd64 ;;
  arm64) TARGETARCH=arm64 ;;
  *) printf 'error: unsupported GOARCH=%s; supported architectures are amd64 and arm64\n' "$GOARCH" >&2; exit 2 ;;
esac
artifact_file="$(mktemp)"
trap 'rm -f "$artifact_file"' EXIT
if ! bash "$ROOT/scripts/select-cflinuxfs5-artifacts.sh" "$TARGETARCH" >"$artifact_file"; then
  exit 2
fi
artifact_args=()
while IFS='=' read -r name value; do artifact_args+=(--build-arg "$name=$value"); done <"$artifact_file"

parent="$(dirname -- "${DIST_DIR:-$ROOT/dist}")"
dist="$(basename -- "${DIST_DIR:-$ROOT/dist}")"
mkdir -p "$parent"
staging="$(mktemp -d "$parent/.${dist}.docker.XXXXXX")"
cleanup() { rm -rf "$staging" "$artifact_file"; }
trap cleanup EXIT

args=(--file "$ROOT/docker/cflinuxfs5-builder/Dockerfile" --target output --tag "cf-herdr-cflinuxfs5-builder:${BUILD_TAG:-local}" --output "type=local,dest=$staging" --build-arg "TARGETARCH=$TARGETARCH" --build-arg "GOARCH=$GOARCH" --build-arg "GOOS=${GOOS:-linux}" --build-arg "DIRECT_SANDBOX=${DIRECT_SANDBOX:-}" --build-arg "SANDBOX_TARGET_INSTALL_DIR=${SANDBOX_TARGET_INSTALL_DIR:-}" "${artifact_args[@]}")
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
