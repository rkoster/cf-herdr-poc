#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
	printf 'usage: %s EXECUTABLE [ARG...]\n' "$0" >&2
	exit 2
fi
executable=$1
shift
directory="$(CDPATH= cd -- "$(dirname -- "$executable")" && pwd -P)"
name="$(basename -- "$executable")"
library_dir="$directory/.${name}-libs"
case "${TARGET_ARCH:-${GOARCH:-amd64}}" in
	amd64) loader=ld-linux-x86-64.so.2 ;;
	arm64) loader=ld-linux-aarch64.so.1 ;;
	*) printf 'error: unsupported target architecture\n' >&2; exit 1 ;;
esac
exec "$library_dir/$loader" --library-path "$library_dir" "$executable" "$@"
