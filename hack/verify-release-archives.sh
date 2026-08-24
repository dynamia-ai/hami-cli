#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:?usage: verify-release-archives.sh DIST_DIR}"

shopt -s nullglob
archives=("$dist_dir"/*.tar.gz)
if ((${#archives[@]} == 0)); then
  echo "no release archives found in $dist_dir" >&2
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
done
