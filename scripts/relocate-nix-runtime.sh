#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	printf 'usage: %s SOURCE DEST\n' "$0" >&2
	exit 2
fi
if [[ "$(uname -s)" != Linux ]]; then
	printf 'error: Nix runtime relocation requires Linux\n' >&2
	exit 1
fi

source_path=$1
destination=$2
target_install_dir="${TARGET_INSTALL_DIR:-}"
target_arch="${TARGET_ARCH:-${GOARCH:-amd64}}"
if [[ -z "$target_install_dir" || "$target_install_dir" != /* || "$target_install_dir" =~ [[:space:][:cntrl:]] || "$target_install_dir" == *//* || "$target_install_dir" == */./* || "$target_install_dir" == */../* || "$target_install_dir" == */. || "$target_install_dir" == */.. ]]; then
	printf 'error: TARGET_INSTALL_DIR must be an absolute normalized path without whitespace or traversal: %s\n' "$target_install_dir" >&2
	exit 1
fi
for tool in readlink readelf ldd install patchelf sha256sum; do
	command -v "$tool" >/dev/null 2>&1 || { printf 'error: required tool %s was not found in PATH\n' "$tool" >&2; exit 1; }
done

resolved_source="$(readlink -f -- "$source_path" 2>/dev/null || true)"
if [[ -z "$resolved_source" || ! -f "$resolved_source" || ! -x "$resolved_source" ]]; then
	printf 'error: source must resolve to a regular executable: %s\n' "$source_path" >&2
	exit 1
fi
elf_header="$(readelf -h "$resolved_source" 2>/dev/null || true)"
if [[ -z "$elf_header" ]]; then
	printf 'error: source must be a Linux ELF executable: %s\n' "$source_path" >&2
	exit 1
fi
machine="$(printf '%s\n' "$elf_header" | while IFS= read -r line; do
	if [[ "$line" =~ ^[[:space:]]*Machine:[[:space:]]*(.+)$ ]]; then printf '%s' "${BASH_REMATCH[1]}"; fi
done)"
case "$target_arch:$machine" in
	amd64:Advanced\ Micro\ Devices\ X86-64) loader_soname=ld-linux-x86-64.so.2 ;;
	arm64:AArch64) loader_soname=ld-linux-aarch64.so.1 ;;
	amd64:*|arm64:*) printf 'error: ELF architecture %s does not match target %s\n' "$machine" "$target_arch" >&2; exit 1 ;;
	*) printf 'error: unsupported target architecture: %s\n' "$target_arch" >&2; exit 1 ;;
esac

