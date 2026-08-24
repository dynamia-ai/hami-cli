#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:?usage: verify-release-archives.sh DIST_DIR}"

shopt -s nullglob
archives=("$dist_dir"/*.tar.gz)
if ((${#archives[@]} == 0)); then
  echo "no release archives found in $dist_dir" >&2
  exit 1
fi
checksums=("$dist_dir"/*.tar.gz.sha256)
if ((${#checksums[@]} != ${#archives[@]})); then
  echo "each release archive must have one checksum file in $dist_dir" >&2
  exit 1
fi

if command -v sha256sum >/dev/null; then
  checksum_command=(sha256sum)
elif command -v shasum >/dev/null; then
  checksum_command=(shasum -a 256)
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

for archive in "${archives[@]}"; do
  if ! tar -tzf "$archive" >/dev/null; then
    echo "invalid release archive: $archive" >&2
    exit 1
  fi

  files=()
  while IFS= read -r entry; do
    if [[ "$entry" != */ ]]; then
      files+=("$entry")
    fi
  done < <(tar -tzf "$archive")

  if ((${#files[@]} != 1)); then
    echo "release archive must contain exactly one file: $archive" >&2
    printf '  %s\n' "${files[@]}" >&2
    exit 1
  fi

  case "${files[0]##*/}" in
    hami-cli | hami-cli.exe) ;;
    *)
      echo "unexpected release archive file in $archive: ${files[0]}" >&2
      exit 1
      ;;
  esac

  checksum="$archive.sha256"
  if [[ ! -f "$checksum" ]]; then
    echo "missing release archive checksum: $checksum" >&2
    exit 1
  fi
  if [[ "$(awk 'END { print NR }' "$checksum")" -ne 1 ]]; then
    echo "release archive checksum must contain exactly one entry: $checksum" >&2
    exit 1
  fi
  checksum_line="$(sed -n '1p' "$checksum")"
  if [[ ! "$checksum_line" =~ ^[0-9a-f]{64}$ ]]; then
    echo "invalid release archive checksum: $checksum" >&2
    exit 1
  fi
  actual_digest="$("${checksum_command[@]}" "$archive" | awk '{ print $1 }')"
  if [[ "$actual_digest" != "$checksum_line" ]]; then
    echo "release archive checksum mismatch: $checksum" >&2
    exit 1
  fi
done
