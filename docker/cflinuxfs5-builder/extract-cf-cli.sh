#!/bin/sh
set -eu

archive=$1
destination=$2
extract_dir=${destination}.extract

rm -rf "$extract_dir"
mkdir -p "$extract_dir" "$destination"
case "$archive" in
  *.tar.gz|*.tgz) tar -xzf "$archive" -C "$extract_dir" ;;
  *.zip) unzip -q "$archive" -d "$extract_dir" ;;
  *) echo 'CF_URL must be a .tgz, .tar.gz, or .zip archive' >&2; exit 2 ;;
esac

candidate=
for path in "$extract_dir/cf" "$extract_dir/cf8"; do
  if [ -f "$path" ] && [ -x "$path" ]; then
    candidate=$path
    break
  fi
done

if [ -z "$candidate" ]; then
  while IFS= read -r path; do
    if [ -f "$path" ] && [ -x "$path" ]; then
      candidate=$path
      break
    fi
  done <<EOF
$(find "$extract_dir" -type f \( -name cf -o -name cf8 \))
EOF
fi

if [ -z "$candidate" ]; then
  echo 'CF CLI archive contains no regular executable named cf or cf8' >&2
  exit 1
fi

install -m 0755 "$candidate" "$destination/cf"
test -x "$destination/cf"
rm -rf "$extract_dir"
