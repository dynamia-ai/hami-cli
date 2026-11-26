#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:?usage: verify-release-archives.sh DIST_DIR VERSION}"
version="${2:?usage: verify-release-archives.sh DIST_DIR VERSION}"
[[ "$version" =~ ^[a-z0-9][a-z0-9._-]{0,127}$ ]]

# Keep this distribution matrix in sync with .goreleaser.yaml.
platforms=(linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64)
shopt -s nullglob
archives=("$dist_dir"/*.tar.gz)
checksums=("$dist_dir"/*.tar.gz.sha256)
if ((${#archives[@]} != ${#platforms[@]} || ${#checksums[@]} != ${#platforms[@]})); then
  echo "expected one archive and one checksum per supported platform in $dist_dir" >&2
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

for platform in "${platforms[@]}"; do
  name="hami-cli_${version}_$platform"
  archive="$dist_dir/$name.tar.gz"
  if [[ ! -f "$archive" ]]; then
    echo "missing release archive: $archive" >&2
    exit 1
  fi

  binary=hami-cli
  [[ "$platform" = windows_* ]] && binary=hami-cli.exe
  entry="$name/$binary"
  if ! listing="$(tar -tzf "$archive")" || ! types="$(tar -tvzf "$archive" | cut -c1)" ||
    ! { [[ "$listing" = "$entry" && "$types" = "-" ]] ||
        [[ "$listing" = "$name/"$'\n'"$entry" && "$types" = $'d\n-' ]]; }; then
    echo "expected only the regular file $name/$binary in $archive" >&2
    exit 1
  fi

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
