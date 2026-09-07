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
for tool in readlink readelf ldd install; do
	command -v "$tool" >/dev/null 2>&1 || { printf 'error: required tool %s was not found in PATH\n' "$tool" >&2; exit 1; }
done

resolved_source="$(readlink -f -- "$source_path" 2>/dev/null || true)"
if [[ -z "$resolved_source" || ! -f "$resolved_source" || ! -x "$resolved_source" ]]; then
	printf 'error: source must resolve to a regular executable: %s\n' "$source_path" >&2
	exit 1
fi
if ! readelf -h "$resolved_source" >/dev/null 2>&1; then
	printf 'error: source must be a Linux ELF executable: %s\n' "$source_path" >&2
	exit 1
fi

interpreter=''
if command -v patchelf >/dev/null 2>&1; then
	interpreter="$(patchelf --print-interpreter "$resolved_source" 2>/dev/null || true)"
else
	interpreter="$(readelf -l "$resolved_source" | while IFS= read -r line; do
		if [[ "$line" =~ Requesting\ program\ interpreter:\ ([^]]+) ]]; then printf '%s\n' "${BASH_REMATCH[1]}"; fi
	done)"
fi
if [[ "$interpreter" != /* || ! -f "$interpreter" ]]; then
	printf 'error: ELF interpreter is missing or nonabsolute: %s\n' "$interpreter" >&2
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

declare -a libraries=("$interpreter")
while IFS= read -r line; do
	[[ -z "${line//[[:space:]]/}" ]] && continue
	if [[ "$line" =~ ^[[:space:]]*linux-vdso[^[:space:]]*[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		continue
	elif [[ "$line" =~ ^[[:space:]]*[^[:space:]]+[[:space:]]+\=\>[[:space:]]+(/[^[:space:]]+)[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		dependency="${BASH_REMATCH[1]}"
		if [[ "$line" != *"$interpreter =>"* ]]; then libraries+=("$dependency"); fi
	elif [[ "$line" =~ ^[[:space:]]*(/[^[:space:]]+)[[:space:]]+\(0x[[:xdigit:]]+\)[[:space:]]*$ ]]; then
		libraries+=("${BASH_REMATCH[1]}")
	else
		printf 'error: malformed ldd output: %s\n' "$line" >&2
		exit 1
	fi
done <<< "$dependencies"

destination_dir="$(dirname -- "$destination")"
destination_name="$(basename -- "$destination")"
payload="$destination.real"
library_dir="$destination_dir/.${destination_name}-libs"
mkdir -p "$destination_dir"
rm -rf "$library_dir"
mkdir -p "$library_dir"
install -m 0755 "$resolved_source" "$payload"

for library in "${libraries[@]}"; do
	resolved_library="$(readlink -f -- "$library" 2>/dev/null || true)"
	if [[ -z "$resolved_library" || ! -f "$resolved_library" ]]; then
		printf 'error: dependency does not resolve to a regular file: %s\n' "$library" >&2
		exit 1
	fi
	target="$library_dir/$(basename -- "$library")"
	if [[ -e "$target" ]] && ! cmp -s "$resolved_library" "$target"; then
		printf 'error: dependency basename collision: %s\n' "$library" >&2
		exit 1
	fi
	install -m 0755 "$resolved_library" "$target"
done

loader_name="$(basename -- "$interpreter")"
cat > "$destination" <<EOF
#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="\$(CDPATH= cd -- "\$(dirname -- "\$0")" && pwd -P)"
exec "\$SCRIPT_DIR/.${destination_name}-libs/${loader_name}" --library-path "\$SCRIPT_DIR/.${destination_name}-libs" "\$SCRIPT_DIR/${destination_name}.real" "\$@"
EOF
chmod 0755 "$destination"