source_interpreter="$(patchelf --print-interpreter "$resolved_source" 2>/dev/null || true)"
if [[ "$source_interpreter" != /* || ! -f "$source_interpreter" ]]; then
	printf 'error: ELF interpreter is missing or nonabsolute: %s\n' "$source_interpreter" >&2
	exit 1
fi

if ! dependencies="$(ldd "$resolved_source" 2>&1)"; then
	printf 'error: ldd failed for %s: %s\n' "$source_path" "$dependencies" >&2
	exit 1
fi
if [[ "$dependencies" == *'not found'* ]]; then
	printf 'error: unresolved dependency in ldd output: %s\n' "$dependencies" >&2
	exit 1
fi

declare -A ldd_paths=()
while IFS= read -r line; do
	[[ -z "${line//[[:space:]]/}" ]] && continue
	if [[ "$line" =~ ^[[:space:]]*linux-vdso[^[:space:]]*[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		continue
	elif [[ "$line" =~ ^[[:space:]]*([^[:space:]]+)[[:space:]]+\=\>[[:space:]]+(/[^[:space:]]+)[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		soname="${BASH_REMATCH[1]}"
		path="${BASH_REMATCH[2]}"
		if [[ "$soname" != "$source_interpreter" ]]; then ldd_paths["$soname"]="$path"; fi
	elif [[ "$line" =~ ^[[:space:]]*(/[^[:space:]]+)[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		path="${BASH_REMATCH[1]}"
		ldd_paths["$(basename -- "$path")"]="$path"
	else
		printf 'error: malformed ldd output: %s\n' "$line" >&2
		exit 1
	fi
done <<< "$dependencies"

declare -A closure_candidates=()
declare -a closure_roots=()
declare -A indexed_closures=()
add_nix_closure() {
	local path=$1 root candidate name
	[[ "$path" == /nix/store/* && -z "${indexed_closures[$path]:-}" ]] || return
	indexed_closures["$path"]=1
	while IFS= read -r root; do
		closure_roots+=("$root")
		shopt -s nullglob globstar
		for candidate in "$root"/lib/lib*.so* "$root"/lib64/lib*.so* "$root"/lib/**/lib*.so* "$root"/lib64/**/lib*.so*; do
			[[ -f "$candidate" ]] || continue
			name="$(basename -- "$candidate")"
			closure_candidates["$name"]+="${closure_candidates[$name]:+$'\n'}$candidate"
		done
		shopt -u globstar
	done < <(nix-store -qR "$path")
}
if [[ "$resolved_source" == /nix/store/* ]]; then
	command -v nix-store >/dev/null 2>&1 || { printf 'error: nix-store is required for Nix closure inspection\n' >&2; exit 1; }
	add_nix_closure "$resolved_source"
	for path in "${ldd_paths[@]}"; do
		add_nix_closure "$path"
	done
fi

destination_dir="$(dirname -- "$destination")"
destination_name="$(basename -- "$destination")"
library_dir="$destination_dir/.${destination_name}-libs"
target_interpreter="$target_install_dir/.${destination_name}-libs/$loader_soname"
mkdir -p "$destination_dir"
rm -rf "$library_dir"
mkdir -p "$library_dir"
declare -A installed_sonames=()
declare -A installed_payloads=()

copy_library() {
	local source=$1 soname=$2 resolved hash payload_name payload alias_target
	resolved="$(readlink -f -- "$source" 2>/dev/null || true)"
	[[ -n "$resolved" && -f "$resolved" ]] || { printf 'error: dependency does not resolve to a regular file: %s\n' "$source" >&2; exit 1; }
	if [[ -z "$soname" || "$soname" != "$(basename -- "$soname")" || "$soname" == . || "$soname" == .. ]]; then
		printf 'error: invalid DT_NEEDED SONAME: %s\n' "$soname" >&2
		exit 1
	fi
	if [[ -n "${installed_sonames[$soname]:-}" ]]; then
		if ! cmp -s "$resolved" "${installed_sonames[$soname]}"; then
			printf 'error: SONAME collision: %s resolves to differing content\n' "$soname" >&2
			exit 1
		fi
		printf '%s' "${installed_payloads[$soname]}"
		return
	fi
	hash="$(sha256sum "$resolved")"
	hash="${hash%% *}"
	payload_name=".${hash}-$(basename -- "$resolved")"
	payload="$library_dir/$payload_name"
	if [[ ! -e "$payload" ]]; then
		install -m 0755 "$resolved" "$payload"
		if [[ "$resolved" != "$(readlink -f -- "$source_interpreter")" ]] && readelf -h "$payload" >/dev/null 2>&1; then
			patchelf --set-rpath '\$ORIGIN' "$payload"
		fi
	fi
	alias_target="$library_dir/$soname"
	if [[ -e "$alias_target" || -L "$alias_target" ]]; then
		printf 'error: private library alias already exists: %s\n' "$soname" >&2
		exit 1
	fi
	ln -s -- "$payload_name" "$alias_target"
	if [[ "$(dirname -- "$(readlink -- "$alias_target")")" != . || "$(dirname -- "$(readlink -f -- "$alias_target")")" != "$library_dir" ]]; then
		printf 'error: private library alias escapes directory: %s\n' "$soname" >&2
		exit 1
	fi
	installed_sonames["$soname"]="$resolved"
	installed_payloads["$soname"]="$payload"
	printf '%s' "$payload"
}

resolve_needed() {
	local soname=$1 requester=$2 requester_dir rpath entry candidate first=''
	if [[ "$soname" == "$(basename -- "$source_interpreter")" ]]; then printf '%s' "$source_interpreter"; return; fi
	# Keep mixed Nix profile glibc components pinned to the executable's direct set.
	case "$soname" in
		libc.so.6|libpthread.so.0|libresolv.so.2|libdl.so.2|librt.so.1)
			if [[ -n "${ldd_paths[$soname]:-}" ]]; then readlink -f -- "${ldd_paths[$soname]}"; return; fi
			;;
	esac
	requester_dir="$(dirname -- "$requester")"
	if [[ -f "$requester_dir/$soname" ]]; then readlink -f -- "$requester_dir/$soname"; return; fi
	rpath="$(patchelf --print-rpath "$requester" 2>/dev/null || true)"
	IFS=: read -ra entries <<< "$rpath"
	for entry in "${entries[@]}"; do
		entry="${entry//\$ORIGIN/$requester_dir}"
		if [[ "$entry" == /* && -f "$entry/$soname" ]]; then readlink -f -- "$entry/$soname"; return; fi
	done
	if [[ -n "${ldd_paths[$soname]:-}" ]]; then readlink -f -- "${ldd_paths[$soname]}"; return; fi
	while IFS= read -r candidate; do
		[[ -n "$candidate" ]] || continue
		candidate="$(readlink -f -- "$candidate")"
		if [[ -z "$first" ]]; then first="$candidate"; continue; fi
		if ! cmp -s "$first" "$candidate"; then
			printf 'error: ambiguous Nix closure dependency %s\n' "$soname" >&2
			exit 1
		fi
	done <<< "${closure_candidates[$soname]:-}"
	if [[ -n "$first" ]]; then printf '%s' "$first"; return; fi
	printf 'error: unable to resolve DT_NEEDED dependency %s from %s\n' "$soname" "$requester" >&2
	exit 1
}

declare -A inspected=()
declare -a queue=("$resolved_source")
copy_library "$source_interpreter" "$loader_soname" >/dev/null
while ((${#queue[@]})); do
	requester="${queue[0]}"
	queue=("${queue[@]:1}")
	[[ -z "${inspected[$requester]:-}" ]] || continue
	inspected["$requester"]=1
	while IFS= read -r needed; do
		[[ -n "$needed" ]] || continue
		resolved="$(resolve_needed "$needed" "$requester")"
		copy_library "$resolved" "$needed" >/dev/null
		queue+=("$resolved")
	done < <(patchelf --print-needed "$requester")
done

for module in libnss_files.so.2 libnss_dns.so.2 libresolv.so.2; do
	if [[ -n "${closure_candidates[$module]:-}" ]]; then
		copy_library "$(resolve_needed "$module" "$source_interpreter")" "$module" >/dev/null
	fi
done

install -m 0755 "$resolved_source" "$destination"
patchelf --set-interpreter "$target_interpreter" --set-rpath "\$ORIGIN/.${destination_name}-libs" "$destination"
